package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewMetadataPrivate(t *testing.T) {
	meta, err := newMetadata("0.3.0-private.1", "darwin", "arm64", "private", 1700000000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Distribution.Visibility != "private" {
		t.Errorf("visibility = %q, want private", meta.Distribution.Visibility)
	}
	if meta.Distribution.PublicRelease {
		t.Error("public_release should be false for private")
	}
	if meta.Distribution.OwnerRisk == "" {
		t.Error("owner_risk should be set for private")
	}
	if meta.ArchiveFormat != 1 {
		t.Errorf("archive_format = %d, want 1", meta.ArchiveFormat)
	}
}

func TestNewMetadataPublic(t *testing.T) {
	meta, err := newMetadata("0.3.0", "linux", "amd64", "public", 1700000000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Distribution.Visibility != "public" {
		t.Errorf("visibility = %q, want public", meta.Distribution.Visibility)
	}
	if !meta.Distribution.PublicRelease {
		t.Error("public_release should be true for public")
	}
	if meta.Distribution.OwnerRisk != "" {
		t.Error("owner_risk should be empty for public")
	}
}

func TestNewMetadataInvalidVisibility(t *testing.T) {
	if _, err := newMetadata("0.3.0", "linux", "amd64", "internal", 1700000000); err == nil {
		t.Error("expected error for invalid visibility")
	}
}

func TestValidateSemVer(t *testing.T) {
	valid := []string{
		"0.0.0", "0.3.0", "1.0.0", "10.20.30",
		"v0.3.0", "v1.0.0",
		"1.0.0-beta.1", "1.0.0-alpha",
		"1.0.0+build.123", "v1.0.0+meta",
	}
	for _, v := range valid {
		if err := validateSemVer(v); err != nil {
			t.Errorf("validateSemVer(%q) = %v, want nil", v, err)
		}
	}
	invalid := []string{
		"", "dev", "latest",
		"0.3", "1", "0.3.0.1",
		"01.2.3", "1.02.3", "1.2.03",
		"a.b.c", "1.x.0",
	}
	for _, v := range invalid {
		if err := validateSemVer(v); err == nil {
			t.Errorf("validateSemVer(%q) = nil, want error", v)
		}
	}
}

func TestPublicArchiveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "grok-search")
	archivePath := filepath.Join(dir, "grok-search_0.3.0_linux_amd64.tar.gz")

	if err := os.WriteFile(binaryPath, []byte("\x7fELF fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	meta, err := newMetadata("0.3.0", "linux", "amd64", "public", 1700000000)
	if err != nil {
		t.Fatal(err)
	}

	if err := createArchive(binaryPath, archivePath, meta); err != nil {
		t.Fatalf("createArchive: %v", err)
	}

	if err := verifyArchive(archivePath, archivePath+".sha256", meta); err != nil {
		t.Fatalf("verifyArchive: %v", err)
	}
}

func TestPublicArchiveDeterminism(t *testing.T) {
	binary := []byte("deterministic fake binary content")
	meta, err := newMetadata("0.3.0", "darwin", "arm64", "public", 1700000000)
	if err != nil {
		t.Fatal(err)
	}

	var archives [2][]byte
	for i := 0; i < 2; i++ {
		dir := t.TempDir()
		binaryPath := filepath.Join(dir, "grok-search")
		archivePath := filepath.Join(dir, "grok-search_0.3.0_darwin_arm64.tar.gz")
		if err := os.WriteFile(binaryPath, binary, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := createArchive(binaryPath, archivePath, meta); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		archives[i] = data
	}

	if !bytes.Equal(archives[0], archives[1]) {
		t.Error("public archives are not byte-identical across two builds")
	}
}

func TestGenerateManifest(t *testing.T) {
	dir := t.TempDir()
	writeManifestFixtures(t, dir, "0.3.0")

	manifestPath := filepath.Join(dir, "checksums.txt")
	if err := generateManifest(dir, manifestPath); err != nil {
		t.Fatalf("generateManifest: %v", err)
	}

	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(manifest)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
	for i, expected := range []string{
		"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64",
	} {
		if !strings.Contains(lines[i], expected) {
			t.Errorf("line %d: expected %q, got %q", i, expected, lines[i])
		}
	}
}

func TestGenerateManifestEmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := generateManifest(dir, filepath.Join(dir, "checksums.txt")); err == nil {
		t.Error("expected error for directory with no checksum files")
	}
}

func TestPrivateMetadataUnchanged(t *testing.T) {
	meta, err := newMetadata("0.3.0-private.1", "linux", "amd64", "private", 1700000000)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Distribution.Visibility != "private" || meta.Distribution.PublicRelease || meta.Distribution.OwnerRisk == "" {
		t.Errorf("private metadata changed: %+v", meta.Distribution)
	}
}

func TestGenerateJSONManifest(t *testing.T) {
	dir := t.TempDir()
	wantDigests := writeManifestFixtures(t, dir, "0.3.0")

	manifestPath := filepath.Join(dir, "manifest.json")
	if err := generateJSONManifest(dir, manifestPath, "0.3.0"); err != nil {
		t.Fatalf("generateJSONManifest: %v", err)
	}

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if manifest.Version != "0.3.0" {
		t.Errorf("version = %q, want 0.3.0", manifest.Version)
	}
	if manifest.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if len(manifest.Assets) != 4 {
		t.Fatalf("expected 4 assets, got %d", len(manifest.Assets))
	}

	// Assets should be sorted alphabetically
	wantOrder := []string{
		"grok-search_0.3.0_darwin_amd64.tar.gz",
		"grok-search_0.3.0_darwin_arm64.tar.gz",
		"grok-search_0.3.0_linux_amd64.tar.gz",
		"grok-search_0.3.0_linux_arm64.tar.gz",
	}
	for i, want := range wantOrder {
		if manifest.Assets[i].Name != want {
			t.Errorf("asset[%d].Name = %q, want %q", i, manifest.Assets[i].Name, want)
		}
	}
	if manifest.Assets[0].SHA256 != wantDigests[manifest.Assets[0].Name] {
		t.Errorf("asset[0].SHA256 = %q, want %q", manifest.Assets[0].SHA256, wantDigests[manifest.Assets[0].Name])
	}
	if manifest.Assets[0].OS != "darwin" || manifest.Assets[0].Arch != "amd64" {
		t.Errorf("asset[0] target = %s/%s, want darwin/amd64", manifest.Assets[0].OS, manifest.Assets[0].Arch)
	}
}

func writeManifestFixtures(t *testing.T, dir, version string) map[string]string {
	t.Helper()
	digests := map[string]string{}
	for _, target := range []struct{ goos, goarch string }{
		{"darwin", "amd64"}, {"darwin", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"},
	} {
		name := fmt.Sprintf("grok-search_%s_%s_%s.tar.gz", version, target.goos, target.goarch)
		contents := []byte("fixture " + target.goos + "/" + target.goarch)
		if err := os.WriteFile(filepath.Join(dir, name), contents, 0o644); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(contents)
		digestText := hex.EncodeToString(digest[:])
		digests[name] = digestText
		if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(digestText+"  "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return digests
}

func TestParseTargetFromArchiveName(t *testing.T) {
	tests := []struct {
		name     string
		wantOS   string
		wantArch string
		wantOK   bool
	}{
		{"grok-search_0.3.0_darwin_amd64.tar.gz", "darwin", "amd64", true},
		{"grok-search_1.2.3_linux_arm64.tar.gz", "linux", "arm64", true},
		{"grok-search_0.3.0_darwin_arm64.tar.gz.sha256", "", "", false},
		{"short.tar.gz", "", "", false},
		{"a_b_c.tar.gz", "", "", false},
	}
	for _, tt := range tests {
		goos, goarch, ok := parseTargetFromArchiveName(tt.name)
		if ok != tt.wantOK || goos != tt.wantOS || goarch != tt.wantArch {
			t.Errorf("parseTargetFromArchiveName(%q) = %s/%s/%v, want %s/%s/%v",
				tt.name, goos, goarch, ok, tt.wantOS, tt.wantArch, tt.wantOK)
		}
	}
}
