package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/soundadam/soundconnect/internal/app"
)

// keys feeds scripted key presses to a form. Each press is a separate write
// so bubbletea sees them as separate keys.
func keys(t *testing.T, presses ...string) io.Reader {
	t.Helper()
	reader, writer := io.Pipe()
	go func() {
		for _, press := range presses {
			time.Sleep(20 * time.Millisecond)
			if _, err := writer.Write([]byte(press)); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { writer.Close() })
	return reader
}

const (
	enter = "\r"
	down  = "\x1b[B"
	right = "\x1b[C"
	ctrlC = "\x03"
)

func run[T any](t *testing.T, ask func(*Interaction) (T, error), presses ...string) (T, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		tui := New(keys(t, presses...), io.Discard)
		value, err := ask(tui)
		done <- result{value, err}
	}()
	select {
	case got := <-done:
		return got.value, got.err
	case <-ctx.Done():
		t.Fatal("form did not finish")
		panic("unreachable")
	}
}

func TestSelect(t *testing.T) {
	options := []app.Option{{Value: "easyconnect", Label: "EasyConnect"}, {Value: "atrust", Label: "aTrust"}}
	value, err := run(t, func(tui *Interaction) (string, error) {
		return tui.Select(context.Background(), "Protocol backend", options, "easyconnect")
	}, down, enter)
	if err != nil || value != "atrust" {
		t.Fatalf("Select() = %q, %v", value, err)
	}
	// The default is preselected.
	value, err = run(t, func(tui *Interaction) (string, error) {
		return tui.Select(context.Background(), "Protocol backend", options, "atrust")
	}, enter)
	if err != nil || value != "atrust" {
		t.Fatalf("Select(default) = %q, %v", value, err)
	}
}

func TestInputKeepsTheDefaultAndTrims(t *testing.T) {
	value, err := run(t, func(tui *Interaction) (string, error) {
		return tui.Input(context.Background(), "Gateway", "vpn.nju.edu.cn", nil)
	}, enter)
	if err != nil || value != "vpn.nju.edu.cn" {
		t.Fatalf("Input(default) = %q, %v", value, err)
	}
	value, err = run(t, func(tui *Interaction) (string, error) {
		return tui.Input(context.Background(), "Account", "", nil)
	}, "s", "t", "u", " ", enter)
	if err != nil || value != "stu" {
		t.Fatalf("Input() = %q, %v", value, err)
	}
}

func TestPasswordAndConfirm(t *testing.T) {
	secret, err := run(t, func(tui *Interaction) ([]byte, error) {
		return tui.Password(context.Background(), "VPN password")
	}, "p", "w", enter)
	if err != nil || string(secret) != "pw" {
		t.Fatalf("Password() = %q, %v", secret, err)
	}
	confirmed, err := run(t, func(tui *Interaction) (bool, error) {
		return tui.Confirm(context.Background(), "Test the login now?", true)
	}, right, enter)
	if err != nil || confirmed {
		t.Fatalf("Confirm() = %t, %v", confirmed, err)
	}
}

func TestCtrlCCancels(t *testing.T) {
	_, err := run(t, func(tui *Interaction) (string, error) {
		return tui.Input(context.Background(), "Gateway", "", nil)
	}, ctrlC)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Input() after Ctrl-C = %v", err)
	}
}

func TestWaitRunsWorkAndClearsTheSpinner(t *testing.T) {
	var output strings.Builder
	want := errors.New("discovery failed")
	err := New(strings.NewReader(""), &output).Wait(context.Background(), "Discovering", func(context.Context) error {
		time.Sleep(150 * time.Millisecond)
		return want
	})
	if !errors.Is(err, want) || !strings.Contains(output.String(), "Discovering") || !strings.HasSuffix(output.String(), "\r\x1b[2K") {
		t.Fatalf("Wait() = %v, output %q", err, output.String())
	}
}

func TestForCommandUsesLinePromptsWithoutATerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	interaction, interactive := ForCommand(app.LineOptions{Input: file}, io.Discard)
	if _, ok := interaction.(*app.LineInteraction); !ok || interactive {
		t.Fatalf("ForCommand() = %T, %t", interaction, interactive)
	}
}
