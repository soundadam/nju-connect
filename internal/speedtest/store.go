package speedtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	Path string
}

func LastResultPath(root string) string {
	return filepath.Join(root, "speedtest", "last-v1.json")
}

func (store Store) Save(result Result) error {
	if err := result.Validate(); err != nil {
		return err
	}
	directory := filepath.Dir(store.Path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create speed-test result directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("speed-test result directory is unsafe")
	}
	if err := validateComponentOwner(info); err != nil {
		return fmt.Errorf("validate speed-test result directory owner: %w", err)
	}
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode speed-test result: %w", err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(directory, ".last-v1-*")
	if err != nil {
		return fmt.Errorf("create temporary speed-test result: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(payload)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write speed-test result: %w", err)
	}
	if err := os.Rename(temporaryPath, store.Path); err != nil {
		return fmt.Errorf("replace speed-test result: %w", err)
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync speed-test result directory: %w", err)
	}
	return nil
}

func (store Store) Load() (Result, error) {
	info, err := os.Lstat(store.Path)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return Result{}, errors.New("speed-test result file is unsafe")
	}
	if err := validateComponentOwner(info); err != nil {
		return Result{}, fmt.Errorf("validate speed-test result owner: %w", err)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil {
		return Result{}, err
	}
	if len(data) > 64<<10 {
		return Result{}, errors.New("speed-test result is too large")
	}
	var result Result
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode speed-test result: %w", err)
	}
	// Releases named soundconnect saved the tunnel route under that name.
	if result.Route == legacyRouteSoundconnect {
		result.Route = RouteNJUConnect
	}
	if err := result.Validate(); err != nil {
		return Result{}, err
	}
	return result, nil
}
