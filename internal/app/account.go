package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
	"github.com/soundadam/soundconnect/internal/runtimecontrol"
)

// Saved-secret states reported by AccountShow.
const (
	SecretSaved       = "saved"
	SecretMissing     = "missing"
	SecretNotRequired = "not_required"
	SecretUnavailable = "unavailable"
)

// AccountInfo is the `account show --json` contract. It never contains a
// secret, only whether one is saved.
type AccountInfo struct {
	SchemaVersion   int    `json:"schema_version"`
	Configuration   string `json:"configuration"`
	Backend         string `json:"backend,omitempty"`
	Server          string `json:"server,omitempty"`
	Username        string `json:"username,omitempty"`
	AuthType        string `json:"auth_type,omitempty"`
	CredentialStore string `json:"credential_store"`
	Password        string `json:"password"`
	ATrustSession   string `json:"atrust_session"`
}

// AccountShow reports the saved account and which secrets are saved,
// without reading any secret.
func AccountShow(deps Deps) (AccountInfo, error) {
	paths, err := deps.Paths()
	if err != nil {
		return AccountInfo{}, fmt.Errorf("resolve local state: %w", err)
	}
	info := AccountInfo{SchemaVersion: 1, Configuration: "missing", CredentialStore: credential.BackendKeyring}
	configured, loadErr := config.Load(paths.Config)
	switch {
	case loadErr == nil:
		info.Configuration = "ready"
		info.Backend = string(configured.BackendName())
		info.Server = configured.Server
		info.Username = configured.Username
		info.AuthType = configured.AuthType
		if configured.CredentialStore != "" {
			info.CredentialStore = configured.CredentialStore
		}
	case !errors.Is(loadErr, os.ErrNotExist):
		info.Configuration = "invalid"
	}

	info.Password = secretState(deps.PasswordStore, PasswordLocation(paths, configured.CredentialStore))
	if loadErr == nil && !usesPassword(configured) && info.Password == SecretMissing {
		info.Password = SecretNotRequired
	}
	info.ATrustSession = secretState(deps.ATrustSessionStore, ATrustSessionLocation(paths, configured.CredentialStore))
	return info, nil
}

func secretState(open func(credential.Location) (credential.Store, error), location credential.Location) string {
	store, err := open(location)
	if err != nil {
		return SecretUnavailable
	}
	switch err := store.Inspect(); {
	case err == nil:
		return SecretSaved
	case errors.Is(err, os.ErrNotExist):
		return SecretMissing
	default:
		return SecretUnavailable
	}
}

// accountState resolves the paths, refuses while a runtime is active, and
// loads the configuration when there is one.
func accountState(deps Deps, operation string) (config.Paths, config.Config, bool, error) {
	paths, err := deps.Paths()
	if err != nil {
		return config.Paths{}, config.Config{}, false, fmt.Errorf("resolve local state: %w", err)
	}
	if err := runtimecontrol.EnsureNoActive(runtimecontrol.Path(paths.Root)); err != nil {
		return config.Paths{}, config.Config{}, false, fmt.Errorf("%s: %w", operation, err)
	}
	configured, err := config.Load(paths.Config)
	if errors.Is(err, os.ErrNotExist) {
		return paths, config.Config{}, false, nil
	}
	if err != nil {
		return config.Paths{}, config.Config{}, false, fmt.Errorf("load configuration: %w", err)
	}
	return paths, configured, true, nil
}

// SetPassword replaces the shared password. It works before setup too, so
// a script can store the password first. It refuses while a runtime is
// active.
func SetPassword(ctx context.Context, deps Deps) error {
	paths, configured, _, err := accountState(deps, "set password")
	if err != nil {
		return err
	}
	store, err := deps.PasswordStore(PasswordLocation(paths, configured.CredentialStore))
	if err != nil {
		return fmt.Errorf("prepare credential store: %w", err)
	}
	password, err := deps.Interaction.Password(ctx, "VPN password")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	defer credential.Clear(password)
	if err := store.Set(password); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	return nil
}

// SetUsername changes the saved account. A different account also forgets
// the saved aTrust session, which belongs to the old account. It reports
// whether a session was forgotten.
func SetUsername(deps Deps, username string) (sessionCleared bool, err error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return false, Usagef("username is required")
	}
	paths, configured, found, err := accountState(deps, "set username")
	if err != nil {
		return false, err
	}
	if !found {
		return false, errors.New(`no configuration to change; run "soundconnect setup" first`)
	}
	if configured.Username == username {
		return false, nil
	}
	configured.Username = username
	if err := configured.Validate(); err != nil {
		return false, Usagef("validate profile: %w", err)
	}
	if err := config.Replace(paths.Config, configured); err != nil {
		return false, fmt.Errorf("write profile: %w", err)
	}
	return forgetATrustSession(deps, paths, configured.CredentialStore)
}

// ForgetRequest selects the secrets Forget removes.
type ForgetRequest struct {
	Password bool
	Session  bool
}

// ForgetResult reports what Forget removed.
type ForgetResult struct {
	PasswordForgotten   bool
	SessionForgotten    bool
	OAuthProfileCleared bool
}

// Forget removes saved secrets. Forgetting the session also clears the aTrust
// OAuth helper's browser profile when the helper is available. It refuses
// while a runtime is active.
func Forget(ctx context.Context, deps Deps, request ForgetRequest) (ForgetResult, error) {
	if !request.Password && !request.Session {
		return ForgetResult{}, Usagef("choose --password, --session, or both")
	}
	paths, configured, _, err := accountState(deps, "forget credentials")
	if err != nil {
		return ForgetResult{}, err
	}
	var result ForgetResult
	if request.Password {
		store, err := deps.PasswordStore(PasswordLocation(paths, configured.CredentialStore))
		if err != nil {
			return result, fmt.Errorf("prepare credential store: %w", err)
		}
		clearable, ok := store.(credential.Clearable)
		if !ok {
			return result, errors.New("credential store does not support clearing")
		}
		result.PasswordForgotten = store.Inspect() == nil
		if err := clearable.Clear(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf("forget password: %w", err)
		}
	}
	if request.Session {
		if result.SessionForgotten, err = forgetATrustSession(deps, paths, configured.CredentialStore); err != nil {
			return result, err
		}
		if result.OAuthProfileCleared, err = clearOAuthProfile(ctx, deps); err != nil {
			return result, err
		}
	}
	return result, nil
}

// clearOAuthProfile asks the bundled OAuth helper to delete its browser
// profile. Without a helper there is no profile to clear.
func clearOAuthProfile(ctx context.Context, deps Deps) (bool, error) {
	helperPath, available := deps.OAuthHelper()
	if !available {
		return false, nil
	}
	clearContext, cancel := context.WithTimeout(ctx, atrustLogoutTimeout)
	defer cancel()
	command := exec.CommandContext(clearContext, helperPath, "--clear-data")
	command.Stdout = io.Discard
	command.Stderr = deps.diagnostics()
	if err := command.Run(); err != nil {
		if clearContext.Err() != nil {
			return false, errors.New("clear OAuth profile: timed out")
		}
		return false, fmt.Errorf("clear OAuth profile: %w", err)
	}
	return true, nil
}

// Account menu actions.
const (
	accountChangePassword = "password"
	accountChangeUsername = "username"
	accountChangeProfile  = "profile"
	accountForgetPassword = "forget-password"
	accountForgetSession  = "forget-session"
	accountDone           = "done"
)

// AccountMenu lets a terminal user review and change the saved account
// until they choose Done. summary is called before every choice to show the
// current state.
func AccountMenu(ctx context.Context, deps Deps, summary func(AccountInfo)) error {
	for {
		info, err := AccountShow(deps)
		if err != nil {
			return err
		}
		summary(info)
		options := []Option{
			{Value: accountChangePassword, Label: "Change the VPN password"},
			{Value: accountChangeUsername, Label: "Change the account name (forgets the aTrust session)"},
			{Value: accountChangeProfile, Label: "Change backend, gateway or sign-in method"},
		}
		if info.Password == SecretSaved {
			options = append(options, Option{Value: accountForgetPassword, Label: "Forget the saved password"})
		}
		if info.ATrustSession == SecretSaved {
			options = append(options, Option{Value: accountForgetSession, Label: "Forget the saved aTrust session"})
		}
		options = append(options, Option{Value: accountDone, Label: "Done"})
		choice, err := deps.Interaction.Select(ctx, "Account", options, accountDone)
		if err != nil {
			return err
		}
		switch choice {
		case accountDone:
			return nil
		case accountChangePassword:
			err = SetPassword(ctx, deps)
		case accountChangeUsername:
			var username string
			username, err = deps.Interaction.Input(ctx, "Account", info.Username, nil)
			if err == nil {
				_, err = SetUsername(deps, username)
			}
		case accountChangeProfile:
			_, err = Setup(ctx, deps, SetupRequest{Guided: true})
		case accountForgetPassword:
			_, err = Forget(ctx, deps, ForgetRequest{Password: true})
		case accountForgetSession:
			_, err = Forget(ctx, deps, ForgetRequest{Session: true})
		}
		if err != nil {
			return err
		}
	}
}
