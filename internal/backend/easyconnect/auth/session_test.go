package gatewayauth

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/soundadam/soundconnect/internal/sessiontoken"
)

func TestTakeSessionRequiresAcceptedAuthentication(t *testing.T) {
	client := &Client{}
	if _, err := client.TakeSession(); err != ErrNoAuthenticatedSession {
		t.Fatalf("TakeSession error = %v", err)
	}
}

func TestClosedSessionHasNoState(t *testing.T) {
	session := &Session{sessionID: []byte("secret")}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if state := session.State(); state != (SessionState{}) {
		t.Fatalf("closed session state = %+v", state)
	}
}

func TestWithIDLendsAndClearsCopy(t *testing.T) {
	session := &Session{http: &http.Client{}, sessionID: []byte("session")}
	var borrowed []byte
	if err := session.WithID(func(id []byte) error {
		borrowed = id
		if string(id) != "session" {
			t.Fatalf("borrowed ID differs")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, value := range borrowed {
		if value != 0 {
			t.Fatal("borrowed ID was not cleared")
		}
	}
	if string(session.sessionID) != "session" {
		t.Fatal("owned session ID was modified")
	}
}

func TestWithNativeGatewayTokenLendsAndClearsCopy(t *testing.T) {
	const sessionID = "fedcba9876543210"
	tokenFixture := append(make([]byte, 32), []byte{0x20, 0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27,
		0x28, 0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f}...)
	ownedToken := append([]byte(nil), tokenFixture...)
	session := &Session{
		http:               &http.Client{},
		sessionID:          []byte(sessionID),
		nativeGatewayToken: sessiontoken.NativeGatewayToken(ownedToken),
	}
	var borrowed sessiontoken.NativeGatewayToken
	if err := session.WithNativeGatewayToken(func(token sessiontoken.NativeGatewayToken) error {
		borrowed = token
		if !bytes.Equal(token, tokenFixture) {
			t.Fatal("native gateway token differs from fixture")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(borrowed, make([]byte, len(tokenFixture))) {
		t.Fatal("borrowed native gateway token was not cleared")
	}
	if string(session.sessionID) != sessionID {
		t.Fatal("owned gateway session identifier was modified")
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ownedToken, make([]byte, len(ownedToken))) {
		t.Fatal("owned native gateway token was not cleared on close")
	}
	if err := session.WithNativeGatewayToken(func(sessiontoken.NativeGatewayToken) error { return nil }); err != ErrNoAuthenticatedSession {
		t.Fatalf("closed session token error = %v", err)
	}
}

func TestWithNativeGatewayTokenRequiresExtractedSSLContext(t *testing.T) {
	session := &Session{http: &http.Client{}, sessionID: []byte("0123456789abcdef")}
	err := session.WithNativeGatewayToken(func(sessiontoken.NativeGatewayToken) error { return nil })
	if err != ErrNoNativeGatewayToken {
		t.Fatalf("native gateway token error = %v", err)
	}
}

func TestProbeBootstrapRejectsAuthenticationEnvelopeWithoutPayload(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "<Auth><ErrorCode>0</ErrorCode></Auth>")
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	session := &Session{baseURL: baseURL, http: server.Client()}
	bootstrap, err := session.ProbeBootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.ConfigurationAvailable || bootstrap.ResourcesAvailable {
		t.Fatalf("bootstrap = %+v", bootstrap)
	}
}

func TestFailedBootstrapInvalidatesPreviouslyExtractedNativeToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fmt.Fprint(writer, "<Conf><Other sslctx=\"invalid\"/></Conf>")
	}))
	defer server.Close()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ownedToken := bytes.Repeat([]byte{0x5a}, 48)
	session := &Session{baseURL: baseURL, http: server.Client(), nativeGatewayToken: sessiontoken.NativeGatewayToken(ownedToken)}
	if _, err := session.ProbeBootstrap(context.Background()); err == nil {
		t.Fatal("invalid gateway SSL context was accepted")
	}
	if !bytes.Equal(ownedToken, make([]byte, len(ownedToken))) {
		t.Fatal("previous native token was not cleared before reprobe")
	}
	if err := session.WithNativeGatewayToken(func(sessiontoken.NativeGatewayToken) error { return nil }); err != ErrNoNativeGatewayToken {
		t.Fatalf("native token remained available after failed reprobe: %v", err)
	}
}

func TestInspectBootstrapBuildsMinimalServiceModel(t *testing.T) {
	sslContextBytes := make([]byte, 64)
	for index := range sslContextBytes {
		sslContextBytes[index] = byte(index)
	}
	sslContext := hex.EncodeToString(sslContextBytes)
	configuration := []byte(`<Conf><Other sddn_enable="1" sslctx="` + sslContext + `"/><SecurityCheck><strategies/></SecurityCheck></Conf>`)
	available, dedicatedLine, securityCheck, nativeToken, err := inspectConfiguration(configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(nativeToken)
	if !available || !dedicatedLine || !securityCheck {
		t.Fatalf("configuration = available:%t dedicated:%t security:%t", available, dedicatedLine, securityCheck)
	}
	wantToken := append(make([]byte, 32), sslContextBytes[32:48]...)
	if !bytes.Equal(nativeToken, wantToken) {
		t.Fatal("configuration native token differs from fixture")
	}

	resources := []byte(`<Resource><Dns dnsserver="10.0.0.1"/><Rcs>` +
		`<Rc type="0"><name>not retained</name></Rc>` +
		`<Rc type="1"><host>not retained</host></Rc>` +
		`<Rc type="2"/><Rc type="9"/><Rc/>` +
		`</Rcs></Resource>`)
	available, summary, internalDNS, err := inspectResources(resources)
	if err != nil {
		t.Fatal(err)
	}
	if !available || !internalDNS {
		t.Fatalf("resources = available:%t internalDNS:%t", available, internalDNS)
	}
	want := ResourceSummary{Web: 1, TCP: 1, L3VPN: 1, Unknown: 2}
	if summary != want {
		t.Fatalf("resource summary = %+v, want %+v", summary, want)
	}
}

func TestServiceRequirementsLocalAgentBoundary(t *testing.T) {
	if (ServiceRequirements{}).LocalAgentRequired() {
		t.Fatal("empty requirements unexpectedly need local agent")
	}
	for _, requirements := range []ServiceRequirements{
		{TCP: true},
		{L3VPN: true},
		{InternalDNS: true},
		{DedicatedLine: true},
		{SecurityCheck: true},
	} {
		if !requirements.LocalAgentRequired() {
			t.Fatalf("requirements %+v do not need local agent", requirements)
		}
	}
}
