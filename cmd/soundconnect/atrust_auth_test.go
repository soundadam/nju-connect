package main

import (
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
)

func TestSelectATrustAuthenticationMethodDefaultsToOAuth(t *testing.T) {
	methods := []backend.AuthenticationMethod{
		{Type: atrustPasswordAuthType, Domain: "openldap13924"},
		{Type: atrustOAuthAuthType, Domain: "customOAuth51300"},
	}
	selected, err := selectATrustAuthenticationMethod(methods, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Type != atrustOAuthAuthType || selected.Domain != "customOAuth51300" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestSelectATrustAuthenticationMethodSupportsNJUPasswordDomain(t *testing.T) {
	methods := []backend.AuthenticationMethod{
		{Type: atrustOAuthAuthType, Domain: "customOAuth51300"},
		{Type: atrustPasswordAuthType, Domain: "openldap13924", Name: "LDAP认证"},
	}
	selected, err := selectATrustAuthenticationMethod(methods, atrustPasswordAuthType, "openldap13924")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Type != atrustPasswordAuthType || selected.Domain != "openldap13924" {
		t.Fatalf("selected = %+v", selected)
	}
}

func TestSelectATrustAuthenticationMethodRejectsUnknownMethod(t *testing.T) {
	_, err := selectATrustAuthenticationMethod(nil, atrustPasswordAuthType, "openldap13924")
	if err == nil || !strings.Contains(err.Error(), "auth/psw") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateATrustAuthenticationType(t *testing.T) {
	for _, authType := range []string{atrustOAuthAuthType, atrustPasswordAuthType} {
		if err := validateATrustAuthenticationType(authType); err != nil {
			t.Fatalf("%q rejected: %v", authType, err)
		}
	}
	if err := validateATrustAuthenticationType("auth/cas"); err == nil {
		t.Fatal("unsupported auth type accepted")
	}
}
