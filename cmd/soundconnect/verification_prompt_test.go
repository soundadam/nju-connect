package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
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
