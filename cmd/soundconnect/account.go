package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/soundadam/soundconnect/internal/app"
)

const accountUsage = `usage: soundconnect account [command] [flags]

With no command, account shows the saved account; on a terminal it opens a
menu to change it.

commands:
  show          print the saved account and which secrets are saved
  set-password  replace the saved VPN password
  set-username  change the saved account name and forget its aTrust session
  forget        forget the saved password and/or aTrust session

Every change refuses while a runtime is active.`

func runAccount(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		flags := flag.NewFlagSet("soundconnect account", flag.ContinueOnError)
		flags.Usage = func() { fmt.Fprintln(flags.Output(), accountUsage) }
		if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
			return err
		}
		return runAccountOverview(deps, stdout, stderr)
	}
	command, rest := arguments[0], arguments[1:]
	switch command {
	case "show":
		return runAccountShow(deps, rest, stdout, stderr)
	case "set-password":
		return runAccountSetPassword(deps, rest, stdout, stderr)
	case "set-username":
		return runAccountSetUsername(deps, rest, stdout, stderr)
	case "forget":
		return runAccountForget(deps, rest, stdout, stderr)
	default:
		return app.Usagef("unknown account command %q; run \"soundconnect account -h\"", command)
	}
}

// runAccountOverview opens the account menu on a terminal and shows the
// saved account otherwise.
func runAccountOverview(deps app.Deps, stdout, stderr io.Writer) error {
	deps = withInteraction(deps, app.LineOptions{}, stderr)
	if deps.Interactive {
		return app.AccountMenu(context.Background(), deps, func(info app.AccountInfo) {
			fmt.Fprintln(stderr)
			writeAccountInfo(stderr, info)
		})
	}
	info, err := app.AccountShow(deps)
	if err != nil {
		return err
	}
	writeAccountInfo(stdout, info)
	return nil
}

func runAccountShow(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect account show", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print JSON")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	info, err := app.AccountShow(withInteraction(deps, app.LineOptions{}, stderr))
	if err != nil {
		return err
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(info); err != nil {
			return fmt.Errorf("encode account: %w", err)
		}
		return nil
	}
	writeAccountInfo(stdout, info)
	return nil
}

func writeAccountInfo(output io.Writer, info app.AccountInfo) {
	fmt.Fprintf(output, "configuration: %s\n", info.Configuration)
	if info.Configuration == "ready" {
		fmt.Fprintf(output, "backend: %s\nserver: %s\nusername: %s\n", info.Backend, info.Server, info.Username)
		if info.AuthType != "" {
			fmt.Fprintf(output, "auth_type: %s\n", info.AuthType)
		}
	}
	fmt.Fprintf(output, "credential_store: %s\npassword: %s\natrust_session: %s\n",
		info.CredentialStore, info.Password, info.ATrustSession)
}

func runAccountSetPassword(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect account set-password", flag.ContinueOnError)
	passwordStdin := flags.Bool("password-stdin", false, "read the password from standard input without a terminal prompt")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	deps = withInteraction(deps, app.LineOptions{PasswordFromStdin: *passwordStdin}, stderr)
	if err := app.SetPassword(context.Background(), deps); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "password: saved")
	return nil
}

func runAccountSetUsername(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect account set-username", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: soundconnect account set-username <name>")
	}
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return exitCode(code)
	}
	if flags.NArg() != 1 {
		return app.Usagef("set-username takes exactly one account name")
	}
	cleared, err := app.SetUsername(withInteraction(deps, app.LineOptions{}, stderr), flags.Arg(0))
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "username: %s\natrust_session_cleared: %t\n", strings.TrimSpace(flags.Arg(0)), cleared)
	return nil
}

func runAccountForget(deps app.Deps, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("soundconnect account forget", flag.ContinueOnError)
	var request app.ForgetRequest
	flags.BoolVar(&request.Password, "password", false, "forget the saved VPN password")
	flags.BoolVar(&request.Session, "session", false, "forget the saved aTrust session and OAuth browser profile")
	if err := parseCommand(flags, arguments, stdout, stderr); err != nil {
		return err
	}
	result, err := app.Forget(context.Background(), withInteraction(deps, app.LineOptions{}, stderr), request)
	if err != nil {
		return err
	}
	if request.Password {
		fmt.Fprintf(stdout, "password_forgotten: %t\n", result.PasswordForgotten)
	}
	if request.Session {
		fmt.Fprintf(stdout, "atrust_session_forgotten: %t\noauth_profile_cleared: %t\n",
			result.SessionForgotten, result.OAuthProfileCleared)
	}
	return nil
}
