package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const (
	maxStoreBytes    = 1 << 20
	earlyExpiry      = 5 * time.Minute
	fallbackLifetime = 30 * 24 * time.Hour
)

type Credential struct {
	AccessToken string
}

type fileSystem interface {
	Lstat(path string) (os.FileInfo, error)
	OpenReadOnlyNoFollow(path string) (*os.File, error)
}

type Loader struct {
	files fileSystem
}

func NewLoader() Loader {
	return Loader{files: osFileSystem{}}
}

func (loader Loader) Load(selectedPath string, now time.Time) (Credential, *search.Error) {
	if loader.files == nil {
		loader.files = osFileSystem{}
	}

	absolutePath, err := filepath.Abs(selectedPath)
	if err != nil {
		return Credential{}, search.NewError(search.ErrorAuthUnreadable)
	}

	data, loadErr := loader.readSecureFile(absolutePath)
	if loadErr != nil {
		return Credential{}, loadErr
	}

	record, parseErr := selectRecord(data)
	if parseErr != nil {
		return Credential{}, parseErr
	}

	createTime, err := time.Parse(time.RFC3339, record.createTime)
	if err != nil || createTime.After(now.Add(earlyExpiry)) {
		return Credential{}, search.NewError(search.ErrorAuthInvalid)
	}

	expiresAt := createTime.Add(fallbackLifetime)
	if record.expiresAt != "" {
		expiresAt, err = time.Parse(time.RFC3339, record.expiresAt)
		if err != nil {
			return Credential{}, search.NewError(search.ErrorAuthInvalid)
		}
	}
	if !now.Before(expiresAt.Add(-earlyExpiry)) {
		return Credential{}, search.NewError(search.ErrorAuthExpired)
	}

	return Credential{AccessToken: record.key}, nil
}

func (loader Loader) readSecureFile(path string) ([]byte, *search.Error) {
	before, err := loader.files.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, search.NewError(search.ErrorAuthMissing)
		}
		return nil, search.NewError(search.ErrorAuthUnreadable)
	}
	if !secureFileInfo(before) {
		return nil, search.NewError(search.ErrorAuthInsecure)
	}

	file, err := loader.files.OpenReadOnlyNoFollow(path)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, os.ErrNotExist) {
			return nil, search.NewError(search.ErrorAuthInsecure)
		}
		return nil, search.NewError(search.ErrorAuthUnreadable)
	}
	defer file.Close()

	after, err := file.Stat()
	if err != nil {
		return nil, search.NewError(search.ErrorAuthUnreadable)
	}
	if !os.SameFile(before, after) || !secureFileInfo(after) {
		return nil, search.NewError(search.ErrorAuthInsecure)
	}

	data, err := io.ReadAll(io.LimitReader(file, maxStoreBytes+1))
	if err != nil {
		return nil, search.NewError(search.ErrorAuthUnreadable)
	}
	if len(data) > maxStoreBytes {
		return nil, search.NewError(search.ErrorAuthInsecure)
	}
	return data, nil
}

func secureFileInfo(info os.FileInfo) bool {
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false
	}
	if info.Size() < 0 || info.Size() > maxStoreBytes {
		return false
	}
	uid, ok := ownerID(info)
	return ok && uid == uint32(os.Geteuid())
}

type credentialRecord struct {
	key        string
	createTime string
	expiresAt  string
}

func selectRecord(data []byte) (credentialRecord, *search.Error) {
	if !utf8.Valid(data) {
		return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
	}
	records, ok := decodeObject(data)
	if !ok {
		return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
	}

	var eligible []credentialRecord
	for _, raw := range records {
		fields, objectOK := decodeObject(raw)
		if !objectOK {
			return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
		}

		mode, modePresent, modeValid := stringField(fields, "auth_mode")
		issuer, issuerPresent, issuerValid := stringField(fields, "oidc_issuer")
		if !modeValid || !issuerValid {
			return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
		}

		oauthMode := mode == "oidc" || mode == "oauth"
		xaiIssuer := issuer == "https://auth.x.ai"
		if oauthMode && !issuerPresent {
			return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
		}
		if xaiIssuer && !modePresent {
			return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
		}
		if !oauthMode || !xaiIssuer {
			continue
		}

		key, keyPresent, keyValid := stringField(fields, "key")
		created, createdPresent, createdValid := stringField(fields, "create_time")
		expires, expiresPresent, expiresValid := stringField(fields, "expires_at")
		if !keyValid || !createdValid || !expiresValid || !keyPresent || key == "" || !createdPresent || created == "" || expiresPresent && expires == "" {
			return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
		}
		eligible = append(eligible, credentialRecord{key: key, createTime: created, expiresAt: expires})
	}

	switch len(eligible) {
	case 0:
		return credentialRecord{}, search.NewError(search.ErrorAuthMissing)
	case 1:
		return eligible[0], nil
	default:
		return credentialRecord{}, search.NewError(search.ErrorAuthInvalid)
	}
}

func decodeObject(data []byte) (map[string]json.RawMessage, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, false
	}

	values := make(map[string]json.RawMessage)
	for decoder.More() {
		name, ok := nextObjectName(decoder)
		if !ok {
			return nil, false
		}
		if _, duplicate := values[name]; duplicate {
			return nil, false
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, false
		}
		values[name] = raw
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return nil, false
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, false
	}
	return values, true
}

func nextObjectName(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil {
		return "", false
	}
	name, ok := token.(string)
	return name, ok
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected JSON value")
	}
	return err
}

func stringField(fields map[string]json.RawMessage, name string) (value string, present bool, valid bool) {
	raw, present := fields[name]
	if !present {
		return "", false, true
	}
	var decoded *string
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded == nil {
		return "", true, false
	}
	return *decoded, true, true
}
