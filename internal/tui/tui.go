// Package tui answers app.Interaction with terminal forms. The command
// layer picks it only when both standard input and standard error are
// terminals; forms render on standard error so standard output stays clean
// for results and JSON.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	"github.com/soundadam/soundconnect/internal/app"
)

const (
	maximumInputCharacters    = 4096
	maximumCodeCharacters     = 64
	maximumCallbackCharacters = 8192
)

// Interaction runs one huh form per question.
type Interaction struct {
	input  io.Reader
	output io.Writer
}

// New returns a form-based Interaction reading keys from input and drawing
// on output.
func New(input io.Reader, output io.Writer) *Interaction {
	return &Interaction{input: input, output: output}
}

func (tui *Interaction) run(ctx context.Context, fields ...huh.Field) error {
	form := huh.NewForm(huh.NewGroup(fields...)).
		WithInput(tui.input).
		WithOutput(tui.output).
		WithShowHelp(false).
		WithTheme(huh.ThemeBase())
	err := form.RunWithContext(ctx)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, huh.ErrUserAborted):
		return context.Canceled
	case ctx.Err() != nil:
		return ctx.Err()
	default:
		return err
	}
}

func (tui *Interaction) Select(ctx context.Context, title string, options []app.Option, defaultValue string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options to choose from")
	}
	choices := make([]huh.Option[string], 0, len(options))
	for _, option := range options {
		choices = append(choices, huh.NewOption(option.Label, option.Value))
	}
	value := defaultValue
	err := tui.run(ctx, huh.NewSelect[string]().Title(title).Options(choices...).Value(&value))
	return value, err
}

func (tui *Interaction) Input(ctx context.Context, title, defaultValue string, validate func(string) error) (string, error) {
	value := defaultValue
	field := huh.NewInput().Title(title).CharLimit(maximumInputCharacters).Value(&value).
		Validate(func(answer string) error {
			answer = strings.TrimSpace(answer)
			if answer == "" {
				return errors.New("a value is required")
			}
			if validate != nil {
				return validate(answer)
			}
			return nil
		})
	if err := tui.run(ctx, field); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

// Password reads a secret without echo. huh keeps field values in Go
// strings, which cannot be cleared; the returned copy can be.
func (tui *Interaction) Password(ctx context.Context, title string) ([]byte, error) {
	var value string
	field := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&value).
		Validate(func(answer string) error {
			if answer == "" {
				return errors.New("a password is required")
			}
			return nil
		})
	if err := tui.run(ctx, field); err != nil {
		return nil, err
	}
	return []byte(value), nil
}

func (tui *Interaction) Confirm(ctx context.Context, title string, defaultValue bool) (bool, error) {
	value := defaultValue
	err := tui.run(ctx, huh.NewConfirm().Title(title).Affirmative("Yes").Negative("No").Value(&value))
	return value, err
}

func (tui *Interaction) VerificationCode(ctx context.Context, destination string) ([]byte, error) {
	var value string
	field := huh.NewInput().Title("Verification code").CharLimit(maximumCodeCharacters).Value(&value).
		Validate(func(answer string) error {
			if strings.TrimSpace(answer) == "" {
				return errors.New("verification code is required")
			}
			return nil
		})
	if destination != "" {
		field.Description(fmt.Sprintf("A verification code was sent to %s.", destination))
	}
	if err := tui.run(ctx, field); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSpace(value)), nil
}

func (tui *Interaction) OAuthCallback(ctx context.Context, loginURL string) (string, error) {
	// The URL is printed outside the form so the terminal keeps it
	// selectable and clickable.
	fmt.Fprintf(tui.output, "Visit %s to sign in.\n", loginURL)
	var value string
	field := huh.NewInput().Title("Callback URL").
		Description("Paste the resulting /passport/v1/auth/httpsOauth2 callback URL; it stays local.").
		CharLimit(maximumCallbackCharacters).Value(&value).
		Validate(func(answer string) error {
			if strings.TrimSpace(answer) == "" {
				return errors.New("the callback URL is required")
			}
			return nil
		})
	if err := tui.run(ctx, field); err != nil {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Wait shows a spinner next to title until work returns.
func (tui *Interaction) Wait(ctx context.Context, title string, work func(context.Context) error) error {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for frame := 0; ; frame++ {
			fmt.Fprintf(tui.output, "\r%s %s", spinnerFrames[frame%len(spinnerFrames)], title)
			select {
			case <-done:
				// Erase the spinner line.
				fmt.Fprint(tui.output, "\r\x1b[2K")
				return
			case <-ticker.C:
			}
		}
	}()
	err := work(ctx)
	close(done)
	<-stopped
	return err
}

// ForCommand returns the Interaction for a command: forms when standard
// input and standard error are terminals and no secret is piped in, and line
// prompts otherwise. interactive reports whether a person is at the
// terminal, which enables guided flows. TERM=dumb or SOUNDCONNECT_ACCESSIBLE=1
// keeps a terminal user on line prompts, which screen readers follow better.
func ForCommand(options app.LineOptions, stderr io.Writer) (interaction app.Interaction, interactive bool) {
	options.Output = stderr
	if options.Input == nil {
		options.Input = os.Stdin
	}
	errorFile, ok := stderr.(*os.File)
	interactive = ok && !options.PasswordFromStdin && !options.CodeFromStdin &&
		term.IsTerminal(int(options.Input.Fd())) && term.IsTerminal(int(errorFile.Fd()))
	if !interactive || Accessible() {
		return app.NewLineInteraction(options), interactive
	}
	return New(options.Input, stderr), true
}

// Accessible reports whether the user asked for plain line prompts.
func Accessible() bool {
	return os.Getenv("TERM") == "dumb" || os.Getenv("SOUNDCONNECT_ACCESSIBLE") == "1"
}
