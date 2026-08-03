package credential

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestPromptGetUsesHiddenTerminalReaderAndDoesNotPrintCredential(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "terminal")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer input.Close()

	var output bytes.Buffer
	store := NewPromptStore(PromptOptions{Input: input, Output: &output})
	store.isTerminal = func(fd int) bool { return fd == int(input.Fd()) }
	store.readTerminal = func(fd int) ([]byte, error) {
		if fd != int(input.Fd()) {
			t.Fatalf("read fd = %d, want %d", fd, input.Fd())
		}
		return []byte("synthetic-hidden-input"), nil
	}

	got, err := store.Get()
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if string(got) != "synthetic-hidden-input" {
		t.Fatal("Get() returned unexpected credential")
	}
	if strings.Contains(output.String(), string(got)) {
		t.Fatal("prompt output disclosed credential")
	}
	if output.String() != defaultPrompt+"\n" {
		t.Fatalf("prompt output = %q", output.String())
	}
}

func TestPromptRejectsNonTerminalBeforeReading(t *testing.T) {
	input, err := os.CreateTemp(t.TempDir(), "not-terminal")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer input.Close()

	var output bytes.Buffer
	store := NewPromptStore(PromptOptions{Input: input, Output: &output})
	store.isTerminal = func(int) bool { return false }
	store.readTerminal = func(int) ([]byte, error) {
		t.Fatal("readTerminal called for a non-terminal")
		return nil, nil
	}

	if _, err := store.Get(); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("Get() error = %v, want ErrNoTerminal", err)
	}
	if output.Len() != 0 {
		t.Fatalf("non-terminal prompt wrote %q", output.String())
	}
}
