package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/soundadam/soundconnect/internal/backend"
	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

// SetupRequest is a full profile. Setup writes exactly these settings; empty
// Server and Username are asked for through Deps.Interaction.
type SetupRequest struct {
	Backend           string
	Server            string
	Username          string
	AuthType          string
	LoginDomain       string
	SOCKSListen       string
	UpstreamProxy     string
	TLSInsecure       bool
	NativeTLSInsecure bool
	// PasswordSupplied means the password arrives without a prompt, as with
	// setup --password-stdin from the macOS app. On aTrust with no AuthType
	// that is an explicit request for shared-password authentication;
	// discovery still determines the tenant-specific login domain.
	PasswordSupplied bool
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
}

// Setup saves the configuration and, unless the profile uses aTrust OAuth,
// the long-lived password.
func Setup(ctx context.Context, deps Deps, request SetupRequest) (SetupResult, error) {
	backendName, err := backend.ParseName(request.Backend)
	if err != nil {
		return SetupResult{}, Usagef("select protocol backend: %w", err)
	}
	if backendName != backend.ATrust && (strings.TrimSpace(request.AuthType) != "" || strings.TrimSpace(request.LoginDomain) != "") {
		return SetupResult{}, Usagef("auth-type and login-domain are only available for the aTrust backend")
	}
	authType := request.AuthType
	if request.PasswordSupplied && backendName == backend.ATrust && strings.TrimSpace(authType) == "" {
		authType = ATrustPasswordAuthType
	}

	server := request.Server
	if strings.TrimSpace(server) == "" {
		server, err = deps.Interaction.Input(ctx, "Gateway", defaultServer(backendName), nil)
		if err != nil {
			return SetupResult{}, fmt.Errorf("read gateway: %w", err)
		}
	}
	username := request.Username
	if backendName == backend.EasyConnect && strings.TrimSpace(username) == "" {
		if username, err = askAccount(ctx, deps); err != nil {
			return SetupResult{}, err
		}
	}
	paths, err := deps.Paths()
	if err != nil {
		return SetupResult{}, fmt.Errorf("resolve local state: %w", err)
	}
	configured := config.Config{
		Backend:           backendName,
		Server:            server,
		Username:          username,
		SOCKSListen:       request.SOCKSListen,
		UpstreamProxy:     request.UpstreamProxy,
		TLSInsecure:       request.TLSInsecure,
		NativeTLSInsecure: request.NativeTLSInsecure,
	}
	if backendName == backend.ATrust {
		if err := chooseATrustSetupMethod(ctx, deps, &configured, authType, request.LoginDomain); err != nil {
			return SetupResult{}, err
		}
		if configured.AuthType == ATrustPasswordAuthType && strings.TrimSpace(configured.Username) == "" {
			if configured.Username, err = askAccount(ctx, deps); err != nil {
				return SetupResult{}, err
			}
		}
	}

	var passwordStore credential.Store
	if usesPassword(configured) {
		passwordStore, err = deps.PasswordStore(PasswordLocation(paths, configured.CredentialStore))
		if err != nil {
			return SetupResult{}, fmt.Errorf("prepare credential store: %w", err)
		}
	}
	readPassword := func() ([]byte, error) { return deps.Interaction.Password(ctx, "VPN password") }
	if err := save(paths, configured, passwordStore, readPassword); err != nil {
		return SetupResult{}, fmt.Errorf("setup failed: %w", err)
	}

	result := SetupResult{Path: paths.Config, Backend: backendName, Credential: CredentialSystemStore}
	if backendName == backend.ATrust {
		result.Credential = CredentialBrowserOAuth
		if configured.AuthType == ATrustPasswordAuthType {
			result.Credential = CredentialSystemStorePassword
		}
	}
	return result, nil
}

func askAccount(ctx context.Context, deps Deps) (string, error) {
	account, err := deps.Interaction.Input(ctx, "Account", "", nil)
	if err != nil {
		return "", fmt.Errorf("read account: %w", err)
	}
	return account, nil
}

// chooseATrustSetupMethod selects the aTrust authentication method from the
// gateway's advertised methods.
func chooseATrustSetupMethod(ctx context.Context, deps Deps, configured *config.Config, authType, loginDomain string) error {
	endpoint, err := ParseATrustEndpoint(configured.Server)
	if err != nil {
		return Usagef("parse aTrust gateway: %w", err)
	}
	methods, err := discoverATrust(ctx, deps, endpoint)
	if err != nil {
		return err
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

// usesPassword reports whether a profile authenticates with the shared
// long-lived password; aTrust OAuth keeps none.
func usesPassword(configured config.Config) bool {
	return configured.BackendName() != backend.ATrust || configured.AuthType == ATrustPasswordAuthType
}

// save stores the non-secret configuration and a long-lived password without
// accepting the password through argv or env.
func save(paths config.Paths, configured config.Config, store credential.Store, readPassword func() ([]byte, error)) error {
	if err := configured.Validate(); err != nil {
		return err
	}
	if !usesPassword(configured) {
		return config.Replace(paths.Config, configured)
	}
	if readPassword == nil {
		return errors.New("secret reader is required")
	}
	if store == nil {
		return errors.New("credential store is required")
	}

	password, err := readPassword()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	defer credential.Clear(password)

	if err := config.Replace(paths.Config, configured); err != nil {
		return err
	}
	if err := store.Set(password); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	return nil
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
