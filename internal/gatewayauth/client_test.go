package gatewayauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestEncryptPassword(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := encryptPassword([]byte("secret"), "nonce", privateKey.N.Text(16), "65537")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := hex.DecodeString(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := rsa.DecryptPKCS1v15(rand.Reader, privateKey, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(plaintext), "secret_nonce"; got != want {
		t.Fatalf("plaintext = %q, want %q", got, want)
	}
}

func TestResultState(t *testing.T) {
	result := Result{Code: 1, NextService: "auth/sms"}
	if !result.NeedsSMS() || result.Accepted() {
		t.Fatalf("unexpected SMS state: %+v", result)
	}
	result.NextService = ""
	if !result.Accepted() {
		t.Fatalf("expected accepted state: %+v", result)
	}
}

func TestPasswordAndSMSAuthentication(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const csrf = "test-nonce"
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/por/login_auth.csp":
			http.SetCookie(writer, &http.Cookie{Name: "session", Value: "present", Path: "/"})
			fmt.Fprintf(writer, "<Auth><ErrorCode>1</ErrorCode><TwfID>gateway-session</TwfID><RSA_ENCRYPT_KEY>%s</RSA_ENCRYPT_KEY><RSA_ENCRYPT_EXP>65537</RSA_ENCRYPT_EXP><CSRF_RAND_CODE>%s</CSRF_RAND_CODE></Auth>", privateKey.N.Text(16), csrf)
		case "/por/login_psw.csp":
			if cookie, cookieErr := request.Cookie("session"); cookieErr != nil || cookie.Value != "present" {
				t.Errorf("session cookie missing: %v", cookieErr)
			}
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			ciphertext, decodeErr := hex.DecodeString(request.Form.Get("svpn_password"))
			if decodeErr != nil {
				t.Error(decodeErr)
			}
			plaintext, decryptErr := rsa.DecryptPKCS1v15(rand.Reader, privateKey, ciphertext)
			if decryptErr != nil {
				t.Error(decryptErr)
			}
			if got, want := string(plaintext), "secret_"+csrf; got != want {
				t.Errorf("password plaintext = %q, want %q", got, want)
			}
			fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode><NextService>auth/sms</NextService><SmsIsStillValid>1</SmsIsStillValid></Auth>")
		case "/por/login_sms1.csp":
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if got := request.Form.Get("svpn_inputsms"); got != "123456" {
				t.Errorf("SMS code = %q", got)
			}
			fmt.Fprint(writer, "<Auth><ErrorCode>1</ErrorCode></Auth>")
		case "/por/conf.csp":
			assertSessionCookie(t, request)
			fmt.Fprint(writer, "<Conf><Policy/></Conf>")
		case "/por/rclist.csp":
			assertSessionCookie(t, request)
			fmt.Fprint(writer, "<Resource><Group/></Resource>")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	parsedURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(Options{Server: parsedURL.Host, TLSInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.AuthenticatePassword(context.Background(), "account", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.NeedsSMS() {
		t.Fatalf("password result = %+v", result)
	}
	result, err = client.AuthenticateSMS(context.Background(), []byte("123456"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Accepted() {
		t.Fatalf("SMS result = %+v", result)
	}
	session, err := client.TakeSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if state := session.State(); state.CookieCount != 1 || !state.HasID {
		t.Fatalf("session state = %+v", state)
	}
	bootstrap, err := session.ProbeBootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !bootstrap.ConfigurationAvailable || !bootstrap.ResourcesAvailable {
		t.Fatalf("bootstrap = %+v", bootstrap)
	}
	if _, err := client.TakeSession(); err != ErrNoAuthenticatedSession {
		t.Fatalf("second TakeSession error = %v", err)
	}
}

func assertSessionCookie(t *testing.T, request *http.Request) {
	t.Helper()
	if cookie, err := request.Cookie("session"); err != nil || cookie.Value != "present" {
		t.Errorf("session cookie missing: %v", err)
	}
}
