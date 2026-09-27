//go:build windows

package owneronly

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Owned reports whether the current token user owns path.
func Owned(path string, _ fs.FileInfo) bool {
	user, err := currentUser()
	if err != nil {
		return false
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return false
	}
	return ownedBy(sd, user)
}

// Restricted returns an error when path's DACL is absent or null, or when an
// ACE grants access to anyone but the current user, SYSTEM, and
// Administrators. perm is the darwin and linux mode mask and is not consulted.
func Restricted(path string, _ fs.FileInfo, _ fs.FileMode) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read DACL: %w", err)
	}
	if sd == nil {
		return errors.New("no security descriptor")
	}
	return restrictedDACL(sd, user)
}

// Restrict makes the current user file's owner and replaces its DACL with a
// protected one granting the current user alone full access. perm is the
// darwin and linux mode and is not consulted.
func Restrict(file *os.File, _ fs.FileMode) error {
	user, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := privateDescriptor(user, "")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	// The handle os.File holds lacks WRITE_DAC and WRITE_OWNER, so the
	// descriptor is set by name.
	return windows.SetNamedSecurityInfo(file.Name(), windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		user, nil, dacl, nil)
}

// MkdirAll creates path and any missing parents. The final directory, when
// MkdirAll creates it, is owned by the current user and carries a protected
// DACL granting that user alone full access, inherited by its children.
func MkdirAll(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	user, err := currentUser()
	if err != nil {
		return err
	}
	sd, err := privateDescriptor(user, "OICI")
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes := windows.SecurityAttributes{SecurityDescriptor: sd}
	attributes.Length = uint32(unsafe.Sizeof(attributes))
	err = windows.CreateDirectory(name, &attributes)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if err != nil {
		return &fs.PathError{Op: "mkdir", Path: path, Err: err}
	}
	return nil
}

func currentUser() (*windows.SID, error) {
	token, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read current token user: %w", err)
	}
	return token.User.Sid, nil
}

// privateDescriptor owns the object as user and grants user alone full
// access through a protected DACL; inherit holds SDDL ACE inheritance flags.
func privateDescriptor(user *windows.SID, inherit string) (*windows.SECURITY_DESCRIPTOR, error) {
	sid := user.String()
	return windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;" + inherit + ";FA;;;" + sid + ")")
}

func ownedBy(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) bool {
	owner, _, err := sd.Owner()
	return err == nil && owner != nil && owner.Equals(user)
}

// trustedSIDs may hold access besides user. CREATOR OWNER and OWNER RIGHTS
// stand for the owner, which Owned requires to be user.
var trustedSIDs = []windows.WELL_KNOWN_SID_TYPE{
	windows.WinLocalSystemSid,
	windows.WinBuiltinAdministratorsSid,
	windows.WinCreatorOwnerSid,
	windows.WinCreatorOwnerRightsSid,
}

func restrictedDACL(sd *windows.SECURITY_DESCRIPTOR, user *windows.SID) error {
	dacl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) || (err == nil && dacl == nil) {
		return errors.New("missing or null DACL grants everyone full access")
	}
	if err != nil {
		return fmt.Errorf("read DACL: %w", err)
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			return fmt.Errorf("read ACE %d: %w", index, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			return fmt.Errorf("ACE %d has unsupported type %d", index, ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trusted(sid, user) {
			return fmt.Errorf("DACL grants access to %s", sid)
		}
	}
	return nil
}

func trusted(sid, user *windows.SID) bool {
	if sid.Equals(user) {
		return true
	}
	for _, kind := range trustedSIDs {
		if sid.IsWellKnown(kind) {
			return true
		}
	}
	return false
}
