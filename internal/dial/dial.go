// Package dial owns the outbound network policy shared by future gateway
// authentication and transport implementations.
package dial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

var ErrUpstreamUnavailable = errors.New("upstream proxy is unavailable")

type ContextFunc func(context.Context, string, string) (net.Conn, error)

func New(proxyURL string, timeout time.Duration) (ContextFunc, error) {
	base := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	address, configured, err := upstreamAddress(proxyURL)
	if err != nil {
		return nil, err
	}
	if !configured {
		return base.DialContext, nil
	}

	dialer, err := proxy.SOCKS5("tcp", address, nil, base)
	if err != nil {
		return nil, fmt.Errorf("create upstream SOCKS dialer: %w", err)
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, errors.New("upstream SOCKS dialer does not support cancellation")
	}
	return contextDialer.DialContext, nil
}

// ProbeUpstream checks only whether the configured proxy listener accepts a
// TCP connection. It does not open credentials or contact a gateway.
func ProbeUpstream(ctx context.Context, proxyURL string) error {
	address, configured, err := upstreamAddress(proxyURL)
	if err != nil {
		return err
	}
	if !configured {
		return nil
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	return connection.Close()
}

// SplitServer normalizes a gateway host to an address with a default TLS port.
func SplitServer(server string) (host string, address string, err error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", "", errors.New("server is required")
	}
	if strings.Contains(server, "://") {
		return "", "", errors.New("server must be host:port, not a URL")
	}
	host, port, err := net.SplitHostPort(server)
	if err != nil {
		if strings.Contains(err.Error(), "missing port") && !strings.Contains(server, "]") {
			host = server
			port = "443"
		} else {
			return "", "", fmt.Errorf("invalid server address: %w", err)
		}
	}
	if host == "" || port == "" {
		return "", "", errors.New("server must include a host and port")
	}
	return host, net.JoinHostPort(host, port), nil
}

func upstreamAddress(proxyURL string) (string, bool, error) {
	if strings.TrimSpace(proxyURL) == "" {
		return "", false, nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return "", false, fmt.Errorf("parse upstream proxy: %w", err)
	}
	if parsed.Scheme != "socks5" && parsed.Scheme != "socks5h" {
		return "", false, fmt.Errorf("unsupported upstream proxy scheme %q", parsed.Scheme)
	}
	if parsed.User != nil {
		return "", false, errors.New("credentials in upstream proxy URLs are unsupported")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return "", false, errors.New("upstream SOCKS proxy must include host and port")
	}
	return parsed.Host, true, nil
}
