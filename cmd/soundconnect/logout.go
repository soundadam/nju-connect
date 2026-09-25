package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/soundadam/soundconnect/internal/credential"
)

const atrustLogoutTimeout = 15 * time.Second

// runLogout forgets only aTrust authentication state. It deliberately does
// not touch the EasyConnect password item or the non-secret configuration.
func runLogout(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect logout", flag.ContinueOnError)
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "logout accepts no positional arguments")
		return 2
	}

	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	store, err := newATrustClientDataStore(paths.ATrustClientData)
	if err != nil {
		fmt.Fprintf(stderr, "prepare aTrust session store: %v\n", err)
		return 1
	}
	clearable, ok := store.(credential.Clearable)
	if !ok {
		fmt.Fprintln(stderr, "aTrust session store does not support clearing")
		return 1
	}
	if err := clearable.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stderr, "clear aTrust session: %v\n", err)
		return 1
	}

	oauthProfileCleared := false
	if helperPath, available := atrustOAuthHelperPath(); available {
		clearContext, cancel := context.WithTimeout(context.Background(), atrustLogoutTimeout)
		defer cancel()
		command := exec.CommandContext(clearContext, helperPath, "--clear-data")
		command.Stdout = io.Discard
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			if clearContext.Err() != nil {
				fmt.Fprintln(stderr, "clear OAuth profile: timed out")
			} else {
				fmt.Fprintf(stderr, "clear OAuth profile: %v\n", err)
			}
			return 1
		}
		oauthProfileCleared = true
	} else if runtime.GOOS == "darwin" {
		fmt.Fprintln(stderr, "clear OAuth profile: bundled aTrust OAuth helper is unavailable")
		return 1
	}

	fmt.Fprintln(stdout, "atrust_session_cleared: true")
	fmt.Fprintf(stdout, "oauth_profile_cleared: %t\n", oauthProfileCleared)
	return 0
}
