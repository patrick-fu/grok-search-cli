package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyReplacesVerifiedBinary(t *testing.T) {
	target := writeCurrentBinary(t, "old binary")
	fixture := newFixture(t, "1.1.0", "linux", "amd64", []byte("new binary"))
	verified := 0
	result, err := Apply(context.Background(), Config{
		CurrentVersion: "1.0.0",
		ExecutablePath: target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		BaseURL:        "https://fixture.invalid/releases/latest/download",
		Fetch:          fixture.fetch,
		VerifyBinary: func(_ context.Context, path, version string) error {
			verified++
			contents, readErr := os.ReadFile(path)
			if readErr != nil || string(contents) != "new binary" || version != "1.1.0" {
				return errors.New("unexpected binary")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.CurrentVersion != "1.0.0" || result.LatestVersion != "1.1.0" || !result.Updated {
		t.Fatalf("result = %+v", result)
	}
	if verified != 2 {
		t.Fatalf("verification calls = %d, want 2", verified)
	}
	assertFileContents(t, target, "new binary")
}

func TestApplyAlreadyCurrentDownloadsOnlyManifest(t *testing.T) {
	target := writeCurrentBinary(t, "old binary")
	fixture := newFixture(t, "1.0.0", "linux", "amd64", []byte("new binary"))
	result, err := Apply(context.Background(), Config{
		CurrentVersion: "1.0.0",
		ExecutablePath: target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		BaseURL:        "https://fixture.invalid/releases/latest/download",
		Fetch:          fixture.fetch,
	})
	if err != nil || result.Updated || fixture.fetches != 1 {
		t.Fatalf("result=%+v err=%v fetches=%d", result, err, fixture.fetches)
	}
	assertFileContents(t, target, "old binary")
}

func TestApplyRejectsTamperedArchiveBeforeReplacement(t *testing.T) {
	target := writeCurrentBinary(t, "old binary")
	fixture := newFixture(t, "1.1.0", "linux", "amd64", []byte("new binary"))
	fixture.files[fixture.archiveURL] = append(fixture.files[fixture.archiveURL], 'x')
	_, err := Apply(context.Background(), Config{
		CurrentVersion: "1.0.0",
		ExecutablePath: target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		BaseURL:        fixture.baseURL,
		Fetch:          fixture.fetch,
	})
	if err == nil {
		t.Fatal("tampered archive unexpectedly installed")
	}
	assertFileContents(t, target, "old binary")
}

func TestApplyRollsBackWhenPostReplacementCheckFails(t *testing.T) {
	target := writeCurrentBinary(t, "old binary")
	fixture := newFixture(t, "1.1.0", "linux", "amd64", []byte("new binary"))
	checks := 0
	_, err := Apply(context.Background(), Config{
		CurrentVersion: "1.0.0",
		ExecutablePath: target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		BaseURL:        fixture.baseURL,
		Fetch:          fixture.fetch,
		VerifyBinary: func(context.Context, string, string) error {
			checks++
			if checks == 2 {
				return errors.New("post-check failed")
			}
			return nil
		},
	})
	if err == nil || checks != 2 {
		t.Fatalf("err=%v checks=%d", err, checks)
	}
	assertFileContents(t, target, "old binary")
}

func TestApplyRejectsSymlinkTarget(t *testing.T) {
	directory := t.TempDir()
	realTarget := filepath.Join(directory, "real")
	if err := os.WriteFile(realTarget, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, binaryName)
	if err := os.Symlink(realTarget, target); err != nil {
		t.Fatal(err)
	}
	fixture := newFixture(t, "1.1.0", "linux", "amd64", []byte("new binary"))
	_, err := Apply(context.Background(), Config{
		CurrentVersion: "1.0.0",
		ExecutablePath: target,
		GOOS:           "linux",
		GOARCH:         "amd64",
		BaseURL:        fixture.baseURL,
		Fetch:          fixture.fetch,
	})
	if err == nil {
		t.Fatal("symlink target unexpectedly installed")
	}
	assertFileContents(t, realTarget, "old binary")
}

func TestApplyRejectsDevelopmentVersionWithoutNetwork(t *testing.T) {
	fetched := false
	_, err := Apply(context.Background(), Config{
		CurrentVersion: "dev",
		ExecutablePath: writeCurrentBinary(t, "old binary"),
		GOOS:           "linux",
		GOARCH:         "amd64",
		Fetch: func(context.Context, string, int64) ([]byte, error) {
			fetched = true
			return nil, nil
		},
	})
	if err == nil || fetched {
		t.Fatalf("err=%v fetched=%t", err, fetched)
	}
}

func TestParseSemVerPrecedence(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-beta", "1.0.0", "1.0.1"}
	for index := 0; index+1 < len(ordered); index++ {
		left, leftErr := parseSemVer(ordered[index])
		right, rightErr := parseSemVer(ordered[index+1])
		if leftErr != nil || rightErr != nil || compareSemVer(left, right) >= 0 {
			t.Fatalf("%q must precede %q: leftErr=%v rightErr=%v", ordered[index], ordered[index+1], leftErr, rightErr)
		}
	}
	for _, invalid := range []string{"dev", "v1.2.3", "1.2", "01.2.3", "1.2.3-", "1.2.3+", "1.2.3-01"} {
		if _, err := parseSemVer(invalid); err == nil {
			t.Errorf("parseSemVer(%q) unexpectedly succeeded", invalid)
		}
	}
}

type fixture struct {
	baseURL    string
	exactBase  string
	archiveURL string
	files      map[string][]byte
	fetches    int
}

func newFixture(t *testing.T, version, goos, goarch string, binary []byte) *fixture {
	t.Helper()
	baseURL := "https://fixture.invalid/releases/latest/download"
	exactBase := "https://fixture.invalid/releases/download/v" + version
	archiveName := fmt.Sprintf("grok-search_%s_%s_%s.tar.gz", version, goos, goarch)
	archive := archiveFixture(t, version, goos, goarch, binary)
	digest := sha256.Sum256(archive)
	digestText := hex.EncodeToString(digest[:])
	manifest := releaseManifest{
		SchemaVersion: 1,
		Version:       version,
		Assets:        []manifestAsset{{Name: archiveName, SHA256: digestText, OS: goos, Arch: goarch}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	archiveURL := exactBase + "/" + archiveName
	return &fixture{
		baseURL:    baseURL,
		exactBase:  exactBase,
		archiveURL: archiveURL,
		files: map[string][]byte{
			baseURL + "/manifest.json": append(manifestBytes, '\n'),
			archiveURL:                 archive,
			archiveURL + ".sha256":     []byte(digestText + "  " + archiveName + "\n"),
		},
	}
}

func (fixture *fixture) fetch(_ context.Context, source string, _ int64) ([]byte, error) {
	fixture.fetches++
	contents, ok := fixture.files[source]
	if !ok {
		return nil, errors.New("fixture not found")
	}
	return append([]byte(nil), contents...), nil
}

func archiveFixture(t *testing.T, version, goos, goarch string, binary []byte) []byte {
	t.Helper()
	metadata := archiveMetadata{Name: binaryName, Version: version}
	metadata.Target.OS = goos
	metadata.Target.Arch = goarch
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	gzipWriter := gzip.NewWriter(&output)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range []struct {
		name string
		mode int64
		data []byte
	}{{metadataName, 0o644, metadataBytes}, {binaryName, 0o755, binary}} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func writeCurrentBinary(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), binaryName)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileContents(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != want {
		t.Fatalf("contents=%q err=%v, want %q", contents, err, want)
	}
}
