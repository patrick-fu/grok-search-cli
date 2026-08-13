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
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	releaseBase      = "https://github.com/patrick-fu/grok-search-cli/releases/latest/download"
	maxManifestBytes = 1 << 20
	maxChecksumBytes = 4 << 10
	maxArchiveBytes  = 128 << 20
	maxMetadataBytes = 64 << 10
	maxRedirects     = 5
	binaryName       = "grok-search"
	metadataName     = "METADATA.json"
)

var trustedDownloadHosts = map[string]bool{
	"github.com":                           true,
	"objects.githubusercontent.com":        true,
	"release-assets.githubusercontent.com": true,
}

type Result struct {
	CurrentVersion string
	LatestVersion  string
	Updated        bool
}

type Config struct {
	CurrentVersion string
	ExecutablePath string
	GOOS           string
	GOARCH         string
	BaseURL        string
	Fetch          func(context.Context, string, int64) ([]byte, error)
	VerifyBinary   func(context.Context, string, string) error
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

type archiveMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Target  struct {
		OS   string `json:"os"`
		Arch string `json:"arch"`
	} `json:"target"`
}

func Apply(ctx context.Context, config Config) (Result, error) {
	config, err := withDefaults(config)
	if err != nil {
		return Result{}, err
	}
	current, err := parseSemVer(config.CurrentVersion)
	if err != nil {
		return Result{}, errors.New("self-update is unavailable for this development build")
	}

	manifestBytes, err := config.Fetch(ctx, config.BaseURL+"/manifest.json", maxManifestBytes)
	if err != nil {
		return Result{}, errors.New("could not download the release manifest")
	}
	manifest, asset, latest, err := parseManifest(manifestBytes, config.GOOS, config.GOARCH)
	if err != nil {
		return Result{}, err
	}
	result := Result{CurrentVersion: config.CurrentVersion, LatestVersion: manifest.Version}
	comparison := compareSemVer(current, latest)
	if comparison == 0 {
		return result, nil
	}
	if comparison > 0 {
		return Result{}, errors.New("the latest release is older than the current binary; refusing to downgrade")
	}

	assetBase := exactReleaseBase(config.BaseURL, manifest.Version)
	archiveURL := assetBase + "/" + url.PathEscape(asset.Name)
	archiveBytes, err := config.Fetch(ctx, archiveURL, maxArchiveBytes)
	if err != nil {
		return Result{}, errors.New("could not download the release archive")
	}
	checksumBytes, err := config.Fetch(ctx, archiveURL+".sha256", maxChecksumBytes)
	if err != nil {
		return Result{}, errors.New("could not download the release checksum")
	}
	if err := verifyChecksums(archiveBytes, checksumBytes, asset); err != nil {
		return Result{}, err
	}

	targetInfo, err := os.Lstat(config.ExecutablePath)
	if err != nil || targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.Mode().IsRegular() {
		return Result{}, errors.New("the current executable is not a replaceable regular file")
	}
	directory := filepath.Dir(config.ExecutablePath)
	staged, err := os.CreateTemp(directory, ".grok-search-update-*")
	if err != nil {
		return Result{}, errors.New("the executable directory is not writable")
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	if err := extractVerifiedBinary(archiveBytes, staged, manifest.Version, config.GOOS, config.GOARCH); err != nil {
		_ = staged.Close()
		return Result{}, err
	}
	if err := staged.Close(); err != nil {
		return Result{}, errors.New("could not finish staging the downloaded binary")
	}
	if err := os.Chmod(stagedPath, 0o755); err != nil {
		return Result{}, errors.New("could not make the downloaded binary executable")
	}
	if err := config.VerifyBinary(ctx, stagedPath, manifest.Version); err != nil {
		return Result{}, errors.New("the downloaded binary failed its version check")
	}
	if err := replaceWithRollback(ctx, config, stagedPath, manifest.Version); err != nil {
		return Result{}, err
	}
	result.Updated = true
	return result, nil
}

func exactReleaseBase(baseURL, version string) string {
	const latestSuffix = "/latest/download"
	if strings.HasSuffix(baseURL, latestSuffix) {
		return strings.TrimSuffix(baseURL, latestSuffix) + "/download/v" + url.PathEscape(version)
	}
	return baseURL
}

func withDefaults(config Config) (Config, error) {
	if config.GOOS == "" {
		config.GOOS = runtime.GOOS
	}
	if config.GOARCH == "" {
		config.GOARCH = runtime.GOARCH
	}
	if !supportedTarget(config.GOOS, config.GOARCH) {
		return Config{}, errors.New("self-update is not supported on this platform")
	}
	if config.ExecutablePath == "" {
		path, err := os.Executable()
		if err != nil {
			return Config{}, errors.New("could not locate the current executable")
		}
		config.ExecutablePath = path
	}
	if config.BaseURL == "" {
		config.BaseURL = releaseBase
	}
	config.BaseURL = strings.TrimRight(config.BaseURL, "/")
	if config.Fetch == nil {
		config.Fetch = secureFetch
	}
	if config.VerifyBinary == nil {
		config.VerifyBinary = verifyBinaryVersion
	}
	return config, nil
}

func supportedTarget(goos, goarch string) bool {
	return (goos == "darwin" || goos == "linux") && (goarch == "amd64" || goarch == "arm64")
}

func secureFetch(ctx context.Context, source string, limit int64) ([]byte, error) {
	parsed, err := url.Parse(source)
	if err != nil || parsed.Scheme != "https" || !trustedDownloadHosts[strings.ToLower(parsed.Hostname())] {
		return nil, errors.New("untrusted release URL")
	}
	client := &http.Client{
		Timeout: 2 * time.Minute,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects || request.URL.Scheme != "https" || !trustedDownloadHosts[strings.ToLower(request.URL.Hostname())] {
				return errors.New("untrusted release redirect")
			}
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("download exceeds size limit")
	}
	return data, nil
}

func parseManifest(data []byte, goos, goarch string) (releaseManifest, manifestAsset, semVersion, error) {
	var manifest releaseManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return manifest, manifestAsset{}, semVersion{}, errors.New("release manifest is invalid")
	}
	if decoder.Decode(&struct{}{}) != io.EOF || manifest.SchemaVersion != 1 {
		return manifest, manifestAsset{}, semVersion{}, errors.New("release manifest is invalid")
	}
	version, err := parseSemVer(manifest.Version)
	if err != nil {
		return manifest, manifestAsset{}, semVersion{}, errors.New("release manifest contains an invalid version")
	}
	wantName := fmt.Sprintf("grok-search_%s_%s_%s.tar.gz", manifest.Version, goos, goarch)
	var match manifestAsset
	matches := 0
	for _, asset := range manifest.Assets {
		if asset.OS == goos && asset.Arch == goarch {
			match = asset
			matches++
		}
	}
	if matches != 1 || match.Name != wantName || !isLowerHexDigest(match.SHA256) {
		return manifest, manifestAsset{}, semVersion{}, errors.New("release manifest does not contain one valid asset for this platform")
	}
	return manifest, match, version, nil
}

func verifyChecksums(archive, sidecar []byte, asset manifestAsset) error {
	fields := strings.Fields(string(sidecar))
	if len(fields) != 2 || fields[1] != asset.Name || !isLowerHexDigest(fields[0]) || fields[0] != asset.SHA256 {
		return errors.New("release checksum metadata is invalid")
	}
	digest := sha256.Sum256(archive)
	if hex.EncodeToString(digest[:]) != asset.SHA256 {
		return errors.New("release archive checksum does not match")
	}
	return nil
}

func isLowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func extractVerifiedBinary(archive []byte, output *os.File, version, goos, goarch string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return errors.New("release archive is not valid gzip")
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)

	header, err := tarReader.Next()
	if err != nil || header.Name != metadataName || header.Typeflag != tar.TypeReg || header.Size > maxMetadataBytes {
		return errors.New("release archive metadata entry is invalid")
	}
	metadataBytes, err := io.ReadAll(io.LimitReader(tarReader, maxMetadataBytes+1))
	if err != nil || int64(len(metadataBytes)) > maxMetadataBytes {
		return errors.New("release archive metadata is invalid")
	}
	var metadata archiveMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil || metadata.Name != binaryName || metadata.Version != version || metadata.Target.OS != goos || metadata.Target.Arch != goarch {
		return errors.New("release archive metadata does not match this platform")
	}

	header, err = tarReader.Next()
	if err != nil || header.Name != binaryName || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxArchiveBytes {
		return errors.New("release archive binary entry is invalid")
	}
	if err := output.Truncate(0); err != nil {
		return errors.New("could not stage the downloaded binary")
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		return errors.New("could not stage the downloaded binary")
	}
	if _, err := io.CopyN(output, tarReader, header.Size); err != nil {
		return errors.New("could not stage the downloaded binary")
	}
	if next, nextErr := tarReader.Next(); nextErr != io.EOF || next != nil {
		return errors.New("release archive contains unexpected entries")
	}
	return nil
}

func verifyBinaryVersion(ctx context.Context, path, version string) error {
	checkContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(checkContext, path, "version")
	output, err := command.Output()
	if err != nil || string(output) != binaryName+" "+version+"\n" {
		return errors.New("version check failed")
	}
	return nil
}

func replaceWithRollback(ctx context.Context, config Config, stagedPath, version string) error {
	info, err := os.Lstat(config.ExecutablePath)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("the current executable changed during update")
	}
	directory := filepath.Dir(config.ExecutablePath)
	backupFile, err := os.CreateTemp(directory, ".grok-search-backup-*")
	if err != nil {
		return errors.New("could not prepare update rollback")
	}
	backupPath := backupFile.Name()
	if err := backupFile.Close(); err != nil {
		os.Remove(backupPath)
		return errors.New("could not prepare update rollback")
	}
	if err := os.Remove(backupPath); err != nil || os.Link(config.ExecutablePath, backupPath) != nil {
		os.Remove(backupPath)
		return errors.New("could not preserve the current binary for rollback")
	}
	cleanupBackup := true
	defer func() {
		if cleanupBackup {
			_ = os.Remove(backupPath)
		}
	}()
	if err := os.Rename(stagedPath, config.ExecutablePath); err != nil {
		return errors.New("could not replace the current binary")
	}
	if err := config.VerifyBinary(ctx, config.ExecutablePath, version); err == nil {
		return nil
	}
	if rollbackErr := os.Rename(backupPath, config.ExecutablePath); rollbackErr != nil {
		cleanupBackup = false
		return errors.New("the updated binary failed verification and rollback failed")
	}
	return errors.New("the updated binary failed verification; the previous binary was restored")
}

type semVersion struct {
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
}

func parseSemVer(value string) (semVersion, error) {
	core := value
	if index := strings.IndexByte(core, '+'); index >= 0 {
		if index == len(core)-1 || !validIdentifiers(core[index+1:], false) {
			return semVersion{}, errors.New("invalid SemVer")
		}
		core = core[:index]
	}
	var prerelease []string
	if index := strings.IndexByte(core, '-'); index >= 0 {
		if index == len(core)-1 || !validIdentifiers(core[index+1:], true) {
			return semVersion{}, errors.New("invalid SemVer")
		}
		prerelease = strings.Split(core[index+1:], ".")
		core = core[:index]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return semVersion{}, errors.New("invalid SemVer")
	}
	values := make([]uint64, 3)
	for index, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return semVersion{}, errors.New("invalid SemVer")
		}
		parsed, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return semVersion{}, errors.New("invalid SemVer")
		}
		values[index] = parsed
	}
	return semVersion{major: values[0], minor: values[1], patch: values[2], prerelease: prerelease}, nil
}

func validIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" || (rejectNumericLeadingZero && len(identifier) > 1 && identifier[0] == '0' && allDigits(identifier)) {
			return false
		}
		for _, character := range identifier {
			if !((character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '-') {
				return false
			}
		}
	}
	return true
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func compareSemVer(left, right semVersion) int {
	for _, pair := range [][2]uint64{{left.major, right.major}, {left.minor, right.minor}, {left.patch, right.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.prerelease) == 0 && len(right.prerelease) == 0 {
		return 0
	}
	if len(left.prerelease) == 0 {
		return 1
	}
	if len(right.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(left.prerelease) && index < len(right.prerelease); index++ {
		leftID, rightID := left.prerelease[index], right.prerelease[index]
		if leftID == rightID {
			continue
		}
		leftNumeric, rightNumeric := allDigits(leftID), allDigits(rightID)
		if leftNumeric && !rightNumeric {
			return -1
		}
		if !leftNumeric && rightNumeric {
			return 1
		}
		if leftNumeric {
			if len(leftID) < len(rightID) || (len(leftID) == len(rightID) && leftID < rightID) {
				return -1
			}
			return 1
		}
		if leftID < rightID {
			return -1
		}
		return 1
	}
	if len(left.prerelease) < len(right.prerelease) {
		return -1
	}
	if len(left.prerelease) > len(right.prerelease) {
		return 1
	}
	return 0
}
