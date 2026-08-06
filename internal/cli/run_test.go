package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/patrick-fu/grok-search-cli/internal/auth"
	"github.com/patrick-fu/grok-search-cli/internal/cli"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

var fixedNow = time.Date(2026, time.August, 5, 12, 0, 0, 0, time.UTC)

const (
	usageJSON    = "{\"schema_version\":1,\"error\":{\"code\":\"usage_error\",\"message\":\"The command arguments are invalid.\",\"retryable\":false}}\n"
	internalJSON = "{\"schema_version\":1,\"error\":{\"code\":\"internal_error\",\"message\":\"An unexpected internal error occurred.\",\"retryable\":false}}\n"
)

func TestInvalidQueriesEmitOneUsageErrorBeforeLoadingCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		terminal bool
	}{
		{name: "missing terminal query", args: []string{"web"}, terminal: true},
		{name: "empty positional query", args: []string{"web", ""}, terminal: true},
		{name: "whitespace positional query", args: []string{"x", " \t\u2003"}, terminal: true},
		{name: "missing piped query", args: []string{"web"}},
		{name: "whitespace piped query", args: []string{"web"}, stdin: " \t\n"},
		{name: "conflicting sources", args: []string{"web", "positional"}, stdin: "piped"},
		{name: "invalid UTF-8", args: []string{"x"}, stdin: string([]byte{0xff, 0xfe})},
		{name: "oversized stdin", args: []string{"web"}, stdin: strings.Repeat("q", 64<<10+1)},
		{name: "oversized positional", args: []string{"web", strings.Repeat("q", 64<<10+1)}, terminal: true},
		{name: "multiple positionals", args: []string{"web", "one", "two"}, terminal: true},
		{name: "unknown command", args: []string{"chat", "query"}, terminal: true},
		{name: "unknown flag", args: []string{"web", "--unknown", "query"}, terminal: true},
		{name: "missing auth flag value", args: []string{"web", "--auth-file"}, terminal: true},
		{name: "flag used as auth value", args: []string{"web", "--auth-file", "--other", "query"}, terminal: true},
		{name: "duplicate auth flag", args: []string{"web", "--auth-file", "one", "--auth-file=two", "query"}, terminal: true},
		{name: "bare delimiter", args: []string{"web", "--"}, terminal: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded := false
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				loaded = true
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = test.terminal

			exitCode, stdout, stderr := run(runtime, test.args, test.stdin)
			if exitCode != 2 {
				t.Fatalf("exit code = %d, want 2", exitCode)
			}
			if stdout != usageJSON {
				t.Fatalf("stdout = %q, want %q", stdout, usageJSON)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			if loaded {
				t.Fatal("credential loader was called for invalid input")
			}
		})
	}
}

func TestHelpAndVersionAreHumanReadable(t *testing.T) {
	t.Parallel()

	runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
		t.Fatal("credential loader must not be called")
		return auth.Credential{}, nil
	})
	runtime.Version = "1.2.3-test"

	for _, args := range [][]string{{"help"}, {"--help"}, {"web", "--help"}} {
		exitCode, stdout, stderr := run(runtime, args, "")
		if exitCode != 0 || !strings.Contains(stdout, "Usage:") || stderr != "" {
			t.Fatalf("args %v: exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
	}
	exitCode, stdout, stderr := run(runtime, []string{"version"}, "")
	if exitCode != 0 || stdout != "grok-search 1.2.3-test\n" || stderr != "" {
		t.Fatalf("version: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestAuthFileSelectionUsesGrokHomeAndInvocationOverride(t *testing.T) {
	t.Setenv("GROK_HOME", filepath.Join(t.TempDir(), "grok-home"))
	t.Setenv("GROK_AUTH_FILE", filepath.Join(t.TempDir(), "must-be-ignored.json"))

	var loadedPath string
	runtime := testRuntime(func(path string, _ time.Time) (auth.Credential, *search.Error) {
		loadedPath = path
		return auth.Credential{}, search.NewError(search.ErrorAuthMissing)
	})
	runtime.StdinIsTerminal = true

	defaultPath := filepath.Join(os.Getenv("GROK_HOME"), "auth.json")
	exitCode, _, _ := run(runtime, []string{"web", "query"}, "")
	if exitCode != 3 || loadedPath != defaultPath {
		t.Fatalf("default: exit=%d path=%q, want %q", exitCode, loadedPath, defaultPath)
	}

	override := filepath.Join(t.TempDir(), "custom-auth.json")
	exitCode, _, _ = run(runtime, []string{"x", "--auth-file", override, "query"}, "")
	if exitCode != 3 || loadedPath != override {
		t.Fatalf("override: exit=%d path=%q, want %q", exitCode, loadedPath, override)
	}
}

func TestAuthFailuresUseStableJSONAndExitThree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code    search.ErrorCode
		message string
	}{
		{search.ErrorAuthMissing, "No valid Grok credential was found. Run `grok login`."},
		{search.ErrorAuthUnreadable, "The Grok credential store could not be read."},
		{search.ErrorAuthInsecure, "The Grok credential store failed security checks."},
		{search.ErrorAuthInvalid, "The Grok credential store is invalid. Run `grok login`."},
		{search.ErrorAuthExpired, "The Grok credential has expired. Run `grok login`."},
	}

	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return auth.Credential{}, search.NewError(test.code)
			})
			runtime.StdinIsTerminal = true
			exitCode, stdout, stderr := run(runtime, []string{"web", "query"}, "")
			want := fmt.Sprintf("{\"schema_version\":1,\"error\":{\"code\":%q,\"message\":%q,\"retryable\":false}}\n", test.code, test.message)
			if exitCode != 3 || stdout != want || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want stdout %q", exitCode, stdout, stderr, want)
			}
		})
	}
}

func TestWebSearchRendersOneNormalizedResponseAndPreservesQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		args     []string
		stdin    string
		terminal bool
		query    string
	}{
		{name: "piped CRLF", args: []string{"web"}, stdin: "find docs\r\n", query: "find docs"},
		{name: "terminal positional", args: []string{"web", "find docs"}, terminal: true, query: "find docs"},
		{name: "empty stdin falls back to positional", args: []string{"web", "find docs"}, query: "find docs"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credential := auth.Credential{AccessToken: "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE"}
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return credential, nil
			})
			runtime.StdinIsTerminal = test.terminal
			called := false
			runtime.Search = func(ctx context.Context, request search.Request, gotCredential auth.Credential) (search.Response, *search.Error) {
				called = true
				if ctx.Err() != nil {
					t.Fatal("search context is already cancelled")
				}
				if gotCredential != credential {
					t.Fatalf("credential = %#v", gotCredential)
				}
				if request != (search.Request{Type: search.TypeWeb, Query: test.query, Model: "grok-4.5"}) {
					t.Fatalf("request = %#v", request)
				}
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          search.TypeWeb,
					Query:         request.Query,
					Model:         request.Model,
					ProviderModel: "grok-4.5-build",
					Answer:        "The docs are available.",
					Sources:       []search.Source{{URL: "https://docs.example.test/"}},
					Warnings:      []search.Warning{},
				}, nil
			}

			exitCode, stdout, stderr := run(runtime, test.args, test.stdin)
			want := fmt.Sprintf("{\"schema_version\":1,\"type\":\"web\",\"query\":%q,\"model\":\"grok-4.5\",\"provider_model\":\"grok-4.5-build\",\"answer\":\"The docs are available.\",\"sources\":[{\"url\":\"https://docs.example.test/\"}],\"warnings\":[]}\n", test.query)
			if exitCode != 0 || stdout != want || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want stdout %q", exitCode, stdout, stderr, want)
			}
			if !called {
				t.Fatal("search was not called")
			}
			for _, forbidden := range []string{credential.AccessToken, "Authorization", "raw SSE"} {
				if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
					t.Fatalf("output leaked %q", forbidden)
				}
			}
		})
	}
}

func TestCredentialStoreFailuresAreClassifiedWithoutNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prepare  func(t *testing.T, directory string) string
		wantCode search.ErrorCode
	}{
		{
			name: "missing file",
			prepare: func(_ *testing.T, directory string) string {
				return filepath.Join(directory, "missing.json")
			},
			wantCode: search.ErrorAuthMissing,
		},
		{
			name: "path metadata unreadable",
			prepare: func(_ *testing.T, directory string) string {
				return filepath.Join(directory, strings.Repeat("x", 4096))
			},
			wantCode: search.ErrorAuthUnreadable,
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, directory string) string {
				target := writeStore(t, directory, "target.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), ""), 0o600)
				link := filepath.Join(directory, "auth.json")
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
				return link
			},
			wantCode: search.ErrorAuthInsecure,
		},
		{
			name: "non regular file",
			prepare: func(t *testing.T, directory string) string {
				path := filepath.Join(directory, "auth.json")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
			wantCode: search.ErrorAuthInsecure,
		},
		{
			name: "group readable mode",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), ""), 0o640)
			},
			wantCode: search.ErrorAuthInsecure,
		},
		{
			name: "oversized store",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", []byte(strings.Repeat("x", 1<<20+1)), 0o600)
			},
			wantCode: search.ErrorAuthInsecure,
		},
		{
			name: "malformed JSON",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", []byte("{"), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "invalid UTF-8 JSON",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", []byte{'{', '"', 0xff, '"', ':', '{', '}', '}'}, 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "non object record",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", []byte(`{"record":42}`), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "no eligible record",
			prepare: func(t *testing.T, directory string) string {
				store := map[string]any{
					"api":   map[string]any{"auth_mode": "api_key", "oidc_issuer": "https://auth.x.ai", "key": "TEST_API_KEY"},
					"other": map[string]any{"auth_mode": "oidc", "oidc_issuer": "https://example.com", "key": "TEST_OTHER_TOKEN"},
				}
				return writeStore(t, directory, "auth.json", marshal(t, store), 0o600)
			},
			wantCode: search.ErrorAuthMissing,
		},
		{
			name: "multiple eligible records",
			prepare: func(t *testing.T, directory string) string {
				one := validRecord("oidc", fixedNow.Add(-time.Hour), "")
				two := validRecord("oauth", fixedNow.Add(-time.Hour), "")
				return writeStore(t, directory, "auth.json", marshal(t, map[string]any{"one": one, "two": two}), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "duplicate map key",
			prepare: func(t *testing.T, directory string) string {
				record := string(validStore(t, "oidc", fixedNow.Add(-time.Hour), ""))
				record = strings.TrimPrefix(strings.TrimSuffix(record, "}"), "{")
				return writeStore(t, directory, "auth.json", []byte("{"+record+","+record+"}"), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "null expiry",
			prepare: func(t *testing.T, directory string) string {
				record := validRecord("oidc", fixedNow.Add(-time.Hour), "")
				record["expires_at"] = nil
				return writeStore(t, directory, "auth.json", marshal(t, map[string]any{"production": record}), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "future creation time",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", validStore(t, "oidc", fixedNow.Add(5*time.Minute+time.Second), ""), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "invalid creation timestamp",
			prepare: func(t *testing.T, directory string) string {
				record := validRecord("oidc", fixedNow.Add(-time.Hour), "")
				record["create_time"] = "not-rfc3339"
				return writeStore(t, directory, "auth.json", marshal(t, map[string]any{"production": record}), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "invalid expiry timestamp",
			prepare: func(t *testing.T, directory string) string {
				return writeStore(t, directory, "auth.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), "not-rfc3339"), 0o600)
			},
			wantCode: search.ErrorAuthInvalid,
		},
		{
			name: "fallback expiry buffer",
			prepare: func(t *testing.T, directory string) string {
				created := fixedNow.Add(-30*24*time.Hour + 5*time.Minute)
				return writeStore(t, directory, "auth.json", validStore(t, "oidc", created, ""), 0o600)
			},
			wantCode: search.ErrorAuthExpired,
		},
		{
			name: "explicit expiry takes precedence",
			prepare: func(t *testing.T, directory string) string {
				expires := fixedNow.Add(5 * time.Minute).Format(time.RFC3339)
				return writeStore(t, directory, "auth.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), expires), 0o600)
			},
			wantCode: search.ErrorAuthExpired,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := test.prepare(t, directory)
			runtime := cli.DefaultRuntime()
			runtime.Now = func() time.Time { return fixedNow }
			runtime.StdinIsTerminal = true
			exitCode, stdout, stderr := run(runtime, []string{"web", "--auth-file", path, "query"}, "")
			if exitCode != 3 {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want exit 3", exitCode, stdout, stderr)
			}
			assertErrorCode(t, stdout, test.wantCode)
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestUnreadableStoreIsReportedWhenPermissionsDenyOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read an owner-unreadable file")
	}
	path := writeStore(t, t.TempDir(), "auth.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), ""), 0o600)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	runtime := cli.DefaultRuntime()
	runtime.Now = func() time.Time { return fixedNow }
	runtime.StdinIsTerminal = true
	exitCode, stdout, _ := run(runtime, []string{"web", "--auth-file", path, "query"}, "")
	if exitCode != 3 {
		t.Fatalf("exit=%d stdout=%q, want exit 3", exitCode, stdout)
	}
	assertErrorCode(t, stdout, search.ErrorAuthUnreadable)
}

func TestValidLegacyAndCurrentCredentialsFailClosedWithoutLeakingOrChangingStore(t *testing.T) {
	for _, mode := range []string{"oidc", "oauth"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			expires := ""
			if mode == "oidc" {
				expires = fixedNow.Add(time.Hour).Format(time.RFC3339)
			}
			path := writeStore(t, directory, "auth.json", validStore(t, mode, fixedNow.Add(-time.Hour), expires), 0o600)
			before := snapshot(t, path)

			runtime := cli.DefaultRuntime()
			runtime.Now = func() time.Time { return fixedNow }
			runtime.StdinIsTerminal = true
			exitCode, stdout, stderr := run(runtime, []string{"x", "--auth-file", path, "query"}, "")

			if exitCode != 70 || stdout != internalJSON || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			for _, forbidden := range []string{"TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE", "TEST_ONLY_REFRESH_SENTINEL_DO_NOT_USE", "Authorization", path} {
				if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
					t.Fatalf("output leaked %q", forbidden)
				}
			}
			after := snapshot(t, path)
			if before != after {
				t.Fatalf("credential store changed:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func TestSecureCredentialStoreSupportsOfflineWebSearchWithoutLeakingOrChangingStore(t *testing.T) {
	for _, mode := range []string{"oidc", "oauth"} {
		t.Run(mode, func(t *testing.T) {
			directory := t.TempDir()
			expires := ""
			if mode == "oidc" {
				expires = fixedNow.Add(time.Hour).Format(time.RFC3339)
			}
			path := writeStore(t, directory, "auth.json", validStore(t, mode, fixedNow.Add(-time.Hour), expires), 0o600)
			before := snapshot(t, path)

			runtime := cli.DefaultRuntime()
			runtime.Now = func() time.Time { return fixedNow }
			runtime.StdinIsTerminal = true
			runtime.Search = func(_ context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error) {
				if credential.AccessToken != "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE" {
					t.Fatalf("unexpected credential")
				}
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          request.Type,
					Query:         request.Query,
					Model:         request.Model,
					Answer:        "Offline response.",
					Sources:       []search.Source{},
					Warnings:      []search.Warning{search.WarningMissingCitations},
				}, nil
			}

			exitCode, stdout, stderr := run(runtime, []string{"web", "--auth-file", path, "query"}, "")
			if exitCode != 0 || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			for _, forbidden := range []string{"TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE", "TEST_ONLY_REFRESH_SENTINEL_DO_NOT_USE", "Authorization", path} {
				if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
					t.Fatalf("output leaked %q", forbidden)
				}
			}
			after := snapshot(t, path)
			if before != after {
				t.Fatalf("credential store changed:\nbefore=%+v\nafter=%+v", before, after)
			}
		})
	}
}

func testRuntime(loader func(string, time.Time) (auth.Credential, *search.Error)) cli.Runtime {
	return cli.Runtime{
		Version:        "test",
		Now:            func() time.Time { return fixedNow },
		LoadCredential: loader,
	}
}

func run(runtime cli.Runtime, args []string, stdin string) (int, string, string) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := cli.Run(args, strings.NewReader(stdin), &stdout, &stderr, runtime)
	return exitCode, stdout.String(), stderr.String()
}

func writeStore(t *testing.T, directory, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func validStore(t *testing.T, mode string, created time.Time, expires string) []byte {
	t.Helper()
	return marshal(t, map[string]any{"production": validRecord(mode, created, expires)})
}

func validRecord(mode string, created time.Time, expires string) map[string]any {
	record := map[string]any{
		"key":           "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE",
		"refresh_token": "TEST_ONLY_REFRESH_SENTINEL_DO_NOT_USE",
		"auth_mode":     mode,
		"oidc_issuer":   "https://auth.x.ai",
		"create_time":   created.Format(time.RFC3339),
	}
	if expires != "" {
		record["expires_at"] = expires
	}
	return record
}

func marshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertErrorCode(t *testing.T, stdout string, want search.ErrorCode) {
	t.Helper()
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Error         struct {
			Code search.ErrorCode `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not one JSON object: %q: %v", stdout, err)
	}
	if envelope.SchemaVersion != 1 || envelope.Error.Code != want || !strings.HasSuffix(stdout, "\n") || strings.Count(stdout, "\n") != 1 {
		t.Fatalf("stdout=%q, want schema 1 and code %q", stdout, want)
	}
}

type fileSnapshot struct {
	Hash    [sha256.Size]byte
	Mode    os.FileMode
	Size    int64
	ModTime int64
	UID     uint32
	Inode   uint64
}

func snapshot(t *testing.T, path string) fileSnapshot {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	result := fileSnapshot{
		Hash:    sha256.Sum256(data),
		Mode:    info.Mode(),
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		result.UID = stat.Uid
		result.Inode = stat.Ino
	}
	return result
}
