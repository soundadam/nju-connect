//go:build windows

package owneronly

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestMkdirAllAndRestrictCreateOwnerOnlyPaths(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "parent", "private")
	if err := MkdirAll(directory); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	assertOwnerOnly(t, directory)

	file, err := os.CreateTemp(directory, "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := Restrict(file, 0600); err != nil {
		t.Fatalf("Restrict() error = %v", err)
	}
	assertOwnerOnly(t, file.Name())

	if err := MkdirAll(directory); err != nil {
		t.Fatalf("MkdirAll() on an existing directory error = %v", err)
	}
}

func TestRestrictedDACLAllowsOnlyUserSystemAndAdministrators(t *testing.T) {
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	u := user.String()
	for _, test := range []struct {
		name string
		sddl string
		ok   bool
	}{
		{"user alone", "D:P(A;;FA;;;" + u + ")", true},
		{"user, SYSTEM, Administrators", "D:P(A;OICI;FA;;;" + u + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)", true},
		{"deny to everyone", "D:P(A;;FA;;;" + u + ")(D;;FA;;;WD)", true},
		{"creator owner inherit-only", "D:P(A;;FA;;;" + u + ")(A;OICIIO;FA;;;CO)", true},
		{"read to everyone", "D:P(A;;FA;;;" + u + ")(A;;FR;;;WD)", false},
		{"read to users", "D:P(A;;FA;;;" + u + ")(A;;FR;;;BU)", false},
		{"null DACL", "D:NO_ACCESS_CONTROL", false},
		{"no DACL", "O:" + u, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(test.sddl)
			if err != nil {
				t.Fatal(err)
			}
			if err := restrictedDACL(sd, user); (err == nil) != test.ok {
				t.Fatalf("restrictedDACL(%q) error = %v, want ok = %v", test.sddl, err, test.ok)
			}
		})
	}
}

func TestOwnedByRequiresTheTokenUser(t *testing.T) {
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		owner string
		want  bool
	}{
		{user.String(), true},
		{"SY", false},
		{"BA", false},
	} {
		sd, err := windows.SecurityDescriptorFromString("O:" + test.owner)
		if err != nil {
			t.Fatal(err)
		}
		if got := ownedBy(sd, user); got != test.want {
			t.Errorf("ownedBy(O:%s) = %v, want %v", test.owner, got, test.want)
		}
	}
}

func TestRestrictedReadsThePathDACL(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "private")
	if err := MkdirAll(directory); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "shared")
	if err := os.WriteFile(path, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	user, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Restricted(path, info, 0600); err == nil {
		t.Fatal("Restricted() accepted a DACL granting Everyone read access")
	}
}

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !Owned(path, info) {
		t.Errorf("Owned(%s) = false", path)
	}
	if err := Restricted(path, info, 0600); err != nil {
		t.Errorf("Restricted(%s) error = %v", path, err)
	}
}
