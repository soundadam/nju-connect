package speedtest

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const maximumComponentSize int64 = 64 << 20

var ErrComponentMissing = errors.New("campus speed-test component is not installed")

type ComponentAsset struct {
	Version       string `json:"version"`
	HelperVersion string `json:"helper_version"`
	OS            string `json:"os"`
	Architecture  string `json:"architecture"`
	URL           string `json:"url"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

type ComponentStatus struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version"`
	HelperVersion string `json:"helper_version"`
	Architecture  string `json:"architecture"`
	Path          string `json:"path"`
	DownloadSize  int64  `json:"download_size"`
	DownloadReady bool   `json:"download_ready"`
	InstallSource string `json:"install_source,omitempty"`
}

type ComponentManager struct {
	Root         string
	Asset        ComponentAsset
	ExternalPath string
	Client       *http.Client
}

func DefaultComponentAsset() ComponentAsset {
	arch := runtime.GOARCH
	var catalog struct {
		ComponentVersion string `json:"component_version"`
		HelperVersion    string `json:"helper_version"`
		Assets           map[string]struct {
			URL    string `json:"url"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"assets"`
	}
	_ = json.Unmarshal(componentCatalogJSON, &catalog)
	assetMetadata := catalog.Assets[arch]
	return ComponentAsset{
		Version:       catalog.ComponentVersion,
		HelperVersion: catalog.HelperVersion,
		OS:            "darwin",
		Architecture:  arch,
		URL:           assetMetadata.URL,
		Size:          assetMetadata.Size,
		SHA256:        assetMetadata.SHA256,
	}
}

//go:embed component_catalog.json
var componentCatalogJSON []byte

func ComponentRoot(root string) string {
	return filepath.Join(root, "components", "campus-speed")
}

func (manager ComponentManager) Path() string {
	return filepath.Join(manager.Root, manager.Asset.Version, manager.Asset.Architecture, HelperName)
}

func (manager ComponentManager) ExecutablePath() string {
	if manager.externalReady() == nil {
		return manager.ExternalPath
	}
	return manager.Path()
}

func (manager ComponentManager) Status() ComponentStatus {
	status := ComponentStatus{
		Version: manager.Asset.Version, HelperVersion: manager.Asset.HelperVersion,
		Architecture: manager.Asset.Architecture, Path: manager.ExecutablePath(), DownloadSize: manager.Asset.Size,
		DownloadReady: manager.installSourceReady() == nil, InstallSource: manager.installSource(),
	}
	status.Installed = manager.Validate() == nil
	return status
}

func (manager ComponentManager) Validate() error {
	if manager.externalReady() == nil {
		return nil
	}
	if err := manager.validateIdentity(); err != nil {
		return err
	}
	info, err := os.Lstat(manager.Path())
	if errors.Is(err, os.ErrNotExist) {
		return ErrComponentMissing
	}
	if err != nil {
		return fmt.Errorf("inspect campus speed-test component: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("campus speed-test component has unsafe file properties")
	}
	if err := validateComponentOwner(info); err != nil {
		return err
	}
	if manager.Asset.Size > 0 && info.Size() != manager.Asset.Size {
		return errors.New("campus speed-test component size does not match the catalog")
	}
	file, err := os.Open(manager.Path())
	if err != nil {
		return fmt.Errorf("open campus speed-test component: %w", err)
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, io.LimitReader(file, maximumComponentSize+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("hash campus speed-test component: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close campus speed-test component: %w", closeErr)
	}
	if !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), manager.Asset.SHA256) {
		return errors.New("campus speed-test component checksum does not match the catalog")
	}
	return nil
}

func (manager ComponentManager) Install(ctx context.Context, sink ProgressSink) error {
	if manager.Validate() == nil {
		return nil
	}
	if err := manager.installSourceReady(); err != nil {
		return err
	}
	directory := filepath.Dir(manager.Path())
	if err := ensurePrivateComponentPath(manager.Root, manager.Asset.Version, manager.Asset.Architecture); err != nil {
		return err
	}
	reader, closeReader, phase, err := manager.openInstallSource(ctx)
	if err != nil {
		return err
	}
	defer closeReader()
	temporary, err := os.CreateTemp(directory, ".librespeed-cli-*")
	if err != nil {
		return fmt.Errorf("create temporary campus speed-test component: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	digest := sha256.New()
	progressReader := &componentProgressReader{
		Reader: io.LimitReader(reader, manager.Asset.Size+1), Total: manager.Asset.Size, Sink: sink, Phase: phase,
	}
	written, copyErr := io.Copy(io.MultiWriter(temporary, digest), progressReader)
	if copyErr == nil && written != manager.Asset.Size {
		copyErr = fmt.Errorf("downloaded %d bytes, expected %d", written, manager.Asset.Size)
	}
	if copyErr == nil && !strings.EqualFold(hex.EncodeToString(digest.Sum(nil)), manager.Asset.SHA256) {
		copyErr = errors.New("downloaded component checksum does not match the catalog")
	}
	if copyErr == nil {
		copyErr = temporary.Chmod(0o700)
	}
	if copyErr == nil {
		copyErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("install campus speed-test component: %w", copyErr)
	}
	if err := os.Rename(temporaryPath, manager.Path()); err != nil {
		return fmt.Errorf("publish campus speed-test component: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		_ = os.Remove(manager.Path())
		return fmt.Errorf("sync campus speed-test component directory: %w", err)
	}
	if err := manager.Validate(); err != nil {
		_ = os.Remove(manager.Path())
		return err
	}
	report(sink, Event{Type: "component_progress", Phase: "complete", Progress: floatPointer(1)})
	return nil
}

func (manager ComponentManager) validateIdentity() error {
	if manager.Root == "" || manager.Asset.Version == "" || manager.Asset.Architecture == "" {
		return errors.New("campus speed-test component catalog is incomplete")
	}
	if manager.Asset.OS != runtime.GOOS || manager.Asset.Architecture != runtime.GOARCH {
		return errors.New("campus speed-test component does not match this platform")
	}
	if manager.Asset.HelperVersion != HelperVersion {
		return errors.New("campus speed-test helper version is unsupported")
	}
	if filepath.Base(manager.Asset.Version) != manager.Asset.Version || filepath.Base(manager.Asset.Architecture) != manager.Asset.Architecture {
		return errors.New("campus speed-test component identity contains an unsafe path")
	}
	return nil
}

func ensurePrivateComponentPath(root, version, architecture string) error {
	base := filepath.Dir(root)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fmt.Errorf("create campus speed-test component parent directory: %w", err)
	}
	paths := []string{base, root, filepath.Join(root, version), filepath.Join(root, version, architecture)}
	for _, path := range paths {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("create campus speed-test component directory: %w", err)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() {
			return errors.New("campus speed-test component directory is unsafe")
		}
		if err := validateComponentOwner(info); err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			if err := os.Chmod(path, 0o700); err != nil {
				return errors.New("protect campus speed-test component directory")
			}
		}
	}
	return nil
}

func (manager ComponentManager) assetReady() error {
	if err := manager.validateIdentity(); err != nil {
		return err
	}
	parsed, err := url.Parse(manager.Asset.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("campus speed-test component URL is invalid")
	}
	if manager.Asset.Size <= 0 || manager.Asset.Size > maximumComponentSize {
		return errors.New("campus speed-test component has not been published")
	}
	digest, err := hex.DecodeString(manager.Asset.SHA256)
	if err != nil || len(digest) != sha256.Size || strings.Trim(manager.Asset.SHA256, "0") == "" {
		return errors.New("campus speed-test component checksum is unavailable")
	}
	return nil
}

func (manager ComponentManager) installSource() string {
	if manager.externalReady() == nil {
		return "homebrew"
	}
	if manager.assetReady() == nil {
		return "download"
	}
	return ""
}

func (manager ComponentManager) installSourceReady() error {
	if manager.externalReady() == nil {
		return nil
	}
	return manager.assetReady()
}

func (manager ComponentManager) externalReady() error {
	if manager.ExternalPath == "" || !filepath.IsAbs(manager.ExternalPath) {
		return errors.New("external campus speed-test helper is unavailable")
	}
	info, err := os.Lstat(manager.ExternalPath)
	if err != nil {
		return fmt.Errorf("inspect external campus speed-test helper: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("external campus speed-test helper has unsafe file properties")
	}
	return nil
}

func (manager ComponentManager) openInstallSource(ctx context.Context) (io.Reader, func(), string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manager.Asset.URL, nil)
	if err != nil {
		return nil, func() {}, "", fmt.Errorf("prepare campus speed-test component download: %w", err)
	}
	client := manager.Client
	if client == nil {
		client = &http.Client{}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, func() {}, "", fmt.Errorf("download campus speed-test component: %w", err)
	}
	closeResponse := func() { _ = response.Body.Close() }
	if response.Request == nil || response.Request.URL == nil || response.Request.URL.Scheme != "https" {
		closeResponse()
		return nil, func() {}, "", errors.New("campus speed-test component download left HTTPS")
	}
	if response.StatusCode != http.StatusOK {
		closeResponse()
		return nil, func() {}, "", fmt.Errorf("download campus speed-test component: HTTP %d", response.StatusCode)
	}
	if response.ContentLength > manager.Asset.Size || response.ContentLength > maximumComponentSize {
		closeResponse()
		return nil, func() {}, "", errors.New("campus speed-test component download is larger than expected")
	}
	return response.Body, closeResponse, "downloading", nil
}

type contextReader struct {
	Context context.Context
	Reader  io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.Context.Done():
		return 0, reader.Context.Err()
	default:
		return reader.Reader.Read(buffer)
	}
}

type componentProgressReader struct {
	io.Reader
	Total     int64
	BytesRead int64
	Sink      ProgressSink
	Phase     string
}

func (reader *componentProgressReader) Read(buffer []byte) (int, error) {
	n, err := reader.Reader.Read(buffer)
	reader.BytesRead += int64(n)
	if n > 0 && reader.Total > 0 {
		progress := float64(reader.BytesRead) / float64(reader.Total)
		if progress > 1 {
			progress = 1
		}
		report(reader.Sink, Event{Type: "component_progress", Phase: reader.Phase, Progress: &progress})
	}
	return n, err
}

func floatPointer(value float64) *float64 { return &value }
