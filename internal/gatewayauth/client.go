package gatewayauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/dial"
)

const maxResponseBytes = 1 << 20

type Options struct {
	Server        string
	ResolveIP     string
	TLSInsecure   bool
	UpstreamProxy string
	Timeout       time.Duration
}

type Result struct {
	Code        int
	NextService string
}

func (result Result) NeedsSMS() bool {
	return strings.Contains(strings.ToLower(result.NextService), "sms")
}

func (result Result) Accepted() bool {
	return result.Code == 1 && strings.TrimSpace(result.NextService) == ""
}

type Client struct {
	baseURL       *url.URL
	http          *http.Client
	authenticated bool
	sessionID     []byte
}

// Close clears authentication material retained before TakeSession and closes
// idle gateway connections. It is safe to call after a successful transfer.
func (client *Client) Close() error {
	if client == nil {
		return nil
	}
	clear(client.sessionID)
	client.sessionID = nil
	client.authenticated = false
	if client.http != nil {
		client.http.CloseIdleConnections()
		jar, err := cookiejar.New(nil)
		if err != nil {
			return err
		}
		client.http.Jar = jar
	}
	client.http = nil
	client.baseURL = nil
	return nil
}

type authXML struct {
	ErrorCode    int    `xml:"ErrorCode"`
	NextService  string `xml:"NextService"`
	RSAKey       string `xml:"RSA_ENCRYPT_KEY"`
	RSAExponent  string `xml:"RSA_ENCRYPT_EXP"`
	CSRFRandCode string `xml:"CSRF_RAND_CODE"`
	SessionID    string `xml:"TwfID"`
}

func New(options Options) (*Client, error) {
	host, address, err := dial.SplitServer(options.Server)
	if err != nil {
		return nil, err
	}
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}
	if options.ResolveIP != "" && net.ParseIP(options.ResolveIP) == nil {
		return nil, errors.New("resolve IP must be a numeric address")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("create cookie jar: %w", err)
	}
	baseDial, err := dial.New(options.UpstreamProxy, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("prepare outbound dialer: %w", err)
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			ServerName:         host,
			InsecureSkipVerify: options.TLSInsecure, // development opt-in from validated local config
		},
	}
	if options.ResolveIP != "" {
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			addressHost, addressPort, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, splitErr
			}
			if strings.EqualFold(addressHost, host) {
				address = net.JoinHostPort(options.ResolveIP, addressPort)
			}
			return baseDial(ctx, network, address)
		}
	} else {
		transport.DialContext = baseDial
	}

	return &Client{
		baseURL: &url.URL{Scheme: "https", Host: address},
		http:    &http.Client{Transport: transport, Jar: jar, Timeout: options.Timeout},
	}, nil
}

func (client *Client) AuthenticatePassword(ctx context.Context, username string, password []byte) (Result, error) {
	initialized, err := client.request(ctx, http.MethodGet, "/por/login_auth.csp?apiversion=1", nil)
	if err != nil {
		return Result{}, fmt.Errorf("initialize authentication: %w", err)
	}
	if initialized.ErrorCode != 1 {
		return Result{}, fmt.Errorf("initialize authentication: gateway code %d", initialized.ErrorCode)
	}
	client.rememberSession(initialized.SessionID)
	encrypted, err := encryptPassword(password, initialized.CSRFRandCode, initialized.RSAKey, initialized.RSAExponent)
	if err != nil {
		return Result{}, err
	}
	form := url.Values{
		"mitm_result":       {""},
		"svpn_req_randcode": {initialized.CSRFRandCode},
		"svpn_name":         {username},
		"svpn_password":     {encrypted},
		"svpn_rand_code":    {""},
	}
	response, err := client.request(ctx, http.MethodPost, "/por/login_psw.csp?anti_replay=1&encrypt=1&apiversion=1", form)
	if err != nil {
		return Result{}, fmt.Errorf("password authentication: %w", err)
	}
	return client.observe(response), nil
}

// PrepareSMS enters the gateway's SMS authentication stage. The reference
// client performs this request before submitting a verification code, even
// when the password response says that an existing code is still valid.
func (client *Client) PrepareSMS(ctx context.Context) error {
	response, err := client.request(ctx, http.MethodPost, "/por/login_sms.csp", url.Values{})
	if err != nil {
		return fmt.Errorf("initialize SMS authentication: %w", err)
	}
	client.rememberSession(response.SessionID)
	if response.ErrorCode != 1 {
		return fmt.Errorf("initialize SMS authentication: gateway code %d", response.ErrorCode)
	}
	return nil
}

func (client *Client) AuthenticateSMS(ctx context.Context, code []byte) (Result, error) {
	form := url.Values{"svpn_inputsms": {string(code)}}
	response, err := client.request(ctx, http.MethodPost, "/por/login_sms1.csp?apiversion=1", form)
	if err != nil {
		return Result{}, fmt.Errorf("SMS authentication: %w", err)
	}
	return client.observe(response), nil
}

func (client *Client) observe(response authXML) Result {
	client.rememberSession(response.SessionID)
	result := resultFromXML(response)
	client.authenticated = result.Accepted()
	return result
}

func (client *Client) rememberSession(id string) {
	if id == "" {
		return
	}
	clear(client.sessionID)
	client.sessionID = append(client.sessionID[:0], id...)
}

func (client *Client) request(ctx context.Context, method, path string, form url.Values) (authXML, error) {
	data, err := requestBytes(ctx, client.http, client.baseURL, method, path, form)
	if err != nil {
		return authXML{}, err
	}
	defer clear(data)
	var envelope authXML
	if err := xml.Unmarshal(data, &envelope); err != nil {
		return authXML{}, fmt.Errorf("decode gateway XML: %w", err)
	}
	return envelope, nil
}

func requestBytes(ctx context.Context, httpClient *http.Client, baseURL *url.URL, method, path string, form url.Values) ([]byte, error) {
	parsed, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("parse gateway endpoint: %w", err)
	}
	endpoint := baseURL.ResolveReference(parsed)
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(data) > maxResponseBytes {
		return nil, errors.New("gateway response exceeds size limit")
	}
	return data, nil
}

func encryptPassword(password []byte, csrf, modulusHex, exponentText string) (string, error) {
	modulus, ok := new(big.Int).SetString(strings.TrimSpace(modulusHex), 16)
	if !ok || modulus.Sign() <= 0 {
		return "", errors.New("gateway returned an invalid RSA modulus")
	}
	exponent, err := strconv.Atoi(strings.TrimSpace(exponentText))
	if err != nil || exponent < 3 {
		return "", errors.New("gateway returned an invalid RSA exponent")
	}
	plaintext := make([]byte, 0, len(password)+1+len(csrf))
	plaintext = append(plaintext, password...)
	plaintext = append(plaintext, '_')
	plaintext = append(plaintext, csrf...)
	defer clear(plaintext)
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, &rsa.PublicKey{N: modulus, E: exponent}, plaintext)
	if err != nil {
		return "", fmt.Errorf("encrypt password: %w", err)
	}
	return hex.EncodeToString(ciphertext), nil
}

func resultFromXML(response authXML) Result {
	return Result{
		Code:        response.ErrorCode,
		NextService: strings.TrimSpace(response.NextService),
	}
}
