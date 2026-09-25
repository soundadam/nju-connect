package credential

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// FileStore keeps the credential in a separate plaintext file. Construction
// requires an explicit opt-in, and every operation enforces regular-file and
// owner-only permission requirements.
type FileStore struct {
	path string
}

// NewFileStore creates a plaintext file backend only when allowPlaintext is
// explicitly true.
func NewFileStore(path string, allowPlaintext bool) (*FileStore, error) {
	if !allowPlaintext {
		return nil, ErrPlaintextOptIn
	}
	if path == "" {
		return nil, errors.New("credential file path is empty")
	}
	return &FileStore{path: filepath.Clean(path)}, nil
}

// Path returns the non-secret path configured for this backend.
func (s *FileStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Inspect verifies the file's type, ownership, mode, and bounded non-empty
// size without reading the credential bytes.
func (s *FileStore) Inspect() error {
	if s == nil || s.path == "" {
		return errors.New("credential file backend is not initialized")
	}
	info, err := os.Lstat(s.path)
	if err != nil {
		return fmt.Errorf("inspect credential file: %w", err)
	}
	if err := validateFileInfo(info); err != nil {
		return err
	}
	if info.Size() == 0 {
		return ErrEmptyCredential
	}
	if info.Size() > maxCredentialBytes {
		return ErrCredentialTooLarge
	}
	return nil
}

// Get reads a credential only from an owner-only regular file. Symbolic links
// and files with any permission bit outside 0600 are rejected.
func (s *FileStore) Get() ([]byte, error) {
	if s == nil || s.path == "" {
		return nil, errors.New("credential file backend is not initialized")
	}

	pathInfo, err := os.Lstat(s.path)
	if err != nil {
		return nil, fmt.Errorf("inspect credential file: %w", err)
	}
	if err := validateFileInfo(pathInfo); err != nil {
		return nil, err
	}

	file, err := os.Open(s.path)
	if err != nil {
		return nil, fmt.Errorf("open credential file: %w", err)
	}
	defer file.Close()

	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened credential file: %w", err)
	}
	if err := validateFileInfo(openedInfo); err != nil {
		return nil, err
	}
	if !os.SameFile(pathInfo, openedInfo) {
		return nil, errors.New("credential file changed while opening")
	}

	secret, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read credential file: %w", err)
	}
	if err := validateSecret(secret); err != nil {
		clearBytes(secret)
		return nil, err
	}
	return secret, nil
}

// Set atomically replaces the credential file with a newly created 0600
// regular file in the same directory.
func (s *FileStore) Set(secret []byte) error {
	if s == nil || s.path == "" {
		return errors.New("credential file backend is not initialized")
	}
	if err := validateSecret(secret); err != nil {
		return err
	}

	if info, err := os.Lstat(s.path); err == nil {
		if err := validateFileInfo(info); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect credential file: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	directoryInfo, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect credential directory: %w", err)
	}
	if !directoryInfo.IsDir() || directoryInfo.Mode().Perm()&0077 != 0 {
		return ErrInsecureDirectory
	}
	if !ownedByCurrentUser(directoryInfo) {
		return ErrWrongOwner
	}

	temporary, err := os.CreateTemp(dir, ".soundconnect-credential-*")
	if err != nil {
		return fmt.Errorf("create temporary credential file: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	fail := func(operation string, operationErr error) error {
		_ = temporary.Close()
		return fmt.Errorf("%s credential file: %w", operation, operationErr)
	}
	if err := temporary.Chmod(0600); err != nil {
		return fail("secure temporary", err)
	}
	if err := writeAll(temporary, secret); err != nil {
		return fail("write temporary", err)
	}
	if err := temporary.Sync(); err != nil {
		return fail("sync temporary", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary credential file: %w", err)
	}
	if err := os.Rename(temporaryPath, s.path); err != nil {
		return fmt.Errorf("atomically replace credential file: %w", err)
	}
	removeTemporary = false
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("sync credential directory: %w", err)
	}
	return nil
}

// Clear removes the owner-only credential file. It treats an already absent
// file as success and rechecks the inode before removal to avoid deleting a
// replacement path after the initial inspection.
func (s *FileStore) Clear() error {
	if s == nil || s.path == "" {
		return errors.New("credential file backend is not initialized")
	}
	info, err := os.Lstat(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect credential file: %w", err)
	}
	if err := validateFileInfo(info); err != nil {
		return err
	}
	current, err := os.Lstat(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reinspect credential file: %w", err)
	}
	if !os.SameFile(info, current) {
		return errors.New("credential file changed while clearing")
	}
	if err := os.Remove(s.path); err != nil {
		return fmt.Errorf("remove credential file: %w", err)
	}
	if err := syncDirectory(filepath.Dir(s.path)); err != nil {
		return fmt.Errorf("sync credential directory: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path) // #nosec G304 -- validated private directory is intentionally synchronized.
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validateFileInfo(info fs.FileInfo) error {
	if !info.Mode().IsRegular() {
		return ErrNotRegular
	}
	if !ownedByCurrentUser(info) {
		return ErrWrongOwner
	}
	if info.Mode().Perm()&^fs.FileMode(0600) != 0 {
		return fmt.Errorf("%w: mode %04o", ErrInsecurePermissions, info.Mode().Perm())
	}
	return nil
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
