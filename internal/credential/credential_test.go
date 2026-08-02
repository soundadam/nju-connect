package credential

import (
	"errors"
	"testing"
)

func TestParseBackend(t *testing.T) {
	tests := []struct {
		input string
		want  Backend
	}{
		{input: "", want: BackendPrompt},
		{input: "prompt", want: BackendPrompt},
		{input: "file", want: BackendFile},
	}
	for _, test := range tests {
		got, err := ParseBackend(test.input)
		if err != nil {
			t.Fatalf("ParseBackend(%q) error = %v", test.input, err)
		}
		if got != test.want {
			t.Errorf("ParseBackend(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if _, err := ParseBackend("memory"); !errors.Is(err, ErrUnknownBackend) {
		t.Fatalf("ParseBackend(unknown) error = %v", err)
	}
}

func TestOpenFileRequiresExplicitPlaintextOptIn(t *testing.T) {
	_, err := Open(Options{Backend: BackendFile, FilePath: "credential"})
	if !errors.Is(err, ErrPlaintextOptIn) {
		t.Fatalf("Open(file without opt-in) error = %v", err)
	}
}
