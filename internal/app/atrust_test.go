package app

import (
	"context"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
)

func TestSelectATrustAuthenticationMethodDefaultsToOAuth(t *testing.T) {
	methods := []backend.AuthenticationMethod{
		{Type: ATrustPasswordAuthType, Domain: "openldap13924"},
		{Type: ATrustOAuthAuthType, Domain: "customOAuth51300"},
	}
	selected, err := SelectATrustAuthenticationMethod(methods, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Type != ATrustOAuthAuthType || selected.Domain != "customOAuth51300" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestSelectATrustAuthenticationMethodSupportsNJUPasswordDomain(t *testing.T) {
	methods := []backend.AuthenticationMethod{
		{Type: ATrustOAuthAuthType, Domain: "customOAuth51300"},
		{Type: ATrustPasswordAuthType, Domain: "openldap13924", Name: "LDAP认证"},
	}
	selected, err := SelectATrustAuthenticationMethod(methods, ATrustPasswordAuthType, "openldap13924")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Type != ATrustPasswordAuthType || selected.Domain != "openldap13924" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestSelectATrustAuthenticationMethodRejectsUnknownMethod(t *testing.T) {
	_, err := SelectATrustAuthenticationMethod(nil, ATrustPasswordAuthType, "openldap13924")
	if err == nil || !strings.Contains(err.Error(), "auth/psw") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateATrustAuthenticationType(t *testing.T) {
	for _, authType := range []string{ATrustOAuthAuthType, ATrustPasswordAuthType} {
		if err := ValidateATrustAuthenticationType(authType); err != nil {
			t.Fatalf("%q rejected: %v", authType, err)
		}
	}
	if err := ValidateATrustAuthenticationType("auth/cas"); err == nil {
		t.Fatal("unsupported auth type accepted")
	}
}

func TestParseATrustEndpoint(t *testing.T) {
	endpoint, err := ParseATrustEndpoint("vpn.example.edu:8443")
	if err != nil || endpoint.Host != "vpn.example.edu" || endpoint.Port != 8443 {
		t.Fatalf("endpoint=%+v err=%v", endpoint, err)
	}
	if _, err := ParseATrustEndpoint("vpn.example.edu:https"); err == nil {
		t.Fatal("named port accepted")
	}
}

func TestDiscoverATrustRejectsUsageErrors(t *testing.T) {
	for _, testCase := range []struct{ backend, server string }{
		{"openvpn", "vpn.example.edu"},
		{"easyconnect", "vpn.example.edu"},
		{"atrust", "vpn.example.edu:https"},
	} {
		if _, err := DiscoverATrust(context.Background(), Deps{}, testCase.backend, testCase.server); !IsUsage(err) {
			t.Fatalf("DiscoverATrust(%q, %q) = %v", testCase.backend, testCase.server, err)
		}
	}
}
