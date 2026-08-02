package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/doctor"
	"github.com/soundadam/soundconnect/internal/gatewayauth"
	setupservice "github.com/soundadam/soundconnect/internal/setup"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeUsage(stdout)
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		writeUsage(stdout)
		return 0
	case "version":
		fmt.Fprintf(stdout, "soundconnect %s\n", version)
		return 0
	case "setup":
		return runSetup(arguments[1:], stdout, stderr)
	case "doctor":
		return runDoctor(arguments[1:], stdout, stderr)
	case "connect":
		return runConnect(arguments[1:], stdout, stderr)
	case "status", "observe":
		fmt.Fprintf(stderr, "soundconnect %s is not implemented yet\n", arguments[0])
		return 2
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", arguments[0])
		writeUsage(stderr)
		return 2
	}
}

func runConnect(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worktree := flags.String("worktree", ".", "soundconnect working tree")
	resolveIP := flags.String("resolve-ip", "", "development-only numeric gateway address override")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "connect accepts no positional arguments")
		return 2
	}
	paths, err := config.LocalPaths(*worktree)
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	configured, err := config.Load(paths.Config)
	if err != nil {
		fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}
	passwordStore, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		fmt.Fprintf(stderr, "open credential: %v\n", err)
		return 1
	}
	password, err := passwordStore.Get()
	if err != nil {
		fmt.Fprintf(stderr, "read credential: %v\n", err)
		return 1
	}
	defer credential.Clear(password)
	client, err := gatewayauth.New(gatewayauth.Options{
		Server: configured.Server, ResolveIP: *resolveIP,
		TLSInsecure: configured.TLSInsecure, UpstreamProxy: configured.UpstreamProxy,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		fmt.Fprintf(stderr, "prepare gateway authentication: %v\n", err)
		return 1
	}
	result, err := client.AuthenticatePassword(context.Background(), configured.Username, password)
	if err != nil {
		fmt.Fprintf(stderr, "authenticate password: %v\n", err)
		return 1
	}
	if result.NeedsSMS() {
		code, promptErr := promptVerificationCode(os.Stdin, stderr)
		if promptErr != nil {
			fmt.Fprintf(stderr, "read verification code: %v\n", promptErr)
			return 1
		}
		defer credential.Clear(code)
		result, err = client.AuthenticateSMS(context.Background(), code)
		if err != nil {
			fmt.Fprintf(stderr, "authenticate verification code: %v\n", err)
			return 1
		}
	}
	if result.Accepted() {
		fmt.Fprintln(stdout, "authentication: accepted")
		session, sessionErr := client.TakeSession()
		if sessionErr != nil {
			fmt.Fprintf(stderr, "retain authenticated session: %v\n", sessionErr)
			return 1
		}
		defer session.Close()
		fmt.Fprintln(stdout, "session: retained_in_memory")
		bootstrap, probeErr := session.ProbeBootstrap(context.Background())
		if probeErr != nil {
			fmt.Fprintf(stderr, "probe gateway bootstrap: %v\n", probeErr)
			return 1
		}
		if !bootstrap.ConfigurationAvailable || !bootstrap.ResourcesAvailable {
			fmt.Fprintln(stderr, "probe gateway bootstrap: required configuration or resources are absent")
			return 1
		}
		fmt.Fprintln(stdout, "initialization: gateway_bootstrap_available")
		fmt.Fprintln(stdout, "dataplane: not_started")
		return 0
	}
	if result.NextService != "" {
		fmt.Fprintf(stderr, "authentication requires unsupported next step %q (gateway code %d)\n", result.NextService, result.Code)
	} else {
		fmt.Fprintf(stderr, "authentication rejected by gateway code %d\n", result.Code)
	}
	return 1
}

func promptVerificationCode(input *os.File, output io.Writer) ([]byte, error) {
	if input == nil || !term.IsTerminal(int(input.Fd())) {
		return nil, credential.ErrNoTerminal
	}
	if _, err := io.WriteString(output, "Verification code: "); err != nil {
		return nil, err
	}
	code, err := term.ReadPassword(int(input.Fd()))
	fmt.Fprintln(output)
	if err != nil {
		return nil, err
	}
	code = []byte(strings.TrimSpace(string(code)))
	if len(code) == 0 {
		return nil, errors.New("verification code is required")
	}
	return code, nil
}

func runSetup(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worktree := flags.String("worktree", ".", "soundconnect working tree")
	server := flags.String("server", "", "campus VPN gateway host or host:port")
	username := flags.String("username", "", "campus account")
	socksListen := flags.String("socks-listen", config.DefaultSOCKSListen, "numeric loopback SOCKS5 listener")
	upstreamProxy := flags.String("upstream-proxy", "", "optional socks5 upstream URL")
	tlsInsecure := flags.Bool("tls-insecure", false, "allow an unverified development gateway certificate")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "setup accepts no positional arguments")
		return 2
	}
	lineReader := bufio.NewReader(os.Stdin)
	if strings.TrimSpace(*server) == "" {
		value, err := promptLine(lineReader, stderr, "Gateway: ")
		if err != nil {
			fmt.Fprintf(stderr, "read gateway: %v\n", err)
			return 1
		}
		*server = value
	}
	if strings.TrimSpace(*username) == "" {
		value, err := promptLine(lineReader, stderr, "Account: ")
		if err != nil {
			fmt.Fprintf(stderr, "read account: %v\n", err)
			return 1
		}
		*username = value
	}
	paths, err := config.LocalPaths(*worktree)
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	configured := config.Config{
		Server:        *server,
		Username:      *username,
		SOCKSListen:   *socksListen,
		UpstreamProxy: *upstreamProxy,
		TLSInsecure:   *tlsInsecure,
	}
	readSecret := func(prompt string) ([]byte, error) {
		return credential.NewPromptStore(credential.PromptOptions{
			Input: os.Stdin, Output: stderr, Prompt: prompt,
		}).Get()
	}
	if err := setupservice.Save(paths, configured, readSecret); err != nil {
		fmt.Fprintf(stderr, "setup failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "configuration: %s\ncredential: %s\n", paths.Config, paths.Credential)
	return 0
}

func promptLine(input *bufio.Reader, output io.Writer, prompt string) (string, error) {
	if _, err := io.WriteString(output, prompt); err != nil {
		return "", err
	}
	value, err := input.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("value is required")
	}
	return value, nil
}

func runDoctor(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	worktree := flags.String("worktree", ".", "soundconnect working tree")
	asJSON := flags.Bool("json", false, "print JSON")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor accepts no positional arguments")
		return 2
	}
	paths, err := config.LocalPaths(*worktree)
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	report := doctor.Build(paths)
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "encode report: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "ready: %t\nconfiguration: %s\ncredential_file: %s\nupstream_proxy: %s\n",
			report.Ready, report.Configuration, report.CredentialFile, report.UpstreamProxy)
	}
	if !report.Ready {
		return 1
	}
	return 0
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, `usage: soundconnect <command>

commands:
  setup      configure the account and long-lived password
	connect    probe attended gateway authentication
  status     print sanitized runtime status
  doctor     inspect the local development environment
  observe    record a sanitized behavior timeline
  version    print build identity`)
}
