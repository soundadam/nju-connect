package gatewayauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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

func TestInspectBootstrapBuildsMinimalServiceModel(t *testing.T) {
	configuration := []byte(`<Conf><Other sddn_enable="1"/><SecurityCheck><strategies/></SecurityCheck></Conf>`)
	available, dedicatedLine, securityCheck, err := inspectConfiguration(configuration)
	if err != nil {
		t.Fatal(err)
	}
	if !available || !dedicatedLine || !securityCheck {
		t.Fatalf("configuration = available:%t dedicated:%t security:%t", available, dedicatedLine, securityCheck)
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
