package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/soundadam/nju-connect/internal/credential"
)

// fileInput returns a non-terminal *os.File holding content.
func fileInput(t *testing.T, content string) *os.File {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func lineInteraction(t *testing.T, content string, options LineOptions) (*LineInteraction, *strings.Builder) {
	t.Helper()
	var output strings.Builder
	options.Input = fileInput(t, content)
	options.Output = &output
	return NewLineInteraction(options), &output
}

func TestInputTrimsAndRequiresAValue(t *testing.T) {
	line, output := lineInteraction(t, "  vpn.example.edu  \n\n", LineOptions{})
	value, err := line.Input(context.Background(), "Gateway", "", nil)
	if err != nil || value != "vpn.example.edu" || output.String() != "Gateway: " {
		t.Fatalf("value=%q err=%v output=%q", value, err, output.String())
	}
	if _, err := line.Input(context.Background(), "Account", "", nil); err == nil || err.Error() != "value is required" {
		t.Fatalf("empty input error = %v", err)
	}
}

func TestInputUsesDefaultForEmptyAnswerAndAtEOF(t *testing.T) {
	line, output := lineInteraction(t, "\n", LineOptions{})
	for range 2 {
		value, err := line.Input(context.Background(), "Gateway", "vpn.nju.edu.cn", nil)
		if err != nil || value != "vpn.nju.edu.cn" {
			t.Fatalf("value=%q err=%v", value, err)
		}
	}
	if output.String() != "Gateway [vpn.nju.edu.cn]: Gateway [vpn.nju.edu.cn]: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestInputRunsValidation(t *testing.T) {
	line, _ := lineInteraction(t, "bad\n", LineOptions{})
	rejected := errors.New("rejected")
	if _, err := line.Input(context.Background(), "Gateway", "", func(string) error { return rejected }); !errors.Is(err, rejected) {
		t.Fatalf("error = %v", err)
	}
}

// Consecutive questions share one input stream, the way the macOS app pipes
// several answers; no answer may be swallowed by a read-ahead buffer.
func TestConsecutiveAnswersAreNotSwallowed(t *testing.T) {
	line, _ := lineInteraction(t, "vpn.example.edu\nstudent\nsecret\r\n", LineOptions{PasswordFromStdin: true})
	gateway, _ := line.Input(context.Background(), "Gateway", "", nil)
	account, _ := line.Input(context.Background(), "Account", "", nil)
	password, err := line.Password(context.Background(), "VPN password")
	if err != nil || gateway != "vpn.example.edu" || account != "student" || string(password) != "secret" {
		t.Fatalf("gateway=%q account=%q password=%q err=%v", gateway, account, password, err)
	}
}

func TestPasswordFromStdin(t *testing.T) {
	line, output := lineInteraction(t, "local-secret\n", LineOptions{PasswordFromStdin: true})
	secret, err := line.Password(context.Background(), "VPN password")
	if err != nil || string(secret) != "local-secret" || output.Len() != 0 {
		t.Fatalf("secret=%q err=%v output=%q", secret, err, output.String())
	}
	credential.Clear(secret)

	line, _ = lineInteraction(t, "\n", LineOptions{PasswordFromStdin: true})
	if _, err := line.Password(context.Background(), "VPN password"); !errors.Is(err, credential.ErrEmptyCredential) {
		t.Fatalf("empty secret error = %v", err)
	}
	line, _ = lineInteraction(t, strings.Repeat("a", maximumSecretBytes+1), LineOptions{PasswordFromStdin: true})
	if _, err := line.Password(context.Background(), "VPN password"); !errors.Is(err, credential.ErrCredentialTooLarge) {
		t.Fatalf("oversized secret error = %v", err)
	}
}

func TestPasswordPromptRequiresTerminal(t *testing.T) {
	line, output := lineInteraction(t, "local-secret\n", LineOptions{})
	if _, err := line.Password(context.Background(), "VPN password"); !errors.Is(err, credential.ErrNoTerminal) {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("output = %q", output.String())
	}
}

func TestSelectAcceptsValueIndexOrDefault(t *testing.T) {
	options := []Option{{Value: "easyconnect", Label: "EasyConnect"}, {Value: "atrust", Label: "aTrust"}}
	line, output := lineInteraction(t, "atrust\n2\n\nopenvpn\n", LineOptions{})
	for _, want := range []string{"atrust", "atrust", "easyconnect"} {
		got, err := line.Select(context.Background(), "Backend", options, "easyconnect")
		if err != nil || got != want {
			t.Fatalf("Select() = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := line.Select(context.Background(), "Backend", options, "easyconnect"); err == nil {
		t.Fatal("unknown choice accepted")
	}
	if !strings.HasPrefix(output.String(), "  1) EasyConnect\n  2) aTrust\nBackend [easyconnect]: ") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestConfirm(t *testing.T) {
	line, output := lineInteraction(t, "y\nNo\n\nmaybe\n", LineOptions{})
	for _, want := range []bool{true, false, true} {
		got, err := line.Confirm(context.Background(), "Test login now?", true)
		if err != nil || got != want {
			t.Fatalf("Confirm() = %t, %v; want %t", got, err, want)
		}
	}
	if _, err := line.Confirm(context.Background(), "Test login now?", true); err == nil {
		t.Fatal("unclear answer accepted")
	}
	if !strings.HasPrefix(output.String(), "Test login now? [Y/n]: ") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestVerificationCodeFromStdin(t *testing.T) {
	line, output := lineInteraction(t, "123456\n", LineOptions{CodeFromStdin: true})
	code, err := line.VerificationCode(context.Background(), "")
	if err != nil || string(code) != "123456" || output.String() != "Verification code: \n" {
		t.Fatalf("code=%q err=%v output=%q", code, err, output.String())
	}
}

func TestVerificationCodeRequiresTerminalUnlessAllowed(t *testing.T) {
	line, output := lineInteraction(t, "123456\n", LineOptions{})
	code, err := line.VerificationCode(context.Background(), "1xx****0000")
	if !errors.Is(err, credential.ErrNoTerminal) || code != nil {
		t.Fatalf("code=%v error=%v", code, err)
	}
	// The destination is still shown, so the user knows where the code went.
	if output.String() != "A verification code was sent to 1xx****0000.\n" {
		t.Fatalf("output = %q", output.String())
	}
}

func TestVerificationCodeHonoursCancellation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	line := NewLineInteraction(LineOptions{Input: reader, Output: io.Discard, CodeFromStdin: true})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := line.VerificationCode(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestReadRawVerificationCode(t *testing.T) {
	code, err := readRawVerificationCode(bytes.NewReader([]byte{'1', '2', 0x7f, '3', '\r'}))
	if err != nil || string(code) != "13" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	if code, err := readRawVerificationCode(bytes.NewReader([]byte{'1', '2', 0x03})); !errors.Is(err, context.Canceled) || code != nil {
		t.Fatalf("Ctrl-C code=%v error=%v", code, err)
	}
	if code, err := readRawVerificationCode(bytes.NewReader([]byte{0x04})); !errors.Is(err, io.EOF) || code != nil {
		t.Fatalf("Ctrl-D code=%v error=%v", code, err)
	}
	input := append(bytes.Repeat([]byte{'1'}, maximumCodeBytes+1), '\r')
	if code, err := readRawVerificationCode(bytes.NewReader(input)); err == nil || code != nil {
		t.Fatalf("long code=%v error=%v", code, err)
	}
}

func TestOAuthCallback(t *testing.T) {
	line, output := lineInteraction(t, "https://vpn.example.edu/callback?code=1\r\n", LineOptions{})
	callback, err := line.OAuthCallback(context.Background(), "https://vpn.example.edu/login")
	if err != nil || callback != "https://vpn.example.edu/callback?code=1" {
		t.Fatalf("callback=%q err=%v", callback, err)
	}
	if !strings.HasPrefix(output.String(), "Visit https://vpn.example.edu/login to sign in.\n") ||
		!strings.HasSuffix(output.String(), "Callback URL: ") {
		t.Fatalf("output = %q", output.String())
	}

	line, _ = lineInteraction(t, strings.Repeat("a", maximumCallbackBytes+1)+"\n", LineOptions{})
	if _, err := line.OAuthCallback(context.Background(), "https://vpn.example.edu/login"); err == nil ||
		err.Error() != "callback URL is too long" {
		t.Fatalf("oversized callback error = %v", err)
	}
	line, _ = lineInteraction(t, "https://vpn.example.edu/partial", LineOptions{})
	if _, err := line.OAuthCallback(context.Background(), "https://vpn.example.edu/login"); !errors.Is(err, io.EOF) {
		t.Fatalf("unterminated callback error = %v", err)
	}
}
