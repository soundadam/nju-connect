package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/soundadam/nju-connect/internal/app"
)

func newLogoutCommand(deps app.Deps) *cobra.Command {
	return &cobra.Command{
		Use: "logout",
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := app.Logout(cmd.Context(), withInteraction(deps, app.LineOptions{}, cmd.ErrOrStderr()))
			if err != nil {
				return err
			}
			if result.OAuthHelperMissing {
				fmt.Fprintln(cmd.ErrOrStderr(), "browser sign-in state was not cleared: nju-connect-atrust-oauth-helper is not next to this binary (use the app's bundled CLI, or set NJU_CONNECT_ATRUST_OAUTH_HELPER)")
			}
			stdout := cmd.OutOrStdout()
			fmt.Fprintln(stdout, "atrust_session_cleared: true")
			fmt.Fprintf(stdout, "oauth_profile_cleared: %t\n", result.OAuthProfileCleared)
			return nil
		},
	}
}
