package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/soundadam/nju-connect/internal/backend"
	"github.com/soundadam/nju-connect/internal/config"
	"github.com/soundadam/nju-connect/internal/credential"
	"github.com/soundadam/nju-connect/internal/runtimecontrol"
)

// SetupRequest describes the profile to save. Setup starts from the saved
// configuration, so settings the request leaves unset (nil or empty) keep
// their saved values; missing required values are asked for through
// Deps.Interaction.
type SetupRequest struct {
	// Backend is the protocol backend. Empty keeps the saved backend, asks
	// when Guided, or selects EasyConnect.
	Backend     string
	Server      string
	Username    string
	AuthType    string
	LoginDomain string
	// Nil keeps the saved value.
	SOCKSListen       *string
	UpstreamProxy     *string
	TLSInsecure       *bool
	NativeTLSInsecure *bool
	// PasswordSupplied means the password arrives without a prompt, as with
	// setup --password-stdin from the macOS app. On aTrust with no AuthType
	// that is an explicit request for shared-password authentication;
	// discovery still determines the tenant-specific login domain.
	PasswordSupplied bool
	// Guided asks for every choice a terminal user can make, including the
	// backend and the aTrust authentication method, with the saved values
	// as defaults. Without it Setup asks only for missing required values.
	Guided bool
}

// Credential kinds reported by Setup.
const (
	CredentialSystemStore         = "system_store"
	CredentialBrowserOAuth        = "browser_oauth"
	CredentialSystemStorePassword = "system_store_password"
)

// SetupResult describes the saved profile.
type SetupResult struct {
	Path       string
	Backend    backend.Name
	Credential string
	// Config is the configuration as written.
	Config config.Config
	// SessionCleared reports that a saved aTrust session was forgotten
	// because the account, gateway or backend changed.
	SessionCleared bool
}

// Setup saves the configuration and, unless the profile uses aTrust OAuth,
// the long-lived password. The password is stored first, so a failure
// leaves the saved configuration untouched. It refuses while a runtime is
// active.
func Setup(ctx context.Context, deps Deps, request SetupRequest) (SetupResult, error) {
	var backendName backend.Name
	if strings.TrimSpace(request.Backend) != "" {
		parsed, err := backend.ParseName(request.Backend)
		if err != nil {
			return SetupResult{}, Usagef("select protocol backend: %w", err)
		}
		backendName = parsed
	}
	paths, err := deps.Paths()
	if err != nil {
		return SetupResult{}, fmt.Errorf("resolve local state: %w", err)
	}
	if err := runtimecontrol.EnsureNoActive(runtimecontrol.Path(paths.Root)); err != nil {
		return SetupResult{}, fmt.Errorf("setup profile: %w", err)
	}
	// An unreadable configuration is replaced: setup is how it gets fixed.
	saved, loadErr := config.Load(paths.Config)
	hasSaved := loadErr == nil

	if backendName == "" {
		backendName = backend.EasyConnect
		if hasSaved {
			backendName = saved.BackendName()
		}
		if request.Guided {
			if backendName, err = askBackend(ctx, deps, backendName); err != nil {
				return SetupResult{}, err
			}
		}
	}
	if backendName != backend.ATrust && (strings.TrimSpace(request.AuthType) != "" || strings.TrimSpace(request.LoginDomain) != "") {
		return SetupResult{}, Usagef("auth-type and login-domain are only available for the aTrust backend")
	}

	configured := config.Default()
	if hasSaved {
		configured = saved
	}
	configured.Backend = backendName
	sameBackend := hasSaved && saved.BackendName() == backendName
	applySetupOverrides(&configured, request)

	configured.Server = request.Server
	if strings.TrimSpace(configured.Server) == "" {
		defaultGateway := defaultServer(backendName)
		if sameBackend && strings.TrimSpace(saved.Server) != "" {
			defaultGateway = saved.Server
		}
		if configured.Server, err = deps.Interaction.Input(ctx, "Gateway", defaultGateway, nil); err != nil {
			return SetupResult{}, fmt.Errorf("read gateway: %w", err)
		}
	}
	configured.Username = request.Username
	savedUsername := ""
	if hasSaved {
		savedUsername = saved.Username
	}
	if backendName == backend.EasyConnect && strings.TrimSpace(configured.Username) == "" {
		if configured.Username, err = askAccount(ctx, deps, savedUsername); err != nil {
			return SetupResult{}, err
		}
	}
	if backendName == backend.ATrust {
		authType := request.AuthType
		if request.PasswordSupplied && strings.TrimSpace(authType) == "" {
			authType = ATrustPasswordAuthType
		}
		preferred := ""
		if sameBackend {
			preferred = saved.AuthType
		}
		if err := chooseATrustSetupMethod(ctx, deps, &configured, authType, request.LoginDomain, request.Guided, preferred); err != nil {
			return SetupResult{}, err
		}
		if configured.AuthType == ATrustPasswordAuthType && strings.TrimSpace(configured.Username) == "" {
			if configured.Username, err = askAccount(ctx, deps, savedUsername); err != nil {
				return SetupResult{}, err
			}
		}
	} else {
		configured.AuthType = ""
		configured.LoginDomain = ""
	}

	var passwordStore credential.Store
	if usesPassword(configured) {
		passwordStore, err = deps.PasswordStore(PasswordLocation(paths, configured.CredentialStore))
		if err != nil {
			return SetupResult{}, fmt.Errorf("prepare credential store: %w", err)
		}
	}
	readPassword := func() ([]byte, error) { return deps.Interaction.Password(ctx, "VPN password") }
	if request.Guided && passwordStore != nil && savedUsername == configured.Username && passwordStore.Inspect() == nil {
		keep, err := deps.Interaction.Confirm(ctx, "Keep the saved VPN password?", true)
		if err != nil {
			return SetupResult{}, fmt.Errorf("read password choice: %w", err)
		}
		if keep {
			readPassword = nil
		}
	}
	if err := save(paths, configured, passwordStore, readPassword); err != nil {
		return SetupResult{}, fmt.Errorf("setup failed: %w", err)
	}

	result := SetupResult{Path: paths.Config, Backend: backendName, Credential: CredentialSystemStore, Config: configured}
	if backendName == backend.ATrust {
		result.Credential = CredentialBrowserOAuth
		if configured.AuthType == ATrustPasswordAuthType {
			result.Credential = CredentialSystemStorePassword
		}
	}
	// The aTrust session belongs to one account on one gateway.
	if hasSaved && (saved.Username != configured.Username || saved.Server != configured.Server || saved.BackendName() != backendName) {
		cleared, err := forgetATrustSession(deps, paths, configured.CredentialStore)
		if err != nil {
			return SetupResult{}, err
		}
		result.SessionCleared = cleared
	}
	return result, nil
}

func applySetupOverrides(configured *config.Config, request SetupRequest) {
	if request.SOCKSListen != nil {
		configured.SOCKSListen = *request.SOCKSListen
	}
	if strings.TrimSpace(configured.SOCKSListen) == "" {
		configured.SOCKSListen = config.DefaultSOCKSListen
	}
	if request.UpstreamProxy != nil {
		configured.UpstreamProxy = *request.UpstreamProxy
	}
	if request.TLSInsecure != nil {
		configured.TLSInsecure = *request.TLSInsecure
	}
	if request.NativeTLSInsecure != nil {
		configured.NativeTLSInsecure = *request.NativeTLSInsecure
	}
}

func askBackend(ctx context.Context, deps Deps, defaultBackend backend.Name) (backend.Name, error) {
	var options []Option
	for _, descriptor := range backend.Catalog() {
		options = append(options, Option{Value: string(descriptor.ID), Label: descriptor.DisplayName})
	}
	value, err := deps.Interaction.Select(ctx, "Protocol backend", options, string(defaultBackend))
	if err != nil {
		return "", fmt.Errorf("read backend: %w", err)
	}
	return backend.ParseName(value)
}

func askAccount(ctx context.Context, deps Deps, defaultAccount string) (string, error) {
	account, err := deps.Interaction.Input(ctx, "Account", defaultAccount, nil)
	if err != nil {
		return "", fmt.Errorf("read account: %w", err)
	}
	return account, nil
}

// chooseATrustSetupMethod selects the aTrust authentication method from the
// gateway's advertised methods. Guided with no requested type, the user
// picks one, starting from preferred.
func chooseATrustSetupMethod(
	ctx context.Context, deps Deps, configured *config.Config,
	authType, loginDomain string, guided bool, preferred string,
) error {
	endpoint, err := ParseATrustEndpoint(configured.Server)
	if err != nil {
		return Usagef("parse aTrust gateway: %w", err)
	}
	var methods []backend.AuthenticationMethod
	err = deps.Interaction.Wait(ctx, "Discovering sign-in methods on "+configured.Server, func(ctx context.Context) error {
		methods, err = discoverATrust(ctx, deps, endpoint)
		return err
	})
	if err != nil {
		return err
	}
	if guided && strings.TrimSpace(authType) == "" {
		if authType, err = askATrustMethod(ctx, deps, methods, preferred); err != nil {
			return err
		}
	}
	selected, err := SelectATrustAuthenticationMethod(methods, authType, loginDomain)
	if err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	if err := ValidateATrustAuthenticationType(selected.Type); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	configured.AuthType = selected.Type
	configured.LoginDomain = selected.Domain
	return nil
}

func askATrustMethod(ctx context.Context, deps Deps, methods []backend.AuthenticationMethod, preferred string) (string, error) {
	var options []Option
	seen := map[string]bool{}
	for _, method := range methods {
		if ValidateATrustAuthenticationType(method.Type) != nil || seen[method.Type] {
			continue
		}
		seen[method.Type] = true
		label := "Browser sign-in (OAuth)"
		if method.Type == ATrustPasswordAuthType {
			label = "Account and password"
		}
		if name := strings.TrimSpace(method.Name); name != "" {
			label += " — " + name
		}
		options = append(options, Option{Value: method.Type, Label: label})
	}
	if len(options) == 0 {
		return "", errors.New("setup failed: the gateway offers no supported aTrust sign-in method")
	}
	if !seen[preferred] {
		preferred = options[0].Value
		if seen[ATrustOAuthAuthType] {
			preferred = ATrustOAuthAuthType
		}
	}
	value, err := deps.Interaction.Select(ctx, "Sign-in method", options, preferred)
	if err != nil {
		return "", fmt.Errorf("read sign-in method: %w", err)
	}
	return value, nil
}

// forgetATrustSession clears the saved aTrust client data and reports
// whether there was any.
func forgetATrustSession(deps Deps, paths config.Paths, credentialBackend string) (bool, error) {
	store, err := deps.ATrustSessionStore(ATrustSessionLocation(paths, credentialBackend))
	if err != nil {
		return false, fmt.Errorf("prepare aTrust session store: %w", err)
	}
	clearable, ok := store.(credential.Clearable)
	if !ok {
		return false, errors.New("aTrust session store does not support clearing")
	}
	existed := store.Inspect() == nil
	if err := clearable.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("clear aTrust session: %w", err)
	}
	return existed, nil
}

// usesPassword reports whether a profile authenticates with the shared
// long-lived password; aTrust OAuth keeps none.
func usesPassword(configured config.Config) bool {
	return configured.BackendName() != backend.ATrust || configured.AuthType == ATrustPasswordAuthType
}

// save stores the long-lived password, then the non-secret configuration,
// without accepting the password through argv or env. A nil readPassword
// keeps the saved password.
func save(paths config.Paths, configured config.Config, store credential.Store, readPassword func() ([]byte, error)) error {
	if err := configured.Validate(); err != nil {
		return err
	}
	if !usesPassword(configured) || readPassword == nil {
		return config.Replace(paths.Config, configured)
	}
	if store == nil {
		return errors.New("credential store is required")
	}

	password, err := readPassword()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	defer credential.Clear(password)

	if err := store.Set(password); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	return config.Replace(paths.Config, configured)
}

// MigrateResult reports what Migrate imported.
type MigrateResult struct {
	ConfigurationMigrated bool
	CredentialMigrated    bool
}

// Migrate copies pre-release worktree state from legacyRoot into the user
// state directory. It is idempotent and never moves or deletes the source.
func Migrate(deps Deps, legacyRoot string) (MigrateResult, error) {
	paths, err := deps.Paths()
	if err != nil {
		return MigrateResult{}, fmt.Errorf("resolve user configuration: %w", err)
	}
	legacy, configMigrated, err := config.MigrateLegacyConfig(legacyRoot, paths)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("migrate configuration: %w", err)
	}
	store, err := deps.PasswordStore(PasswordLocation(paths, savedCredentialBackend(paths)))
	if err != nil {
		return MigrateResult{}, fmt.Errorf("prepare credential store: %w", err)
	}
	credentialMigrated, err := credential.MigrateFile(store, legacy.Credential)
	if err != nil {
		return MigrateResult{}, fmt.Errorf("migrate credential: %w", err)
	}
	return MigrateResult{ConfigurationMigrated: configMigrated, CredentialMigrated: credentialMigrated}, nil
}
