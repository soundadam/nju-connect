// Package setup stores initial non-secret configuration and a long-lived
// password without accepting the password through argv or env.
package setup

import (
	"errors"
	"fmt"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

type SecretReader func(prompt string) ([]byte, error)

func Save(paths config.Paths, configured config.Config, store credential.Store, read SecretReader) error {
	if read == nil {
		return errors.New("secret reader is required")
	}
	if store == nil {
		return errors.New("credential store is required")
	}
	if err := configured.Validate(); err != nil {
		return err
	}

	password, err := read("VPN password: ")
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
