//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestInstallOwnedFileProtectsForeignContent verifies CDI setup and teardown do
// not overwrite or remove a file changed by another component.
func TestInstallOwnedFileProtectsForeignContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fake-nvidia.json")
	owned := []byte("owned\n")
	if err := installOwnedFile(path, owned, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(owned) {
		t.Fatalf("installed content = %q, %v", got, err)
	}
	if err := os.WriteFile(path, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedFile(path, owned); err == nil {
		t.Fatal("expected changed CDI spec removal to be refused")
	}
	if err := installOwnedFile(path, owned, 0o644); err == nil {
		t.Fatal("expected foreign CDI spec replacement to be refused")
	}
}

// TestMakeDeviceCoversNvidiaMajors verifies the Linux device encoding retains
// both the classic NVIDIA and mock UVM major numbers used by the installer.
func TestMakeDeviceCoversNvidiaMajors(t *testing.T) {
	for _, tc := range []struct {
		major uint32
		minor uint32
	}{
		{195, 0},
		{195, 255},
		{511, 1},
	} {
		dev := uint64(makeDevice(tc.major, tc.minor))
		major := uint32((dev >> 8) & 0xfff)
		minor := uint32(dev & 0xff)
		if major != tc.major || minor != tc.minor {
			t.Fatalf("makeDevice(%d,%d) decoded as %d,%d", tc.major, tc.minor, major, minor)
		}
	}
}
