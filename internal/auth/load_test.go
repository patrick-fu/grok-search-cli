package auth

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

func TestLoaderRejectsFileIdentityRace(t *testing.T) {
	directory := t.TempDir()
	beforePath := writeSecureTestFile(t, directory, "before.json")
	afterPath := writeSecureTestFile(t, directory, "after.json")
	beforeInfo, err := os.Lstat(beforePath)
	if err != nil {
		t.Fatal(err)
	}

	loader := Loader{files: mismatchedFileSystem{before: beforeInfo, openedPath: afterPath}}
	_, loadErr := loader.Load(filepath.Join(directory, "selected.json"), time.Now())
	if loadErr == nil || loadErr.Code != search.ErrorAuthInsecure {
		t.Fatalf("error = %#v, want auth_insecure", loadErr)
	}
}

func TestLoaderRejectsWrongOwnerWhenTestCanCreateOne(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing file ownership requires root")
	}
	directory := t.TempDir()
	path := writeSecureTestFile(t, directory, "auth.json")
	if err := os.Chown(path, 1, -1); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chown(path, 0, -1) })

	_, loadErr := NewLoader().Load(path, time.Now())
	if loadErr == nil || loadErr.Code != search.ErrorAuthInsecure {
		t.Fatalf("error = %#v, want auth_insecure", loadErr)
	}
}

func TestLoaderRejectsWrongEffectiveOwnerMetadata(t *testing.T) {
	directory := t.TempDir()
	path := writeSecureTestFile(t, directory, "auth.json")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("file metadata does not expose syscall.Stat_t")
	}
	wrongOwner := *stat
	wrongOwner.Uid = uint32(os.Geteuid()) + 1

	loader := Loader{files: ownerMismatchFileSystem{
		info: ownerMismatchInfo{FileInfo: info, stat: &wrongOwner},
	}}
	_, loadErr := loader.Load(path, time.Now())
	if loadErr == nil || loadErr.Code != search.ErrorAuthInsecure {
		t.Fatalf("error = %#v, want auth_insecure", loadErr)
	}
}

type mismatchedFileSystem struct {
	before     os.FileInfo
	openedPath string
}

type ownerMismatchFileSystem struct {
	info os.FileInfo
}

func (files ownerMismatchFileSystem) Lstat(string) (os.FileInfo, error) {
	return files.info, nil
}

func (ownerMismatchFileSystem) OpenReadOnlyNoFollow(string) (*os.File, error) {
	return nil, errors.New("must not open a file with the wrong owner")
}

type ownerMismatchInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (info ownerMismatchInfo) Sys() any {
	return info.stat
}

func (files mismatchedFileSystem) Lstat(string) (os.FileInfo, error) {
	return files.before, nil
}

func (files mismatchedFileSystem) OpenReadOnlyNoFollow(string) (*os.File, error) {
	return os.Open(files.openedPath)
}

func writeSecureTestFile(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
