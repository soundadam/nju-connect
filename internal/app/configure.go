package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
)

// ConfigureRequest changes non-secret connection settings. Empty fields keep
// the saved value, or the backend default after a backend switch.
type ConfigureRequest struct {
	Backend       string
	Server        string
	Username      string
	AuthType      string
	LoginDomain   string
	SOCKSListen   string
	UpstreamProxy string
}

// ConfigureResult is the configuration as written.
type ConfigureResult struct {
	Path   string
	Config config.Config
}

// Configure merges request into the saved configuration. The macOS app uses
// it when the user switches backends. Passwords, aTrust client data and
// one-time codes stay with the credential stores and are never accepted
// here. It refuses while a runtime is active.
func Configure(deps Deps, request ConfigureRequest) (ConfigureResult, error) {
	paths, err := deps.Paths()
	if err != nil {
		return ConfigureResult{}, fmt.Errorf("resolve local state: %w", err)
	}
	if err := runtimecontrol.EnsureNoActive(runtimecontrol.Path(paths.Root)); err != nil {
		return ConfigureResult{}, fmt.Errorf("configure profile: %w", err)
	}

	configured := config.Default()
	existing, loadErr := config.Load(paths.Config)
	if loadErr == nil {
		configured = existing
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return ConfigureResult{}, fmt.Errorf("load configuration: %w", loadErr)
	}

	backendName := configured.BackendName()
	if strings.TrimSpace(request.Backend) != "" {
		backendName, err = backend.ParseName(request.Backend)
		if err != nil {
			return ConfigureResult{}, Usagef("select protocol backend: %w", err)
		}
	}
	previousBackend := configured.BackendName()
	configured.Backend = backendName

	if value := strings.TrimSpace(request.Server); value != "" {
		configured.Server = value
	} else if previousBackend != backendName || strings.TrimSpace(configured.Server) == "" {
		configured.Server = defaultServer(backendName)
	}
	if value := strings.TrimSpace(request.Username); value != "" {
		configured.Username = value
	}
	if value := strings.TrimSpace(request.UpstreamProxy); value != "" {
		configured.UpstreamProxy = value
	}
	if value := strings.TrimSpace(request.SOCKSListen); value != "" {
		configured.SOCKSListen = value
	} else if strings.TrimSpace(configured.SOCKSListen) == "" {
		configured.SOCKSListen = config.DefaultSOCKSListen
	}

	if backendName == backend.ATrust {
		if value := strings.TrimSpace(request.AuthType); value != "" {
			configured.AuthType = value
		} else if previousBackend != backendName || strings.TrimSpace(configured.AuthType) == "" {
			// A backend switch reuses the shared password. The concrete
			// tenant login domain is discovered by connect instead of being
			// encoded in the application or presentation layer.
			configured.AuthType = ATrustPasswordAuthType
		}
		if value := strings.TrimSpace(request.LoginDomain); value != "" {
			configured.LoginDomain = value
		} else if previousBackend != backendName {
			configured.LoginDomain = ""
		}
		if err := ValidateATrustAuthenticationType(configured.AuthType); err != nil {
			return ConfigureResult{}, Usagef("configure aTrust profile: %w", err)
		}
	} else {
		configured.AuthType = ""
		configured.LoginDomain = ""
	}

	if err := configured.Validate(); err != nil {
		return ConfigureResult{}, Usagef("validate profile: %w", err)
	}
	if err := config.Replace(paths.Config, configured); err != nil {
		return ConfigureResult{}, fmt.Errorf("write profile: %w", err)
	}
	return ConfigureResult{Path: paths.Config, Config: configured}, nil
}

func defaultServer(backendName backend.Name) string {
	if backendName == backend.ATrust {
		return config.DefaultATrustServer
	}
	return config.DefaultServer
}
