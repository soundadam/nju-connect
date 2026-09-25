package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/soundadam/soundconnect/internal/credential"
)

// Interaction is every question a service can ask the user. The methods are
// shaped for a form-based terminal UI; LineInteraction answers them with
// plain line prompts, which is also the non-terminal contract the macOS app
// drives through --password-stdin and --verification-code-stdin.
//
// A user who aborts a question gets context.Canceled, which the command
// layer maps to exit status 0.
type Interaction interface {
	// Select asks for one of options and returns its Value.
	Select(ctx context.Context, title string, options []Option, defaultValue string) (string, error)
	// Input asks for one line of text. An empty answer selects defaultValue;
	// with no default an answer is required. validate may be nil.
	Input(ctx context.Context, title, defaultValue string, validate func(string) error) (string, error)
	// Password asks for a secret without echo. The caller clears the result.
	Password(ctx context.Context, title string) ([]byte, error)
	// Confirm asks a yes/no question.
	Confirm(ctx context.Context, title string, defaultValue bool) (bool, error)
	// VerificationCode asks for a one-time code the gateway sent to
	// destination, which may be empty. The caller clears the result.
	VerificationCode(ctx context.Context, destination string) ([]byte, error)
	// OAuthCallback shows loginURL and asks for the callback URL the browser
	// lands on after signing in.
	OAuthCallback(ctx context.Context, loginURL string) (string, error)
	// Wait runs work, which may take a while, showing title as progress.
	Wait(ctx context.Context, title string, work func(context.Context) error) error
}

// Option is one choice offered by Select.
type Option struct {
	Value string
	Label string
}

const (
	maximumInputBytes    = 4096
	maximumSecretBytes   = 1 << 20
	maximumCodeBytes     = 64
	maximumCallbackBytes = 8192
)

// LineOptions configures a LineInteraction.
type LineOptions struct {
	// Input is read one byte at a time, so answers to consecutive questions
	// are never swallowed by a read-ahead buffer. Nil means os.Stdin.
	Input *os.File
	// Output receives the prompts. It should be stderr.
	Output io.Writer
	// PasswordFromStdin makes Password read one raw line from Input without
	// a prompt or a terminal (setup --password-stdin).
	PasswordFromStdin bool
	// CodeFromStdin lets VerificationCode read from a non-terminal Input
	// (connect --verification-code-stdin).
	CodeFromStdin bool
}

// LineInteraction answers Interaction with plain line prompts.
type LineInteraction struct {
	options LineOptions
}

// NewLineInteraction returns a line-prompt Interaction.
func NewLineInteraction(options LineOptions) *LineInteraction {
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	return &LineInteraction{options: options}
}

func (line *LineInteraction) Select(ctx context.Context, title string, options []Option, defaultValue string) (string, error) {
	if len(options) == 0 {
		return "", errors.New("no options to choose from")
	}
	for index, option := range options {
		fmt.Fprintf(line.options.Output, "  %d) %s\n", index+1, option.Label)
	}
	answer, err := line.Input(ctx, title, defaultValue, nil)
	if err != nil {
		return "", err
	}
	for index, option := range options {
		if answer == option.Value || answer == strconv.Itoa(index+1) {
			return option.Value, nil
		}
	}
	return "", fmt.Errorf("%q is not one of the choices", answer)
}

func (line *LineInteraction) Input(_ context.Context, title, defaultValue string, validate func(string) error) (string, error) {
	prompt := title + ": "
	if defaultValue != "" {
		prompt = fmt.Sprintf("%s [%s]: ", title, defaultValue)
	}
	if _, err := io.WriteString(line.options.Output, prompt); err != nil {
		return "", err
	}
	raw, err := readLine(line.options.Input, maximumInputBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		if defaultValue == "" {
			return "", errors.New("value is required")
		}
		value = defaultValue
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return "", err
		}
	}
	return value, nil
}

func (line *LineInteraction) Password(_ context.Context, title string) ([]byte, error) {
	if line.options.PasswordFromStdin {
		return readSecretLine(line.options.Input)
	}
	return credential.NewPromptStore(credential.PromptOptions{
		Input: line.options.Input, Output: line.options.Output, Prompt: title + ": ",
	}).Get()
}

func (line *LineInteraction) Confirm(_ context.Context, title string, defaultValue bool) (bool, error) {
	hint := "y/N"
	if defaultValue {
		hint = "Y/n"
	}
	if _, err := fmt.Fprintf(line.options.Output, "%s [%s]: ", title, hint); err != nil {
		return false, err
	}
	raw, err := readLine(line.options.Input, maximumInputBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	switch answer := strings.ToLower(strings.TrimSpace(string(raw))); answer {
	case "":
		return defaultValue, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("answer %q is not yes or no", answer)
	}
}

func (line *LineInteraction) VerificationCode(ctx context.Context, destination string) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("verification code context is required")
	}
	input, output := line.options.Input, line.options.Output
	if destination != "" {
		fmt.Fprintf(output, "A verification code was sent to %s.\n", destination)
	}
	isTerminal := term.IsTerminal(int(input.Fd()))
	if !isTerminal && !line.options.CodeFromStdin {
		return nil, credential.ErrNoTerminal
	}
	if _, err := io.WriteString(output, "Verification code: "); err != nil {
		return nil, err
	}
	if isTerminal {
		original, err := term.MakeRaw(int(input.Fd()))
		if err != nil {
			return nil, err
		}
		defer term.Restore(int(input.Fd()), original) //nolint:errcheck // best-effort terminal restoration on every exit path
	}
	defer fmt.Fprintln(output)

	type readResult struct {
		code []byte
		err  error
	}
	result := make(chan readResult, 1)
	go func() {
		code, readErr := readRawVerificationCode(input)
		select {
		case result <- readResult{code: code, err: readErr}:
		case <-ctx.Done():
			credential.Clear(code)
		}
	}()

	var read readResult
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case read = <-result:
	}
	if read.err != nil {
		credential.Clear(read.code)
		return nil, read.err
	}
	defer clear(read.code)
	trimmed := bytes.TrimSpace(read.code)
	if len(trimmed) == 0 {
		return nil, errors.New("verification code is required")
	}
	return append([]byte(nil), trimmed...), nil
}

func (line *LineInteraction) OAuthCallback(ctx context.Context, loginURL string) (string, error) {
	output := line.options.Output
	fmt.Fprintf(output, "Visit %s to sign in.\n", loginURL)
	fmt.Fprintln(output, "Paste the resulting /passport/v1/auth/httpsOauth2 callback URL here; it stays local.")
	fmt.Fprint(output, "Callback URL: ")

	type readResult struct {
		line string
		err  error
	}
	completed := make(chan readResult, 1)
	go func() {
		callback, readErr := readCallbackLine(line.options.Input)
		completed <- readResult{line: callback, err: readErr}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-completed:
		return result.line, result.err
	}
}

// Wait runs work without output: line prompts keep stderr to questions.
func (line *LineInteraction) Wait(ctx context.Context, _ string, work func(context.Context) error) error {
	return work(ctx)
}

// readLine reads up to and excluding '\n' without reading ahead. A trailing
// '\r' is dropped. It returns the bytes read so far together with io.EOF when
// input ends first.
func readLine(input io.Reader, maximum int) ([]byte, error) {
	if input == nil {
		return nil, errors.New("input is unavailable")
	}
	line := make([]byte, 0, 64)
	var one [1]byte
	for {
		count, err := input.Read(one[:])
		if count > 0 {
			if one[0] == '\n' {
				return bytes.TrimSuffix(line, []byte{'\r'}), nil
			}
			if len(line) >= maximum {
				clear(line)
				return nil, errLineTooLong
			}
			line = append(line, one[0])
		}
		if err != nil {
			return bytes.TrimSuffix(line, []byte{'\r'}), err
		}
	}
}

var errLineTooLong = errors.New("line is too long")

func readSecretLine(input io.Reader) ([]byte, error) {
	secret, err := readLine(input, maximumSecretBytes)
	switch {
	case errors.Is(err, errLineTooLong):
		return nil, credential.ErrCredentialTooLarge
	case err != nil && !errors.Is(err, io.EOF):
		credential.Clear(secret)
		return nil, errors.New("read password from standard input")
	case len(secret) == 0:
		return nil, credential.ErrEmptyCredential
	}
	return secret, nil
}

func readCallbackLine(input io.Reader) (string, error) {
	line, err := readLine(input, maximumCallbackBytes)
	defer clear(line)
	if errors.Is(err, errLineTooLong) {
		return "", errors.New("callback URL is too long")
	}
	if err != nil {
		return "", err
	}
	return string(line), nil
}

// readRawVerificationCode reads a code from a terminal in raw mode, or from a
// pipe, handling Enter, Backspace, Ctrl-C and Ctrl-D itself.
func readRawVerificationCode(input io.Reader) ([]byte, error) {
	code := make([]byte, 0, 8)
	defer func() {
		if code != nil {
			clear(code)
		}
	}()
	var one [1]byte
	for {
		count, err := input.Read(one[:])
		if count > 0 {
			switch one[0] {
			case 0x03:
				return nil, context.Canceled
			case '\r', '\n':
				return append([]byte(nil), code...), nil
			case 0x04:
				return nil, io.EOF
			case 0x08, 0x7f:
				if len(code) > 0 {
					code = code[:len(code)-1]
				}
			default:
				if len(code) >= maximumCodeBytes {
					return nil, errors.New("verification code is too long")
				}
				code = append(code, one[0])
			}
		}
		if err != nil {
			return nil, err
		}
	}
}
