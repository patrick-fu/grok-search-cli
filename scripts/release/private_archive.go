// private_archive.go creates and verifies deterministic release archives for
// private and public distributions.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	binaryName   = "grok-search"
	metadataName = "METADATA.json"
	compatFloor  = "0.2.117"
)

type metadata struct {
	ArchiveFormat   int           `json:"archive_format"`
	Name            string        `json:"name"`
	Version         string        `json:"version"`
	Target          target        `json:"target"`
	Compatibility   compatibility `json:"compatibility"`
	Distribution    distribution  `json:"distribution"`
	SourceDateEpoch int64         `json:"source_date_epoch"`
}

type target struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type compatibility struct {
	MinimumGrokCLIVersion string `json:"minimum_grok_cli_version"`
}

type distribution struct {
	Visibility    string `json:"visibility"`
	PublicRelease bool   `json:"public_release"`
	OwnerRisk     string `json:"owner_risk"`
}

func main() {
	var input, output, version, goos, goarch, epochText string
	var verify, checksum, visibility, manifestDir, manifestOutput, manifestJSON string
	flag.StringVar(&input, "input", "", "path to the built grok-search binary")
	flag.StringVar(&output, "output", "", "output .tar.gz path")
	flag.StringVar(&version, "version", "", "release version")
	flag.StringVar(&goos, "goos", "", "target operating system")
	flag.StringVar(&goarch, "goarch", "", "target architecture")
	flag.StringVar(&epochText, "source-date-epoch", "", "SOURCE_DATE_EPOCH in Unix seconds")
	flag.StringVar(&verify, "verify", "", "archive to verify")
	flag.StringVar(&checksum, "checksum", "", "checksum sidecar to verify")
	flag.StringVar(&visibility, "visibility", "private", "release visibility: private or public")
	flag.StringVar(&manifestDir, "manifest-dir", "", "directory of .tar.gz.sha256 sidecars to combine into a checksums manifest")
	flag.StringVar(&manifestOutput, "manifest-output", "", "output path for the combined checksums manifest")
	flag.StringVar(&manifestJSON, "manifest-json", "", "output path for a JSON release manifest")
	flag.Parse()

	if manifestDir != "" {
		if manifestOutput == "" && manifestJSON == "" {
			fatal(errors.New("--manifest-output or --manifest-json is required with --manifest-dir"))
		}
		if manifestOutput != "" {
			fatalIf(generateManifest(manifestDir, manifestOutput))
		}
		if manifestJSON != "" {
			if version == "" {
				fatal(errors.New("--version is required with --manifest-json"))
			}
			fatalIf(generateJSONManifest(manifestDir, manifestJSON, version))
		}
		return
	}

	epoch, err := parseEpoch(epochText)
	if err != nil {
		fatal(err)
	}
	want, err := newMetadata(version, goos, goarch, visibility, epoch)
	if err != nil {
		fatal(err)
	}
	if verify != "" {
		if input != "" || output != "" {
			fatal(errors.New("--verify cannot be combined with --input or --output"))
		}
		if checksum == "" {
			checksum = verify + ".sha256"
		}
		fatalIf(verifyArchive(verify, checksum, want))
		return
	}
	if input == "" || output == "" {
		fatal(errors.New("--input and --output are required when creating an archive"))
	}
	fatalIf(createArchive(input, output, want))
}

func parseEpoch(value string) (int64, error) {
	if value == "" {
		return 0, errors.New("--source-date-epoch is required")
	}
	epoch, err := strconv.ParseInt(value, 10, 64)
	if err != nil || epoch < 0 {
		return 0, fmt.Errorf("invalid --source-date-epoch %q", value)
	}
	return epoch, nil
}

func newMetadata(version, goos, goarch, visibility string, epoch int64) (metadata, error) {
	if err := validateVisibility(visibility); err != nil {
		return metadata{}, err
	}
	if visibility == "public" {
		if err := validateSemVer(version); err != nil {
			return metadata{}, err
		}
	}
	dist := distribution{Visibility: visibility, PublicRelease: visibility == "public"}
	if visibility == "private" {
		dist.OwnerRisk = "The private owner is responsible for distribution and use."
	}
	return metadata{
		ArchiveFormat:   1,
		Name:            binaryName,
		Version:         version,
		Target:          target{OS: goos, Arch: goarch},
		Compatibility:   compatibility{MinimumGrokCLIVersion: compatFloor},
		Distribution:    dist,
		SourceDateEpoch: epoch,
	}, nil
}

func createArchive(input, output string, meta metadata) error {
	binary, err := os.ReadFile(input)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	if len(binary) == 0 {
		return errors.New("refusing to archive an empty binary")
	}
	metadataBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metadata: %w", err)
	}
	metadataBytes = append(metadataBytes, '\n')
	archiveBytes, err := deterministicArchive(metadataBytes, binary, meta.SourceDateEpoch)
	if err != nil {
		return err
	}
	if err := writeAtomically(output, archiveBytes, 0o644); err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	digest := sha256.Sum256(archiveBytes)
	checksum := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), filepath.Base(output))
	if err := writeAtomically(output+".sha256", []byte(checksum), 0o644); err != nil {
		return fmt.Errorf("write checksum: %w", err)
	}
	return nil
}

func deterministicArchive(metadataBytes, binary []byte, epoch int64) ([]byte, error) {
	var result bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&result, gzip.DefaultCompression)
	if err != nil {
		return nil, fmt.Errorf("create gzip writer: %w", err)
	}
	gzipWriter.Header.ModTime = time.Unix(epoch, 0).UTC()
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	modTime := time.Unix(epoch, 0).UTC()
	entries := []struct {
		name string
		mode int64
		data []byte
	}{
		{name: metadataName, mode: 0o644, data: metadataBytes},
		{name: binaryName, mode: 0o755, data: binary},
	}
	for _, entry := range entries {
		header := &tar.Header{Format: tar.FormatUSTAR, Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), ModTime: modTime, Uid: 0, Gid: 0}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, fmt.Errorf("write %s header: %w", entry.name, err)
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
			return nil, fmt.Errorf("write %s: %w", entry.name, err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, fmt.Errorf("finish tar: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("finish gzip: %w", err)
	}
	return result.Bytes(), nil
}

func verifyArchive(archivePath, checksumPath string, want metadata) error {
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	if err := verifyChecksum(archive, archivePath, checksumPath); err != nil {
		return err
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open gzip: %w", err)
	}
	defer gzipReader.Close()
	if !gzipReader.Header.ModTime.Equal(time.Unix(want.SourceDateEpoch, 0).UTC()) || gzipReader.Header.OS != 255 {
		return errors.New("gzip header is not deterministic")
	}
	tarReader := tar.NewReader(gzipReader)
	var foundMetadata metadata
	entries := []string{}
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		entries = append(entries, header.Name)
		if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(time.Unix(want.SourceDateEpoch, 0).UTC()) {
			return fmt.Errorf("entry %s does not have deterministic owner or timestamp", header.Name)
		}
		switch header.Name {
		case metadataName:
			if header.Mode != 0o644 {
				return errors.New("metadata mode is not 0644")
			}
			if err := json.NewDecoder(tarReader).Decode(&foundMetadata); err != nil {
				return fmt.Errorf("decode metadata: %w", err)
			}
		case binaryName:
			if header.Mode != 0o755 || header.Size == 0 {
				return errors.New("binary is missing, empty, or not executable")
			}
			if _, err := io.Copy(io.Discard, tarReader); err != nil {
				return fmt.Errorf("read binary: %w", err)
			}
		default:
			return fmt.Errorf("unexpected archive entry %q", header.Name)
		}
	}
	if strings.Join(entries, ",") != metadataName+","+binaryName {
		return fmt.Errorf("archive entry order = %q, want %q", strings.Join(entries, ","), metadataName+","+binaryName)
	}
	if foundMetadata != want {
		return fmt.Errorf("metadata = %+v, want %+v", foundMetadata, want)
	}
	return nil
}

func verifyChecksum(archive []byte, archivePath, checksumPath string) error {
	contents, err := os.ReadFile(checksumPath)
	if err != nil {
		return fmt.Errorf("read checksum: %w", err)
	}
	fields := strings.Fields(string(contents))
	if len(fields) != 2 || fields[1] != filepath.Base(archivePath) {
		return errors.New("checksum sidecar must contain the archive SHA-256 and basename")
	}
	digest := sha256.Sum256(archive)
	if fields[0] != hex.EncodeToString(digest[:]) {
		return errors.New("archive SHA-256 does not match checksum sidecar")
	}
	return nil
}

func validateVisibility(visibility string) error {
	if visibility != "private" && visibility != "public" {
		return fmt.Errorf("invalid visibility %q: must be private or public", visibility)
	}
	return nil
}

func validateSemVer(version string) error {
	v := strings.TrimPrefix(version, "v")
	if v != version && !strings.HasPrefix(version, "v") {
		return fmt.Errorf("version %q is not valid SemVer", version)
	}
	if v == "" || strings.Count(v, "+") > 1 {
		return fmt.Errorf("version %q is not valid SemVer", version)
	}
	if idx := strings.IndexByte(v, '+'); idx >= 0 {
		if idx == len(v)-1 || !validSemVerIdentifiers(v[idx+1:], false) {
			return fmt.Errorf("version %q has invalid build metadata", version)
		}
	}
	core := v
	if idx := strings.IndexByte(core, '+'); idx >= 0 {
		core = core[:idx]
	}
	if idx := strings.IndexByte(core, '-'); idx >= 0 {
		if idx == len(core)-1 || !validSemVerIdentifiers(core[idx+1:], true) {
			return fmt.Errorf("version %q has invalid prerelease identifiers", version)
		}
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return fmt.Errorf("version %q is not valid SemVer (expected MAJOR.MINOR.PATCH)", version)
	}
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return fmt.Errorf("version %q has non-numeric segment %q", version, p)
		}
		if strconv.Itoa(n) != p {
			return fmt.Errorf("version %q has leading zeros in segment %q", version, p)
		}
	}
	return nil
}

func validSemVerIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		numeric := true
		for _, character := range identifier {
			if character < '0' || character > '9' {
				numeric = false
			}
			if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '-') {
				return false
			}
		}
		if rejectNumericLeadingZero && numeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

type releaseManifest struct {
	SchemaVersion int             `json:"schema_version"`
	Version       string          `json:"version"`
	Assets        []manifestAsset `json:"assets"`
}

type manifestAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	OS     string `json:"os"`
	Arch   string `json:"arch"`
}

func generateJSONManifest(dir, output, version string) error {
	if err := validateSemVer(version); err != nil {
		return err
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*.tar.gz.sha256"))
	if err != nil {
		return fmt.Errorf("scan checksum files: %w", err)
	}
	if len(matches) != 4 {
		return fmt.Errorf("release manifest requires exactly 4 checksum files, found %d", len(matches))
	}
	sort.Strings(matches)
	manifest := releaseManifest{SchemaVersion: 1, Version: version}
	seenTargets := map[string]bool{}
	for _, checksumFile := range matches {
		assetName, digestText, err := readValidChecksumSidecar(checksumFile)
		if err != nil {
			return err
		}
		goos, goarch, ok := parseTargetFromArchiveName(assetName)
		targetKey := goos + "/" + goarch
		wantName := fmt.Sprintf("grok-search_%s_%s_%s.tar.gz", version, goos, goarch)
		if !ok || assetName != wantName || !supportedReleaseTarget(targetKey) || seenTargets[targetKey] {
			return fmt.Errorf("cannot parse target from archive name %q", assetName)
		}
		seenTargets[targetKey] = true
		manifest.Assets = append(manifest.Assets, manifestAsset{
			Name:   assetName,
			SHA256: digestText,
			OS:     goos,
			Arch:   goarch,
		})
	}
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"} {
		if !seenTargets[target] {
			return fmt.Errorf("release manifest is missing target %s", target)
		}
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	data = append(data, '\n')
	if err := writeAtomically(output, data, 0o644); err != nil {
		return fmt.Errorf("write JSON manifest: %w", err)
	}
	return nil
}

func supportedReleaseTarget(target string) bool {
	switch target {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64":
		return true
	default:
		return false
	}
}

func parseTargetFromArchiveName(name string) (goos, goarch string, ok bool) {
	base := strings.TrimSuffix(name, ".tar.gz")
	if base == name {
		return "", "", false
	}
	parts := strings.Split(base, "_")
	if len(parts) < 4 {
		return "", "", false
	}
	goos = parts[len(parts)-2]
	goarch = parts[len(parts)-1]
	for _, ch := range goos + goarch {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
			return "", "", false
		}
	}
	return goos, goarch, goos != "" && goarch != ""
}

func generateManifest(dir, output string) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*.tar.gz.sha256"))
	if err != nil {
		return fmt.Errorf("scan checksum files: %w", err)
	}
	if len(matches) == 0 {
		return errors.New("no .tar.gz.sha256 files found in manifest directory")
	}
	sort.Strings(matches)
	var buf bytes.Buffer
	for _, checksumFile := range matches {
		assetName, digestText, err := readValidChecksumSidecar(checksumFile)
		if err != nil {
			return err
		}
		fmt.Fprintf(&buf, "%s  %s\n", digestText, assetName)
	}
	if err := writeAtomically(output, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func readValidChecksumSidecar(checksumFile string) (string, string, error) {
	content, err := os.ReadFile(checksumFile)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", filepath.Base(checksumFile), err)
	}
	fields := strings.Fields(string(content))
	assetName := strings.TrimSuffix(filepath.Base(checksumFile), ".sha256")
	if len(fields) != 2 || fields[1] != assetName || len(fields[0]) != 64 || strings.ToLower(fields[0]) != fields[0] {
		return "", "", fmt.Errorf("invalid checksum file %s", filepath.Base(checksumFile))
	}
	if _, err := hex.DecodeString(fields[0]); err != nil {
		return "", "", fmt.Errorf("checksum file %s has an invalid SHA-256", filepath.Base(checksumFile))
	}
	archiveBytes, err := os.ReadFile(filepath.Join(filepath.Dir(checksumFile), assetName))
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", assetName, err)
	}
	digest := sha256.Sum256(archiveBytes)
	if hex.EncodeToString(digest[:]) != fields[0] {
		return "", "", fmt.Errorf("checksum file %s does not match archive", filepath.Base(checksumFile))
	}
	return assetName, fields[0], nil
}

func writeAtomically(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".private-release-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func fatalIf(err error) {
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
