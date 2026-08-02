// Package setup stores initial non-secret configuration and a confirmed
// long-lived password without accepting the password through argv or env.
package setup

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/soundadam/soundconnect/internal/config"
	"github.com/soundadam/soundconnect/internal/credential"
)

var ErrPasswordMismatch = errors.New("password confirmation does not match")

type SecretReader func(prompt string) ([]byte, error)

func Save(paths config.Paths, configured config.Config, read SecretReader) error {
	if read == nil {
		return errors.New("secret reader is required")
	}
	if err := configured.Validate(); err != nil {
		return err
	}

	password, err := read("soundconnect password: ")
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	defer credential.Clear(password)
	confirmation, err := read("confirm soundconnect password: ")
	if err != nil {
		return fmt.Errorf("read password confirmation: %w", err)
	}
	defer credential.Clear(confirmation)
	if !bytes.Equal(password, confirmation) {
		return ErrPasswordMismatch
	}

	if err := config.Replace(paths.Config, configured); err != nil {
		return err
	}
	store, err := credential.NewFileStore(paths.Credential, true)
	if err != nil {
		return err
	}
	if err := store.Set(password); err != nil {
		return fmt.Errorf("store password: %w", err)
	}
	return nil
}
