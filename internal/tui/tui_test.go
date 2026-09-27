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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/ansi"

	"github.com/soundadam/nju-connect/internal/app"
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

func TestFitWindowKeepsFormsOffTheRightEdge(t *testing.T) {
	for _, test := range []struct{ width, height, wantWidth, wantHeight int }{
		{120, 30, 80, 30},
		{80, 24, 78, 24},
		{40, 10, 38, 10},
		{2, 5, 1, 5},
		{0, 0, 80, 24},
	} {
		got := fitWindow(nil, tea.WindowSizeMsg{Width: test.width, Height: test.height})
		want := tea.WindowSizeMsg{Width: test.wantWidth, Height: test.wantHeight}
		if got != want {
			t.Errorf("fitWindow(%dx%d) = %+v, want %+v", test.width, test.height, got, want)
		}
	}
	key := tea.KeyMsg{Type: tea.KeyEnter}
	if got, ok := fitWindow(nil, key).(tea.KeyMsg); !ok || got.Type != key.Type {
		t.Error("fitWindow changed a key press")
	}
}

// Every line huh draws must end clear of the last column, or terminals that
// wrap there, or draw the border as two cells, stack redraws instead of
// replacing them.
func TestFormLinesStayNarrowerThanTheTerminal(t *testing.T) {
	for _, width := range []int{0, 30, 79, 80, 81, 200} {
		tui := New(strings.NewReader(""), io.Discard)
		form := tui.form(huh.NewInput().Title("Verification code").
			Description("A verification code was sent to 138****0000, which is a long enough sentence to wrap."))
		form.Init()
		form.Update(fitWindow(form, tea.WindowSizeMsg{Width: width, Height: 24}))
		view := form.View()
		if strings.TrimSpace(view) == "" {
			t.Fatalf("width %d: empty form", width)
		}
		limit := width - formRightMargin
		if width == 0 {
			limit = maximumFormWidth
		}
		for _, line := range strings.Split(view, "\n") {
			if got := ansi.StringWidth(line); got > limit {
				t.Errorf("width %d: line is %d cells: %q", width, got, line)
			}
		}
	}
}
