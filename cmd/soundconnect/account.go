package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

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

func newAccountCommand(deps app.Deps) *cobra.Command {
	account := &cobra.Command{
		Use:  "account",
		Long: accountUsage,
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) > 0 {
				return app.Usagef("unknown account command %q; run \"soundconnect account -h\"", arguments[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAccountOverview(cmd.Context(), deps, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	account.AddCommand(
		newAccountShowCommand(deps),
		newAccountSetPasswordCommand(deps),
		newAccountSetUsernameCommand(deps),
		newAccountForgetCommand(deps),
	)
	return account
}

// runAccountOverview opens the account menu on a terminal and shows the
// saved account otherwise.
func runAccountOverview(ctx context.Context, deps app.Deps, stdout, stderr io.Writer) error {
	deps = withInteraction(deps, app.LineOptions{}, stderr)
	if deps.Interactive {
		return app.AccountMenu(ctx, deps, func(info app.AccountInfo) {
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

func newAccountShowCommand(deps app.Deps) *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use: "show",
		RunE: func(cmd *cobra.Command, _ []string) error {
			info, err := app.AccountShow(withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			if asJSON {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(info); err != nil {
					return fmt.Errorf("encode account: %w", err)
				}
				return nil
			}
			writeAccountInfo(cmd.OutOrStdout(), info)
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "print JSON")
	return command
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

func newAccountSetPasswordCommand(deps app.Deps) *cobra.Command {
	var passwordStdin bool
	command := &cobra.Command{
		Use: "set-password",
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := withInteraction(deps, app.LineOptions{PasswordFromStdin: passwordStdin}, cmd.ErrOrStderr())
			if err := app.SetPassword(cmd.Context(), deps); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "password: saved")
			return nil
		},
	}
	command.Flags().BoolVar(&passwordStdin, "password-stdin", false, "read the password from standard input without a terminal prompt")
	return command
}

func newAccountSetUsernameCommand(deps app.Deps) *cobra.Command {
	return &cobra.Command{
		Use:  "set-username <name>",
		Long: "usage: soundconnect account set-username <name>",
		Args: func(_ *cobra.Command, arguments []string) error {
			if len(arguments) != 1 {
				return app.Usagef("set-username takes exactly one account name")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, arguments []string) error {
			cleared, err := app.SetUsername(withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()), arguments[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "username: %s\natrust_session_cleared: %t\n", strings.TrimSpace(arguments[0]), cleared)
			return nil
		},
	}
}

func newAccountForgetCommand(deps app.Deps) *cobra.Command {
	var request app.ForgetRequest
	command := &cobra.Command{
		Use: "forget",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := app.Forget(cmd.Context(), withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()), request)
			if err != nil {
				return err
			}
			stdout := cmd.OutOrStdout()
			if request.Password {
				fmt.Fprintf(stdout, "password_forgotten: %t\n", result.PasswordForgotten)
			}
			if request.Session {
				fmt.Fprintf(stdout, "atrust_session_forgotten: %t\noauth_profile_cleared: %t\n",
					result.SessionForgotten, result.OAuthProfileCleared)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&request.Password, "password", false, "forget the saved VPN password")
	command.Flags().BoolVar(&request.Session, "session", false, "forget the saved aTrust session and OAuth browser profile")
	return command
}
