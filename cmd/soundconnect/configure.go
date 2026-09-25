package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
)

// runConfigure changes only non-secret connection settings. The macOS host
// adapter uses this command when a user switches between backends; passwords,
// aTrust client data, and one-time codes remain owned by the CLI/keychain
// paths and are never accepted as flags or environment variables.
func runConfigure(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("soundconnect configure", flag.ContinueOnError)
	backendValue := flags.String("backend", "", "protocol backend (easyconnect or atrust)")
	serverValue := flags.String("server", "", "campus VPN gateway host or host:port")
	usernameValue := flags.String("username", "", "campus account (preserves the saved account when omitted)")
	authTypeValue := flags.String("auth-type", "", "aTrust authentication type")
	loginDomainValue := flags.String("login-domain", "", "aTrust login domain")
	socksListenValue := flags.String("socks-listen", "", "numeric loopback SOCKS5 listener")
	upstreamProxyValue := flags.String("upstream-proxy", "", "optional socks5 upstream URL")
	if code, ok := parseFlags(flags, arguments, stdout, stderr); !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "configure accepts no positional arguments")
		return 2
	}

	paths, err := commandPaths()
	if err != nil {
		fmt.Fprintf(stderr, "resolve local state: %v\n", err)
		return 1
	}
	if err := ensureNoActiveRuntime(runtimeStatusPath(paths.Root)); err != nil {
		fmt.Fprintf(stderr, "configure profile: %v\n", err)
		return 1
	}

	configured := config.Default()
	existing, loadErr := config.Load(paths.Config)
	if loadErr == nil {
		configured = existing
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		fmt.Fprintf(stderr, "load configuration: %v\n", loadErr)
		return 1
	}

	backendName := configured.BackendName()
	if strings.TrimSpace(*backendValue) != "" {
		backendName, err = backend.ParseName(*backendValue)
		if err != nil {
			fmt.Fprintf(stderr, "select protocol backend: %v\n", err)
			return 2
		}
	}
	previousBackend := configured.BackendName()
	configured.Backend = backendName

	if value := strings.TrimSpace(*serverValue); value != "" {
		configured.Server = value
	} else if previousBackend != backendName || strings.TrimSpace(configured.Server) == "" {
		if backendName == backend.ATrust {
			configured.Server = config.DefaultATrustServer
		} else {
			configured.Server = config.DefaultServer
		}
	}
	if value := strings.TrimSpace(*usernameValue); value != "" {
		configured.Username = value
	}
	if value := strings.TrimSpace(*upstreamProxyValue); value != "" {
		configured.UpstreamProxy = value
	}
	if value := strings.TrimSpace(*socksListenValue); value != "" {
		configured.SOCKSListen = value
	} else if strings.TrimSpace(configured.SOCKSListen) == "" {
		configured.SOCKSListen = config.DefaultSOCKSListen
	}

	if backendName == backend.ATrust {
		if value := strings.TrimSpace(*authTypeValue); value != "" {
			configured.AuthType = value
		} else if previousBackend != backendName || strings.TrimSpace(configured.AuthType) == "" {
			// A backend switch reuses the shared Keychain password. The concrete
			// tenant login domain is discovered by connect instead of being
			// encoded in the application or presentation layer.
			configured.AuthType = atrustPasswordAuthType
		}
		if value := strings.TrimSpace(*loginDomainValue); value != "" {
			configured.LoginDomain = value
		} else if previousBackend != backendName {
			configured.LoginDomain = ""
		}
		if err := validateATrustAuthenticationType(configured.AuthType); err != nil {
			fmt.Fprintf(stderr, "configure aTrust profile: %v\n", err)
			return 2
		}
	} else {
		configured.AuthType = ""
		configured.LoginDomain = ""
	}

	if err := configured.Validate(); err != nil {
		fmt.Fprintf(stderr, "validate profile: %v\n", err)
		return 2
	}
	if err := config.Replace(paths.Config, configured); err != nil {
		fmt.Fprintf(stderr, "write profile: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "configuration: %s\nbackend: %s\nserver: %s\nsocks_listen: %s\n",
		paths.Config, configured.BackendName(), configured.Server, configured.SOCKSListen)
	if configured.BackendName() == backend.ATrust {
		fmt.Fprintf(stdout, "auth_type: %s\nlogin_domain: %s\n", configured.AuthType, configured.LoginDomain)
	}
	return 0
}
