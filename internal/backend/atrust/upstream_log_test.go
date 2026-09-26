package atrustbackend

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/soundadam/soundconnect/internal/backend"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

// terminalLogger stands in for the terminal the standard logger writes to
// by default, and a debug sink the host enabled. Both are restored after the
// test.
func terminalLogger(t *testing.T) (terminal, debug *lockedBuffer) {
	t.Helper()
	terminal, debug = &lockedBuffer{}, &lockedBuffer{}
	previous := log.Writer()
	log.SetOutput(terminal)
	SetUpstreamDebugLog(debug)
	t.Cleanup(func() {
		log.SetOutput(previous)
		SetUpstreamDebugLog(nil)
	})
	return terminal, debug
}

func TestUpstreamLogStaysOffTheTerminalWhenThePasswordIsRejected(t *testing.T) {
	terminal, debug := terminalLogger(t)
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		log.Println("Perform GET /passport/v1/public/authConfig")
		log.Println("Perform POST /passport/v1/auth/psw")
		log.Printf("Code: %d, Message: %s", 10302, "用户名或密码错误")
		return nil, nil, errors.New("unsupported next authentication service: auth/unknown")
	}}
	prompter := PrompterFuncs{OnPassword: func(context.Context, PasswordRequest) ([]byte, error) { return []byte("wrong"), nil }}

	_, err := core.Authenticate(context.Background(), passwordLogin(), prompter)
	if !errors.Is(err, backend.ErrCredentialRejected) {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if terminal.String() != "" {
		t.Fatalf("upstream output reached the terminal: %q", terminal)
	}
	for _, line := range []string{"Perform GET /passport/v1/public/authConfig\n", "Code: 10302, Message: 用户名或密码错误\n"} {
		if !strings.Contains(debug.String(), line) {
			t.Fatalf("debug log = %q, want %q", debug, line)
		}
	}
}

func TestUpstreamLogStaysOffTheTerminalWhileAFactorIsPrompted(t *testing.T) {
	terminal, debug := terminalLogger(t)
	upstream := newFakeUpstream()
	core := zjuCore{setup: func(setupRequest) (upstreamClient, []byte, error) {
		log.Printf("SMS message sent successfully: %s", "138****0000")
		if code, err := scanPrompt(upstreamSMSPrompt); err != nil || code != "123456" {
			return nil, nil, errors.New("verification code was not bridged")
		}
		return upstream, []byte("{}"), nil
	}}
	prompter := PrompterFuncs{
		OnPassword:         func(context.Context, PasswordRequest) ([]byte, error) { return []byte("pw"), nil },
		OnVerificationCode: func(context.Context, VerificationRequest) ([]byte, error) { return []byte("123456"), nil },
	}

	session, err := core.Authenticate(context.Background(), passwordLogin(), prompter)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	// The upstream client keeps logging while the session runs, for example
	// when it re-probes tunnel nodes.
	log.Printf("Best node in group %s: %s with latency %d ms", "default", "node", 12)

	if terminal.String() != "" {
		t.Fatalf("upstream output reached the terminal: %q", terminal)
	}
	for _, line := range []string{"SMS message sent successfully", upstreamSMSPrompt, "Best node in group"} {
		if !strings.Contains(debug.String(), line) {
			t.Fatalf("debug log = %q, want %q", debug, line)
		}
	}
}

func TestUpstreamLogStaysOffTheTerminalDuringDiscovery(t *testing.T) {
	terminal, debug := terminalLogger(t)
	core := zjuCore{discover: func(backend.Endpoint) ([]backend.AuthenticationMethod, error) {
		log.Println("Perform GET /passport/v1/public/authConfig")
		return []backend.AuthenticationMethod{{Domain: "openldap13924", Type: passwordAuthType}}, nil
	}}

	methods, err := core.Discover(context.Background(), backend.ATrustEndpoint("vpn.nju.edu.cn", 443))
	if err != nil || len(methods) != 1 {
		t.Fatalf("Discover() = %+v, %v", methods, err)
	}
	if terminal.String() != "" {
		t.Fatalf("upstream output reached the terminal: %q", terminal)
	}
	if !strings.Contains(debug.String(), "/passport/v1/public/authConfig") {
		t.Fatalf("debug log = %q", debug)
	}
}

func TestNewCoreCapturesTheStandardLogger(t *testing.T) {
	terminal, _ := terminalLogger(t)
	NewCore()
	log.Println("Underlay interface: en0")
	if terminal.String() != "" {
		t.Fatalf("upstream output reached the terminal: %q", terminal)
	}
}

func TestUpstreamLogDiscardsByDefault(t *testing.T) {
	terminal, _ := terminalLogger(t)
	SetUpstreamDebugLog(nil)
	NewCore()
	log.Println("Perform GET /passport/v1/public/authConfig")
	if terminal.String() != "" {
		t.Fatalf("upstream output reached the terminal: %q", terminal)
	}
}

func TestUpstreamLogJoinsPartialWrites(t *testing.T) {
	debug := &lockedBuffer{}
	writer := &upstreamLogWriter{debug: debug}
	var lines []string
	stop := writer.observe(func(line string) { lines = append(lines, line) })
	for _, chunk := range []string{"Perform POST /pass", "port/v1/auth/psw\nCode: 1", "0302, Message: x\n", "tail"} {
		if written, err := writer.Write([]byte(chunk)); err != nil || written != len(chunk) {
			t.Fatalf("Write(%q) = %d, %v", chunk, written, err)
		}
	}
	stop()
	_, _ = writer.Write([]byte("after\n"))

	if want := []string{"Perform POST /passport/v1/auth/psw", "Code: 10302, Message: x"}; strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Fatalf("observed lines = %q, want %q", lines, want)
	}
	if want := "Perform POST /passport/v1/auth/psw\nCode: 10302, Message: x\ntailafter\n"; debug.String() != want {
		t.Fatalf("debug log = %q, want %q", debug, want)
	}
}

func TestUpstreamLogIgnoresAFailingDebugSink(t *testing.T) {
	writer := &upstreamLogWriter{debug: failingWriter{}}
	if written, err := writer.Write([]byte("line\n")); err != nil || written != 5 {
		t.Fatalf("Write() = %d, %v", written, err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
