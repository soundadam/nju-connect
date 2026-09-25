package atrustbackend

import (
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
)

func TestParseOAuthCallbackCodeAcceptsExplicitDefaultPort(t *testing.T) {
	endpoint := backend.ATrustEndpoint("vpn.nju.edu.cn", 443)
	code, err := ParseOAuthCallbackCode("https://vpn.nju.edu.cn:443/passport/v1/auth/httpsOauth2?state=123&code=local-code", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if code != "local-code" {
		t.Fatalf("code = %q", code)
	}
}

func TestParseOAuthCallbackCodeRejectsDifferentHost(t *testing.T) {
	endpoint := backend.ATrustEndpoint("vpn.nju.edu.cn", 443)
	if _, err := ParseOAuthCallbackCode("https://example.com/passport/v1/auth/httpsOauth2?code=secret", endpoint); err == nil {
		t.Fatal("ParseOAuthCallbackCode() unexpectedly accepted another host")
	}
}

func TestParseOAuthCallbackCodeRejectsMissingCode(t *testing.T) {
	endpoint := backend.ATrustEndpoint("vpn.nju.edu.cn", 443)
	if _, err := ParseOAuthCallbackCode("https://vpn.nju.edu.cn/passport/v1/auth/httpsOauth2?state=123", endpoint); err == nil {
		t.Fatal("ParseOAuthCallbackCode() unexpectedly accepted a missing code")
	}
}
