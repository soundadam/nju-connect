package credential

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

const defaultPrompt = "soundconnect credential: "

// PromptOptions supplies the terminal used for hidden input. Nil values use
// standard input and standard error.
type PromptOptions struct {
	Input  *os.File
	Output io.Writer
	Prompt string
}

// PromptStore obtains a credential through terminal input with echo disabled.
// It never persists a value.
type PromptStore struct {
	input        *os.File
	output       io.Writer
	isTerminal   func(fd int) bool
	readTerminal func(fd int) ([]byte, error)
	prompt       string
}

// NewPromptStore creates a hidden terminal prompt backend.
func NewPromptStore(options PromptOptions) *PromptStore {
	input := options.Input
	if input == nil {
		input = os.Stdin
	}
	output := options.Output
	if output == nil {
		output = os.Stderr
	}
	prompt := options.Prompt
	if prompt == "" {
		prompt = defaultPrompt
	}
	return &PromptStore{
		input:        input,
		output:       output,
		isTerminal:   term.IsTerminal,
		readTerminal: term.ReadPassword,
		prompt:       prompt,
	}
}

// Get reads one credential with terminal echo disabled.
func (p *PromptStore) Get() ([]byte, error) {
	if p == nil || p.input == nil || p.output == nil || p.isTerminal == nil || p.readTerminal == nil {
		return nil, fmt.Errorf("%w: prompt is not initialized", ErrNoTerminal)
	}
	fd := int(p.input.Fd())
	if !p.isTerminal(fd) {
		return nil, ErrNoTerminal
	}
	if _, err := io.WriteString(p.output, p.prompt); err != nil {
		return nil, fmt.Errorf("write credential prompt: %w", err)
	}

	secret, err := p.readTerminal(fd)
	_, newlineErr := io.WriteString(p.output, "\n")
	if err != nil {
		clearBytes(secret)
		return nil, fmt.Errorf("read hidden credential: %w", err)
	}
	if newlineErr != nil {
		clearBytes(secret)
		return nil, fmt.Errorf("finish credential prompt: %w", newlineErr)
	}
	if err := validateSecret(secret); err != nil {
		clearBytes(secret)
		return nil, err
	}
	return secret, nil
}
