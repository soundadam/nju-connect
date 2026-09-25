package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/soundadam/soundconnect/internal/backend"
)

const (
	DefaultServer       = backend.DefaultEasyConnectGateway
	DefaultATrustServer = backend.DefaultATrustGateway
	// DefaultSOCKSListen is shared by every protocol backend.
	DefaultSOCKSListen = "127.0.0.1:1081"
	maxConfigBytes     = 1 << 20
)

// Config contains only non-secret connection settings.
type Config struct {
	Backend       backend.Name `toml:"backend"`
	Server        string       `toml:"server"`
	Username      string       `toml:"username"`
	SOCKSListen   string       `toml:"socks_listen"`
	UpstreamProxy string       `toml:"upstream_proxy"`
	TLSInsecure   bool         `toml:"tls_insecure"`
	// NativeTLSInsecure applies only to the legacy native L3 protocol TLS.
	// It must not weaken the HTTPS channel carrying credentials and MFA.
	NativeTLSInsecure bool   `toml:"native_tls_insecure"`
	AuthType          string `toml:"auth_type,omitempty"`
	LoginDomain       string `toml:"login_domain,omitempty"`
}

func Default() Config {
	return Config{Backend: backend.EasyConnect, Server: DefaultServer, SOCKSListen: DefaultSOCKSListen}
}

func (configured Config) Validate() error {
	backendName := configured.BackendName()
	if _, err := backend.ParseName(string(backendName)); err != nil {
		return err
	}
	if backendName == backend.EasyConnect && strings.TrimSpace(configured.Username) == "" {
		return errors.New("username is required")
	}
	if err := validateServer(configured.Server); err != nil {
		return err
	}
	if err := validateLoopback(configured.SOCKSListen); err != nil {
		return err
	}
	if backendName == backend.ATrust {
		if strings.TrimSpace(configured.AuthType) == "" {
			return errors.New("aTrust authentication type is required")
		}
		if configured.AuthType == "auth/psw" && strings.TrimSpace(configured.Username) == "" {
			return errors.New("username is required for aTrust password authentication")
		}
	}
	return validateProxy(configured.UpstreamProxy)
}

func (configured Config) BackendName() backend.Name {
	if configured.Backend == "" {
		return backend.EasyConnect
	}
	return configured.Backend
}

func Parse(data []byte) (Config, error) {
	configured := Default()
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return Config{}, fmt.Errorf("decode config TOML: %w", err)
	}
	if err := configured.Validate(); err != nil {
		return Config{}, err
	}
	return configured, nil
}

func Load(path string) (Config, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Config{}, fmt.Errorf("inspect config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Config{}, errors.New("config path must be a regular file")
	}
	if info.Mode().Perm()&0077 != 0 {
		return Config{}, errors.New("config permissions must be 0600 or stricter")
	}
	if !ownedByCurrentUser(info) {
		return Config{}, errors.New("config must be owned by the current user")
	}

	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("inspect opened config: %w", err)
	}
	if !os.SameFile(info, openedInfo) {
		return Config{}, errors.New("config changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if len(data) > maxConfigBytes {
		return Config{}, errors.New("config exceeds size limit")
	}
	return Parse(data)
}

// Replace atomically stores validated non-secret configuration in an
// owner-only regular file.
func Replace(path string, configured Config) error {
	if err := configured.Validate(); err != nil {
		return err
	}
	data, err := toml.Marshal(configured)
	if err != nil {
		return fmt.Errorf("encode config TOML: %w", err)
	}

	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return err
	}
	if info, inspectErr := os.Lstat(path); inspectErr == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) {
			return errors.New("existing config must be an owner-only regular file")
		}
	} else if !errors.Is(inspectErr, os.ErrNotExist) {
		return fmt.Errorf("inspect config: %w", inspectErr)
	}

	temporary, err := os.CreateTemp(directory, ".soundconnect-config-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if err := writeAll(temporary, data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary config: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary config: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary config: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	removeTemporary = false
	return syncDirectory(directory)
}

func writeAll(writer io.Writer, value []byte) error {
	for len(value) > 0 {
		written, err := writer.Write(value)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		value = value[written:]
	}
	return nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect config directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || !ownedByCurrentUser(info) {
		return errors.New("config directory must be current-user-owned and 0700 or stricter")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validateServer(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("server is required")
	}
	if strings.Contains(value, "://") {
		return errors.New("server must be a hostname or host:port, not a URL")
	}
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	} else if strings.Contains(value, ":") && net.ParseIP(value) == nil {
		return errors.New("server must be a hostname or host:port")
	}
	if strings.Trim(host, "[]") == "" {
		return errors.New("server host is required")
	}
	return nil
}

func validateLoopback(value string) error {
	host, port, err := net.SplitHostPort(value)
	if err != nil || port == "" {
		return errors.New("socks_listen must be numeric loopback host:port")
	}
	address := net.ParseIP(host)
	if address == nil || !address.IsLoopback() {
		return errors.New("socks_listen must use a numeric loopback address")
	}
	return nil
}

func validateProxy(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
		return errors.New("upstream_proxy must use socks5 or socks5h")
	}
	if parsed.User != nil {
		return errors.New("upstream_proxy credentials are unsupported")
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return errors.New("upstream_proxy must include host and port")
	}
	return nil
}
