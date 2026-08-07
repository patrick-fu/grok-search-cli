// private_archive.go creates and verifies deterministic private release archives.
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
	var verify, checksum string
	flag.StringVar(&input, "input", "", "path to the built grok-search binary")
	flag.StringVar(&output, "output", "", "output .tar.gz path")
	flag.StringVar(&version, "version", "", "release version")
	flag.StringVar(&goos, "goos", "", "target operating system")
	flag.StringVar(&goarch, "goarch", "", "target architecture")
	flag.StringVar(&epochText, "source-date-epoch", "", "SOURCE_DATE_EPOCH in Unix seconds")
	flag.StringVar(&verify, "verify", "", "archive to verify")
	flag.StringVar(&checksum, "checksum", "", "checksum sidecar to verify")
	flag.Parse()

	epoch, err := parseEpoch(epochText)
	if err != nil {
		fatal(err)
	}
	want := newMetadata(version, goos, goarch, epoch)
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

func newMetadata(version, goos, goarch string, epoch int64) metadata {
	return metadata{
		ArchiveFormat: 1,
		Name:          binaryName,
		Version:       version,
		Target:        target{OS: goos, Arch: goarch},
		Compatibility: compatibility{MinimumGrokCLIVersion: compatFloor},
		Distribution: distribution{
			Visibility:    "private",
			PublicRelease: false,
			OwnerRisk:     "The private owner is responsible for distribution and use.",
		},
		SourceDateEpoch: epoch,
	}
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
