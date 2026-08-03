package main

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) = %d", code)
	}
	if got := stdout.String(); got != "soundconnect dev\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestPromptLineReadsTrimmedLocalInput(t *testing.T) {
	var output bytes.Buffer
	value, err := promptLine(bufio.NewReader(strings.NewReader("  vpn.example.edu  \n")), &output, "Gateway: ")
	if err != nil {
		t.Fatal(err)
	}
	if value != "vpn.example.edu" {
		t.Fatalf("value = %q", value)
	}
	if output.String() != "Gateway: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestPromptLineRejectsEmptyInput(t *testing.T) {
	var output bytes.Buffer
	if _, err := promptLine(bufio.NewReader(strings.NewReader("\n")), &output, "Account: "); err == nil {
		t.Fatal("promptLine() accepted empty input")
	}
}

func TestPromptLineDefaultAcceptsEmptyInput(t *testing.T) {
	var output bytes.Buffer
	value, err := promptLineDefault(bufio.NewReader(strings.NewReader("\n")), &output, "Gateway", "vpn.nju.edu.cn")
	if err != nil {
		t.Fatal(err)
	}
	if value != "vpn.nju.edu.cn" {
		t.Fatalf("value = %q", value)
	}
	if output.String() != "Gateway [vpn.nju.edu.cn]: " {
		t.Fatalf("output = %q", output.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"unknown"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown) = %d", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "unknown"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
