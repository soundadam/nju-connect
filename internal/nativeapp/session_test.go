package nativeapp

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/core"
	"github.com/soundadam/soundconnect/internal/runtime"
	"github.com/soundadam/soundconnect/internal/sessiontoken"
	"github.com/soundadam/soundconnect/internal/traffic"
)

func TestGatewayTargetPreservesServerNameWhenAddressIsResolved(t *testing.T) {
	tests := []struct {
		name      string
		server    string
		resolveIP string
		want      gatewayTarget
	}{
		{
			name:   "gateway DNS",
			server: "vpn.example.edu",
			want:   gatewayTarget{serverName: "vpn.example.edu", address: "vpn.example.edu:443"},
		},
		{
			name:      "resolved IPv4 with custom port",
			server:    "vpn.example.edu:8443",
			resolveIP: "192.0.2.10",
			want:      gatewayTarget{serverName: "vpn.example.edu", address: "192.0.2.10:8443"},
		},
		{
			name:      "resolved IPv6",
			server:    "vpn.example.edu",
			resolveIP: "2001:db8::10",
			want:      gatewayTarget{serverName: "vpn.example.edu", address: "[2001:db8::10]:443"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := gatewayTargetFor(test.server, test.resolveIP)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("target = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestGatewayTargetRejectsNonNumericResolutionWithoutEchoingIt(t *testing.T) {
	const sensitive = "not-an-ip-secret"
	_, err := gatewayTargetFor("vpn.example.edu", sensitive)
	if err == nil {
		t.Fatal("expected invalid resolution to fail")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("error leaked rejected input: %q", err)
	}
}

func TestCommandDialerUsesOnlyResolvedEndpoint(t *testing.T) {
	server, peer := net.Pipe()
	defer peer.Close()
	var gotNetwork string
	var gotAddress string
	dialer := commandDialer(func(_ context.Context, network string, address string) (net.Conn, error) {
		gotNetwork = network
		gotAddress = address
		return server, nil
	}, "192.0.2.10:8443")
	connection, err := dialer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if gotNetwork != "tcp" || gotAddress != "192.0.2.10:8443" {
		t.Fatalf("dial = %s %s", gotNetwork, gotAddress)
	}
}

func TestNewProtocolProfileSelectsOnlyExplicitID(t *testing.T) {
	rawDial := func(context.Context) (net.Conn, error) { return nil, nil }
	for _, profileID := range []runtime.ProtocolProfileID{
		runtime.ProfileCommunityUTLSCompat,
		runtime.ProfileEasyConnect767FixedPreface,
	} {
		profile, err := newProtocolProfile(profileID, rawDial, "vpn.example.edu", false)
		if err != nil {
			t.Fatal(err)
		}
		if profile.ID() != profileID {
			t.Fatalf("selected profile = %q, want %q", profile.ID(), profileID)
		}
	}
	if _, err := newProtocolProfile("unknown", rawDial, "vpn.example.edu", false); err == nil {
		t.Fatal("unknown profile was accepted")
	}
}

func TestNewSessionConstructsWithoutNetworkAndBorrowsToken(t *testing.T) {
	token := []byte("0123456789abcdef0123456789abcdef0123456789abcdef")
	if len(token) != sessiontoken.NativeGatewayTokenSize {
		t.Fatalf("fixture token length = %d", len(token))
	}
	session, err := NewSession(SessionConfig{
		Settings: config.Config{
			Server:      "vpn.example.edu",
			Username:    "student",
			SOCKSListen: "127.0.0.1:0",
		},
		Plan: core.DataplanePlan{
			Mode:               core.DataplaneL3VPN,
			LocalAgentRequired: true,
			BoundaryReady:      true,
		},
		NativeGatewayToken: sessiontoken.NativeGatewayToken(token),
		NativeProfile:      runtime.ProfileCommunityUTLSCompat,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := session.Profile()
	if profile.ID != runtime.ProfileCommunityUTLSCompat || profile.Evidence != runtime.EvidenceReverseTestLiveCompat || !profile.Security.Encrypted || !profile.Security.PeerVerified {
		t.Fatalf("session profile = %+v", profile)
	}
	clear(token)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNewSessionRejectsInvalidTokenWithoutLeakingIt(t *testing.T) {
	const sensitive = "native-token-secret"
	_, err := NewSession(SessionConfig{
		Settings: config.Config{
			Server:      "vpn.example.edu",
			Username:    "student",
			SOCKSListen: "127.0.0.1:1081",
		},
		Plan:               core.DataplanePlan{},
		NativeGatewayToken: sessiontoken.NativeGatewayToken(sensitive),
		NativeProfile:      runtime.ProfileCommunityUTLSCompat,
	})
	if err == nil {
		t.Fatal("expected invalid token to fail")
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatalf("error leaked token: %q", err)
	}
}

func TestNewSessionAcceptsExplicitScopedTLSInsecure(t *testing.T) {
	token := make(sessiontoken.NativeGatewayToken, sessiontoken.NativeGatewayTokenSize)
	session, err := NewSession(SessionConfig{
		Settings: config.Config{
			Server:            "vpn.example.edu",
			Username:          "student",
			SOCKSListen:       "127.0.0.1:1081",
			NativeTLSInsecure: true,
		},
		Plan: core.DataplanePlan{
			Mode:               core.DataplaneL3VPN,
			LocalAgentRequired: true,
			BoundaryReady:      true,
		},
		NativeGatewayToken: token,
		NativeProfile:      runtime.ProfileCommunityUTLSCompat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRunPublishesOnlySanitizedTraffic(t *testing.T) {
	want := TrafficSnapshot{
		SessionStartedAt:  time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC),
		UploadBytes:       10,
		DownloadBytes:     20,
		ActiveConnections: 1,
		TotalConnections:  2,
	}
	var got TrafficSnapshot
	observer := newSerializedObserver(ObserverFuncs{OnTraffic: func(snapshot TrafficSnapshot) {
		got = snapshot
	}})
	fake := &fakeNativeSession{snapshot: traffic.Snapshot{
		SessionStartedAt:  want.SessionStartedAt,
		UploadBytes:       want.UploadBytes,
		DownloadBytes:     want.DownloadBytes,
		ActiveConnections: want.ActiveConnections,
		TotalConnections:  want.TotalConnections,
	}}
	session := &Session{native: fake, observer: observer}
	if err := session.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("traffic = %+v, want %+v", got, want)
	}
}

type fakeNativeSession struct {
	snapshot traffic.Snapshot
}

func (*fakeNativeSession) Run(context.Context) error { return nil }
func (*fakeNativeSession) Close() error              { return nil }
func (session *fakeNativeSession) Traffic() traffic.Snapshot {
	return session.snapshot
}
