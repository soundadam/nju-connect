package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/soundadam/soundconnect/internal/credential"
)

func TestReadRawVerificationCodeAcceptsEnterAndBackspace(t *testing.T) {
	code, err := readRawVerificationCode(bytes.NewReader([]byte{'1', '2', 0x7f, '3', '\r'}))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(code)
	if string(code) != "13" {
		t.Fatalf("code = %q", code)
	}
}

func TestReadRawVerificationCodeCancelsOnControlC(t *testing.T) {
	code, err := readRawVerificationCode(bytes.NewReader([]byte{'1', '2', 0x03}))
	if !errors.Is(err, context.Canceled) || code != nil {
		t.Fatalf("code=%v error=%v", code, err)
	}
}

func TestReadRawVerificationCodeRejectsEOFAndUnboundedInput(t *testing.T) {
	if code, err := readRawVerificationCode(bytes.NewReader([]byte{0x04})); !errors.Is(err, io.EOF) || code != nil {
		t.Fatalf("EOF code=%v error=%v", code, err)
	}
	input := append(bytes.Repeat([]byte{'1'}, 65), '\r')
	if code, err := readRawVerificationCode(bytes.NewReader(input)); err == nil || code != nil {
		t.Fatalf("long code=%v error=%v", code, err)
	}
}

func TestPromptVerificationCodeAllowsExplicitNonTerminalInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.WriteString("123456\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	code, err := promptVerificationCode(context.Background(), reader, &output, true)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(code)
	if string(code) != "123456" || output.String() != "Verification code: \n" {
		t.Fatalf("code=%q output=%q", code, output.String())
	}
}

func TestPromptVerificationCodeRejectsImplicitNonTerminalInput(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	var output strings.Builder
	code, err := promptVerificationCode(context.Background(), reader, &output, false)
	if !errors.Is(err, credential.ErrNoTerminal) || code != nil || output.Len() != 0 {
		t.Fatalf("code=%v error=%v output=%q", code, err, output.String())
	}
}
