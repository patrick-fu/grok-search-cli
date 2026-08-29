package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/patrick-fu/grok-search-cli/internal/auth"
	"github.com/patrick-fu/grok-search-cli/internal/cli"
	"github.com/patrick-fu/grok-search-cli/internal/search"
	updatecli "github.com/patrick-fu/grok-search-cli/internal/update"
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

	t.Run("x over unique handle limit", func(t *testing.T) {
		args := []string{"x"}
		for index := 0; index < 21; index++ {
			args = append(args, "--allow-handle", fmt.Sprintf("user%d", index))
		}
		args = append(args, "query")
		loaded := false
		runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
			loaded = true
			return auth.Credential{}, nil
		})
		runtime.StdinIsTerminal = true
		exitCode, stdout, stderr := run(runtime, args, "")
		if exitCode != 2 || stdout != usageJSON || stderr != "" || loaded {
			t.Fatalf("exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
		}
	})
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
		if exitCode != 0 || !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "Default model: grok-4.6") || stderr != "" {
			t.Fatalf("args %v: exit=%d stdout=%q stderr=%q", args, exitCode, stdout, stderr)
		}
	}
	exitCode, stdout, stderr := run(runtime, []string{"version"}, "")
	if exitCode != 0 || stdout != "grok-search 1.2.3-test\n" || stderr != "" {
		t.Fatalf("version: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	}
}

func TestUpdateCommandIsDirectAndNeverLoadsCredentials(t *testing.T) {
	loaded := false
	runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
		loaded = true
		return auth.Credential{}, nil
	})
	runtime.Version = "1.0.0"
	runtime.Update = func(_ context.Context, version string) (updatecli.Result, error) {
		if version != "1.0.0" {
			t.Fatalf("version = %q", version)
		}
		return updatecli.Result{CurrentVersion: version, LatestVersion: "1.1.0", Updated: true}, nil
	}
	exitCode, stdout, stderr := run(runtime, []string{"update"}, "")
	if exitCode != 0 || stdout != "Updated grok-search from 1.0.0 to 1.1.0.\n" || stderr != "" || loaded {
		t.Fatalf("exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
	}
}

func TestUpdateCommandFailureIsHumanReadableAndDoesNotLoadCredentials(t *testing.T) {
	loaded := false
	runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
		loaded = true
		return auth.Credential{}, nil
	})
	runtime.Version = "dev"
	runtime.Update = func(context.Context, string) (updatecli.Result, error) {
		return updatecli.Result{}, errors.New("self-update is unavailable for this development build")
	}
	exitCode, stdout, stderr := run(runtime, []string{"update"}, "")
	if exitCode != 4 || stdout != "" || stderr != "update failed: self-update is unavailable for this development build\n" || loaded {
		t.Fatalf("exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
	}
	exitCode, stdout, stderr = run(runtime, []string{"update", "extra"}, "")
	if exitCode != 2 || stdout != "" || stderr != "" || loaded {
		t.Fatalf("usage exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
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

func TestCommonScalarFlagValidationPrecedesCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing timeout value", args: []string{"web", "--timeout"}},
		{name: "missing timeout value before flag", args: []string{"web", "--timeout", "--verbose", "query"}},
		{name: "empty timeout value", args: []string{"web", "--timeout=", "query"}},
		{name: "duplicate timeout", args: []string{"web", "--timeout", "5s", "--timeout=10s", "query"}},
		{name: "timeout below range", args: []string{"web", "--timeout", "999ms", "query"}},
		{name: "zero timeout", args: []string{"web", "--timeout", "0s", "query"}},
		{name: "negative timeout", args: []string{"web", "--timeout=-1s", "query"}},
		{name: "timeout above range", args: []string{"web", "--timeout", "181s", "query"}},
		{name: "timeout above range combined", args: []string{"web", "--timeout=3m1s", "query"}},
		{name: "timeout without unit", args: []string{"web", "--timeout", "90", "query"}},
		{name: "missing max tokens value", args: []string{"web", "--max-output-tokens"}},
		{name: "empty max tokens value", args: []string{"web", "--max-output-tokens=", "query"}},
		{name: "duplicate max tokens", args: []string{"web", "--max-output-tokens", "128", "--max-output-tokens=256", "query"}},
		{name: "max tokens below range", args: []string{"web", "--max-output-tokens", "127", "query"}},
		{name: "zero max tokens", args: []string{"web", "--max-output-tokens=0", "query"}},
		{name: "max tokens above range", args: []string{"web", "--max-output-tokens", "4097", "query"}},
		{name: "max tokens not an integer", args: []string{"web", "--max-output-tokens", "abc", "query"}},
		{name: "max tokens fractional", args: []string{"web", "--max-output-tokens=1024.5", "query"}},
		{name: "duplicate verbose", args: []string{"web", "--verbose", "--verbose", "query"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded := false
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				loaded = true
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = true

			exitCode, stdout, stderr := run(runtime, test.args, "")
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
				t.Fatal("credential loader was called for invalid flags")
			}
		})
	}
}

func TestCommonScalarFlagsAreForwardedToSearch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		wantTimeout   time.Duration
		wantMaxTokens int
	}{
		{name: "defaults", args: []string{"web", "query"}, wantTimeout: 90 * time.Second, wantMaxTokens: 1024},
		{name: "timeout lower bound", args: []string{"web", "--timeout", "1s", "query"}, wantTimeout: time.Second, wantMaxTokens: 1024},
		{name: "timeout upper bound", args: []string{"web", "--timeout=180s", "query"}, wantTimeout: 180 * time.Second, wantMaxTokens: 1024},
		{name: "timeout fractional", args: []string{"web", "--timeout", "1500ms", "query"}, wantTimeout: 1500 * time.Millisecond, wantMaxTokens: 1024},
		{name: "max tokens lower bound", args: []string{"web", "--max-output-tokens", "128", "query"}, wantTimeout: 90 * time.Second, wantMaxTokens: 128},
		{name: "max tokens upper bound", args: []string{"web", "--max-output-tokens=4096", "query"}, wantTimeout: 90 * time.Second, wantMaxTokens: 4096},
		{name: "both custom", args: []string{"web", "--timeout", "5s", "--max-output-tokens", "2048", "query"}, wantTimeout: 5 * time.Second, wantMaxTokens: 2048},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			credential := auth.Credential{AccessToken: "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE"}
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return credential, nil
			})
			runtime.StdinIsTerminal = true
			runtime.Search = func(ctx context.Context, request search.Request, gotCredential auth.Credential) (search.Response, *search.Error) {
				if gotCredential != credential {
					t.Fatalf("credential = %#v", gotCredential)
				}
				wantRequest := search.Request{Type: search.Type("web"), Query: "query", Model: "grok-4.6", MaxOutputTokens: test.wantMaxTokens}
				if request != wantRequest {
					t.Fatalf("request = %#v, want %#v", request, wantRequest)
				}
				deadline, ok := ctx.Deadline()
				if !ok {
					t.Fatal("search context has no deadline")
				}
				remaining := time.Until(deadline)
				if remaining <= 0 || remaining > test.wantTimeout {
					t.Fatalf("deadline remaining = %v, want within %v", remaining, test.wantTimeout)
				}
				if test.wantTimeout > 5*time.Second && remaining < test.wantTimeout-5*time.Second {
					t.Fatalf("deadline remaining = %v, want close to %v", remaining, test.wantTimeout)
				}
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          request.Type,
					Query:         request.Query,
					Model:         request.Model,
					Answer:        "Offline response.",
					Sources:       []search.Source{},
					Warnings:      []search.Warning{},
				}, nil
			}

			exitCode, stdout, stderr := run(runtime, test.args, "")
			if exitCode != 0 || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
		})
	}
}

func TestModelOptionIsOpaqueAndValidatedBeforeCredentials(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		args      []string
		wantModel string
	}{
		{name: "default web", args: []string{"web", "query"}, wantModel: "grok-4.6"},
		{name: "explicit future x", args: []string{"x", "--model", "future-model:beta.1", "query"}, wantModel: "future-model:beta.1"},
		{name: "preserves exact spelling", args: []string{"web", "--model=Grok.Future_1", "query"}, wantModel: "Grok.Future_1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = true
			runtime.Search = func(_ context.Context, request search.Request, _ auth.Credential) (search.Response, *search.Error) {
				if request.Model != test.wantModel {
					t.Fatalf("model = %q, want %q", request.Model, test.wantModel)
				}
				return search.Response{SchemaVersion: search.SchemaVersion, Type: request.Type, Query: request.Query, Model: request.Model, Sources: []search.Source{}, Warnings: []search.Warning{}, Answer: "ok"}, nil
			}
			if exitCode, _, stderr := run(runtime, test.args, ""); exitCode != 0 || stderr != "" {
				t.Fatalf("exit=%d stderr=%q", exitCode, stderr)
			}
		})
	}

	invalidModels := []struct {
		name string
		args []string
	}{
		{name: "missing", args: []string{"web", "--model"}},
		{name: "empty equals", args: []string{"web", "--model=", "query"}},
		{name: "duplicate", args: []string{"web", "--model", "one", "--model=two", "query"}},
		{name: "whitespace", args: []string{"web", "--model", "model name", "query"}},
		{name: "unicode whitespace", args: []string{"web", "--model=model\u00a0name", "query"}},
		{name: "control", args: []string{"web", "--model=model\nname", "query"}},
		{name: "invalid utf8", args: []string{"web", "--model=" + string([]byte{0xff}), "query"}},
		{name: "too long", args: []string{"web", "--model=" + strings.Repeat("a", 257), "query"}},
	}
	for _, test := range invalidModels {
		t.Run("invalid/"+test.name, func(t *testing.T) {
			loaded := false
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				loaded = true
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = true
			exitCode, stdout, stderr := run(runtime, test.args, "")
			if exitCode != 2 || stdout != usageJSON || stderr != "" || loaded {
				t.Fatalf("exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
			}
		})
	}
}

func TestFiltersValidateBeforeCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
	}{
		{name: "web modes conflict", args: []string{"web", "--allow-domain", "a.example", "--exclude-domain", "b.example", "query"}},
		{name: "web invalid scheme", args: []string{"web", "--allow-domain", "https://a.example", "query"}},
		{name: "web invalid port", args: []string{"web", "--allow-domain=a.example:443", "query"}},
		{name: "web invalid wildcard", args: []string{"web", "--allow-domain=*.example", "query"}},
		{name: "web over unique limit", args: []string{"web", "--allow-domain", "a.example", "--allow-domain", "b.example", "--allow-domain", "c.example", "--allow-domain", "d.example", "--allow-domain", "e.example", "--allow-domain", "f.example", "query"}},
		{name: "x modes conflict", args: []string{"x", "--allow-handle", "one", "--exclude-handle", "two", "query"}},
		{name: "x at handle", args: []string{"x", "--allow-handle", "@one", "query"}},
		{name: "x whitespace handle", args: []string{"x", "--allow-handle", "one two", "query"}},
		{name: "x invalid date", args: []string{"x", "--from", "2026-02-30", "query"}},
		{name: "x unpadded date", args: []string{"x", "--to=2026-2-03", "query"}},
		{name: "x inverted dates", args: []string{"x", "--from", "2026-02-04", "--to", "2026-02-03", "query"}},
		{name: "wrong type web filter", args: []string{"x", "--allow-domain", "a.example", "query"}},
		{name: "wrong type x filter", args: []string{"web", "--allow-handle", "one", "query"}},
		{name: "duplicate date", args: []string{"x", "--from", "2026-02-03", "--from=2026-02-04", "query"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loaded := false
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				loaded = true
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = true
			exitCode, stdout, stderr := run(runtime, test.args, "")
			if exitCode != 2 || stdout != usageJSON || stderr != "" || loaded {
				t.Fatalf("exit=%d stdout=%q stderr=%q loaded=%t", exitCode, stdout, stderr, loaded)
			}
		})
	}
}

func TestFiltersNormalizeAndForwardWithRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, request search.Request)
	}{
		{
			name: "web domains normalize and deduplicate",
			args: []string{"web", "--allow-domain", "EXAMPLE.com", "--allow-domain=example.COM", "--allow-domain", "docs.example.com", "query"},
			check: func(t *testing.T, request search.Request) {
				if request.WebFilters == nil || request.XFilters != nil || strings.Join(request.WebFilters.AllowedDomains, ",") != "example.com,docs.example.com" || len(request.WebFilters.ExcludedDomains) != 0 {
					t.Fatalf("filters = %#v", request)
				}
			},
		},
		{
			name: "x handles and dates preserve spelling",
			args: []string{"x", "--exclude-handle", "One", "--exclude-handle=One", "--exclude-handle", "二号", "--from", "2026-02-03", "--to=2026-02-04", "query"},
			check: func(t *testing.T, request search.Request) {
				if request.XFilters == nil || request.WebFilters != nil || strings.Join(request.XFilters.ExcludedHandles, ",") != "One,二号" || request.XFilters.FromDate != "2026-02-03" || request.XFilters.ToDate != "2026-02-04" {
					t.Fatalf("filters = %#v", request)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return auth.Credential{}, nil
			})
			runtime.StdinIsTerminal = true
			runtime.Search = func(_ context.Context, request search.Request, _ auth.Credential) (search.Response, *search.Error) {
				test.check(t, request)
				return search.Response{SchemaVersion: search.SchemaVersion, Type: request.Type, Query: request.Query, Model: request.Model, Answer: "ok", Sources: []search.Source{}, Warnings: []search.Warning{}}, nil
			}
			if exitCode, _, stderr := run(runtime, test.args, ""); exitCode != 0 || stderr != "" {
				t.Fatalf("exit=%d stderr=%q", exitCode, stderr)
			}
		})
	}
}

func TestDocumentedStdinInvocationsAndResponseParsingStayOffline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		args  []string
		check func(t *testing.T, request search.Request)
	}{
		{
			name: "web with requested model and domain",
			args: []string{"web", "--model", "grok-4.5", "--allow-domain", "docs.example.test"},
			check: func(t *testing.T, request search.Request) {
				if request.Type != search.TypeWeb || request.Query != "find current docs" || request.WebFilters == nil || strings.Join(request.WebFilters.AllowedDomains, ",") != "docs.example.test" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
		{
			name: "x with handle and date",
			args: []string{"x", "--allow-handle", "OpenAI", "--from", "2026-02-03"},
			check: func(t *testing.T, request search.Request) {
				if request.Type != search.TypeX || request.Query != "find current posts" || request.XFilters == nil || strings.Join(request.XFilters.AllowedHandles, ",") != "OpenAI" || request.XFilters.FromDate != "2026-02-03" {
					t.Fatalf("request = %#v", request)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return auth.Credential{}, nil
			})
			runtime.Search = func(_ context.Context, request search.Request, _ auth.Credential) (search.Response, *search.Error) {
				test.check(t, request)
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          request.Type,
					Query:         request.Query,
					Model:         request.Model,
					Answer:        "offline answer",
					Sources:       []search.Source{{URL: "https://docs.example.test/"}},
					Warnings:      []search.Warning{search.WarningInvalidCitationsIgnored},
				}, nil
			}
			exitCode, stdout, stderr := run(runtime, test.args, map[string]string{
				"web with requested model and domain": "find current docs",
				"x with handle and date":              "find current posts",
			}[test.name])
			if exitCode != 0 || stderr != "" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
			}
			var result struct {
				Answer   string           `json:"answer"`
				Sources  []search.Source  `json:"sources"`
				Warnings []search.Warning `json:"warnings"`
			}
			if err := json.Unmarshal([]byte(stdout), &result); err != nil || result.Answer != "offline answer" || len(result.Sources) != 1 || result.Sources[0].URL != "https://docs.example.test/" || len(result.Warnings) != 1 || result.Warnings[0] != search.WarningInvalidCitationsIgnored {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestVerboseDiagnosticsAreAllowlistedAndNeverChangeStdout(t *testing.T) {
	t.Parallel()

	credential := auth.Credential{AccessToken: "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE"}
	const query = "SENTINEL_QUERY_DO_NOT_PRINT"

	tests := []struct {
		name         string
		searchFunc   func(context.Context, search.Request, auth.Credential) (search.Response, *search.Error)
		wantPhases   []string
		wantCodes    []string
		wantEvidence []string
	}{
		{
			name: "success",
			searchFunc: func(_ context.Context, request search.Request, _ auth.Credential) (search.Response, *search.Error) {
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          request.Type,
					Query:         request.Query,
					Model:         request.Model,
					Answer:        "SENTINEL_ANSWER_DO_NOT_PRINT",
					Sources:       []search.Source{},
					Warnings:      []search.Warning{},
				}, nil
			},
			wantPhases: []string{"parse", "auth", "search"},
			wantCodes:  []string{"ok", "ok", "ok"},
		},
		{
			name: "search error",
			searchFunc: func(_ context.Context, _ search.Request, _ auth.Credential) (search.Response, *search.Error) {
				return search.Response{}, search.NewError(search.ErrorRateLimited).WithHTTPStatus(429)
			},
			wantPhases:   []string{"parse", "auth", "search"},
			wantCodes:    []string{"ok", "ok", "rate_limited"},
			wantEvidence: []string{"http_status=429"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
				return credential, nil
			})
			runtime.StdinIsTerminal = true
			runtime.Search = test.searchFunc

			quietExit, quietStdout, quietStderr := run(runtime, []string{"web", query}, "")
			if quietStderr != "" {
				t.Fatalf("non-verbose stderr = %q, want empty", quietStderr)
			}
			verboseExit, verboseStdout, verboseStderr := run(runtime, []string{"web", "--verbose", query}, "")
			if verboseExit != quietExit || verboseStdout != quietStdout {
				t.Fatalf("verbose changed exit/stdout: exit %d vs %d, stdout %q vs %q", verboseExit, quietExit, verboseStdout, quietStdout)
			}

			lines := strings.Split(strings.TrimSuffix(verboseStderr, "\n"), "\n")
			if len(lines) != len(test.wantPhases) {
				t.Fatalf("stderr = %q, want %d diagnostic lines", verboseStderr, len(test.wantPhases))
			}
			for index, line := range lines {
				evidence := []string{}
				if index == len(test.wantPhases)-1 {
					evidence = test.wantEvidence
				}
				assertDiagnostic(t, line, test.wantPhases[index], test.wantCodes[index], evidence...)
			}
			for _, forbidden := range []string{credential.AccessToken, "Authorization", "citation"} {
				if strings.Contains(verboseStdout, forbidden) {
					t.Fatalf("verbose output leaked %q", forbidden)
				}
			}
			for _, forbidden := range []string{credential.AccessToken, "Authorization", query, "SENTINEL_ANSWER_DO_NOT_PRINT", "citation"} {
				if strings.Contains(verboseStderr, forbidden) {
					t.Fatalf("verbose diagnostics leaked %q", forbidden)
				}
			}
		})
	}
}

func TestVerboseDoesNotChangeUsageErrorOutput(t *testing.T) {
	t.Parallel()

	runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
		t.Fatal("credential loader must not be called")
		return auth.Credential{}, nil
	})
	runtime.StdinIsTerminal = true

	exitCode, stdout, stderr := run(runtime, []string{"web", "--verbose", "--timeout", "1h", "query"}, "")
	if exitCode != 2 || stdout != usageJSON {
		t.Fatalf("exit=%d stdout=%q, want exit 2 stdout %q", exitCode, stdout, usageJSON)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty on usage error", stderr)
	}
}

func assertDiagnostic(t *testing.T, line, wantPhase, wantCode string, wantEvidence ...string) {
	t.Helper()
	fields := strings.Fields(line)
	if len(fields) < 3 || !strings.HasPrefix(fields[0], "phase=") || !strings.HasPrefix(fields[1], "elapsed=") || !strings.HasPrefix(fields[2], "code=") {
		t.Fatalf("diagnostic line = %q", line)
	}
	if fields[0] != "phase="+wantPhase || fields[2] != "code="+wantCode {
		t.Fatalf("diagnostic line = %q, want phase=%s code=%s", line, wantPhase, wantCode)
	}
	if _, err := time.ParseDuration(strings.TrimPrefix(fields[1], "elapsed=")); err != nil {
		t.Fatalf("diagnostic elapsed = %q: %v", fields[1], err)
	}
	if got := fields[3:]; len(got) != len(wantEvidence) {
		t.Fatalf("diagnostic evidence = %v, want %v", got, wantEvidence)
	} else {
		for index := range wantEvidence {
			if got[index] != wantEvidence[index] {
				t.Fatalf("diagnostic evidence = %v, want %v", got, wantEvidence)
			}
		}
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

func TestWebAndXSearchRenderOneNormalizedResponseAndPreserveQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  string
		args     []string
		stdin    string
		terminal bool
		query    string
	}{
		{name: "web piped CRLF", command: "web", args: []string{"web"}, stdin: "find docs\r\n", query: "find docs"},
		{name: "web terminal positional", command: "web", args: []string{"web", "find docs"}, terminal: true, query: "find docs"},
		{name: "web empty stdin falls back to positional", command: "web", args: []string{"web", "find docs"}, query: "find docs"},
		{name: "x piped CRLF", command: "x", args: []string{"x"}, stdin: "find docs\r\n", query: "find docs"},
		{name: "x terminal positional", command: "x", args: []string{"x", "find docs"}, terminal: true, query: "find docs"},
		{name: "x empty stdin falls back to positional", command: "x", args: []string{"x", "find docs"}, query: "find docs"},
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
				wantRequest := search.Request{Type: search.Type(test.command), Query: test.query, Model: "grok-4.6", MaxOutputTokens: 1024}
				if request != wantRequest {
					t.Fatalf("request = %#v", request)
				}
				return search.Response{
					SchemaVersion: search.SchemaVersion,
					Type:          request.Type,
					Query:         request.Query,
					Model:         request.Model,
					ProviderModel: "grok-4.5-build",
					Answer:        "The docs are available.",
					Sources:       []search.Source{{URL: "https://docs.example.test/"}},
					Warnings:      []search.Warning{},
				}, nil
			}

			exitCode, stdout, stderr := run(runtime, test.args, test.stdin)
			want := fmt.Sprintf("{\"schema_version\":1,\"type\":%q,\"query\":%q,\"model\":\"grok-4.6\",\"provider_model\":\"grok-4.5-build\",\"answer\":\"The docs are available.\",\"sources\":[{\"url\":\"https://docs.example.test/\"}],\"warnings\":[]}\n", test.command, test.query)
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

func TestWebAndXShareSearchErrorEnvelopeAndExit(t *testing.T) {
	t.Parallel()

	searchErr := search.NewError(search.ErrorRateLimited).
		WithHTTPStatus(429).
		WithRetryAfterSeconds(0).
		WithRequestID("safe-request-id")
	want := "{\"schema_version\":1,\"error\":{\"code\":\"rate_limited\",\"message\":\"The provider rate-limited the search request.\",\"retryable\":true,\"http_status\":429,\"retry_after_seconds\":0,\"request_id\":\"safe-request-id\"}}\n"

	var outputs []string
	for _, command := range []string{"web", "x"} {
		runtime := testRuntime(func(string, time.Time) (auth.Credential, *search.Error) {
			return auth.Credential{}, nil
		})
		runtime.StdinIsTerminal = true
		runtime.Search = func(_ context.Context, request search.Request, _ auth.Credential) (search.Response, *search.Error) {
			if request.Type != search.Type(command) {
				t.Fatalf("request type = %q, want %q", request.Type, search.Type(command))
			}
			return search.Response{}, searchErr
		}

		exitCode, stdout, stderr := run(runtime, []string{command, "query"}, "")
		if exitCode != 4 || stdout != want || stderr != "" {
			t.Fatalf("%s: exit=%d stdout=%q stderr=%q, want exit 4 stdout %q stderr empty", command, exitCode, stdout, stderr, want)
		}
		outputs = append(outputs, stdout)
	}
	if outputs[0] != outputs[1] {
		t.Fatalf("web and x error envelopes differ: %q vs %q", outputs[0], outputs[1])
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
			runtime.Search = func(_ context.Context, _ search.Request, credential auth.Credential) (search.Response, *search.Error) {
				if credential.AccessToken != "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE" {
					t.Fatalf("unexpected credential")
				}
				return search.Response{}, search.NewError(search.ErrorInternal)
			}
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

func TestSearchErrorExitContractIsOfflineAndDoesNotLeak(t *testing.T) {
	tests := []struct {
		name      string
		searchErr *search.Error
		wantExit  int
		wantJSON  string
	}{
		{
			name: "rate limited carries all verified metadata",
			searchErr: search.NewError(search.ErrorRateLimited).
				WithHTTPStatus(429).
				WithRetryAfterSeconds(0).
				WithRequestID("safe-request-id"),
			wantExit: 4,
			wantJSON: "{\"schema_version\":1,\"error\":{\"code\":\"rate_limited\",\"message\":\"The provider rate-limited the search request.\",\"retryable\":true,\"http_status\":429,\"retry_after_seconds\":0,\"request_id\":\"safe-request-id\"}}\n",
		},
		{
			name:      "auth rejected exits three",
			searchErr: search.NewError(search.ErrorAuthRejected).WithHTTPStatus(401),
			wantExit:  3,
			wantJSON:  "{\"schema_version\":1,\"error\":{\"code\":\"auth_rejected\",\"message\":\"The Grok credential was rejected. Run `grok login`.\",\"retryable\":false,\"http_status\":401}}\n",
		},
		{
			name: "mutable error is rebuilt before output",
			searchErr: &search.Error{
				Code:      search.ErrorProvider,
				Message:   "RAW_PROVIDER_BODY_SENTINEL",
				Retryable: true,
			},
			wantExit: 4,
			wantJSON: "{\"schema_version\":1,\"error\":{\"code\":\"provider_error\",\"message\":\"The provider could not complete the search request.\",\"retryable\":false}}\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := writeStore(t, directory, "auth.json", validStore(t, "oidc", fixedNow.Add(-time.Hour), fixedNow.Add(time.Hour).Format(time.RFC3339)), 0o600)

			const query = "SENTINEL_QUERY_DO_NOT_PRINT"
			runtime := cli.DefaultRuntime()
			runtime.Now = func() time.Time { return fixedNow }
			runtime.StdinIsTerminal = true
			runtime.Search = func(_ context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error) {
				if credential.AccessToken != "TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE" {
					t.Fatalf("unexpected credential")
				}
				if request.Query != query {
					t.Fatalf("query = %q, want %q", request.Query, query)
				}
				return search.Response{}, test.searchErr
			}

			exitCode, stdout, stderr := run(runtime, []string{"web", "--auth-file", path, query}, "")
			if exitCode != test.wantExit {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want exit %d", exitCode, stdout, stderr, test.wantExit)
			}
			if stdout != test.wantJSON {
				t.Fatalf("stdout = %q, want %q", stdout, test.wantJSON)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
			for _, forbidden := range []string{"TEST_ONLY_ACCESS_SENTINEL_DO_NOT_USE", "TEST_ONLY_REFRESH_SENTINEL_DO_NOT_USE", "Authorization", path, query, "RAW_PROVIDER_BODY_SENTINEL"} {
				if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
					t.Fatalf("output leaked %q", forbidden)
				}
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

func TestProcessLevelWebAndXShareGoldenSchemaAndExit(t *testing.T) {
	t.Parallel()

	binary := buildCLIBinary(t)
	authMissingJSON := "{\"schema_version\":1,\"error\":{\"code\":\"auth_missing\",\"message\":\"No valid Grok credential was found. Run `grok login`.\",\"retryable\":false}}\n"

	tests := []struct {
		name     string
		args     []string
		env      func(t *testing.T) []string
		wantExit int
		wantJSON string
	}{
		{
			name:     "usage",
			args:     []string{},
			wantExit: 2,
			wantJSON: usageJSON,
		},
		{
			name:     "auth missing",
			args:     []string{"query"},
			wantExit: 3,
			wantJSON: authMissingJSON,
			env: func(t *testing.T) []string {
				env := []string{}
				for _, entry := range os.Environ() {
					if !strings.HasPrefix(entry, "GROK_HOME=") {
						env = append(env, entry)
					}
				}
				return append(env, "GROK_HOME="+t.TempDir())
			},
		},
		{
			name:     "out of range timeout is usage before auth",
			args:     []string{"--timeout", "1h", "query"},
			wantExit: 2,
			wantJSON: usageJSON,
		},
	}

	for _, command := range []string{"web", "x"} {
		for _, test := range tests {
			t.Run(command+"/"+test.name, func(t *testing.T) {
				argv := append([]string{command}, test.args...)
				cmd := exec.Command(binary, argv...)
				cmd.Stdin = strings.NewReader("")
				if test.env != nil {
					cmd.Env = test.env(t)
				}
				var stdout bytes.Buffer
				var stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil {
					var exitError *exec.ExitError
					if !errors.As(err, &exitError) {
						t.Fatalf("run %v: %v", argv, err)
					}
				}
				exitCode := cmd.ProcessState.ExitCode()
				if exitCode != test.wantExit || stdout.String() != test.wantJSON || stderr.String() != "" {
					t.Fatalf("command %v: exit=%d stdout=%q stderr=%q, want exit %d stdout %q stderr empty", argv, exitCode, stdout.String(), stderr.String(), test.wantExit, test.wantJSON)
				}
			})
		}
	}
}

func TestProcessLevelVerboseDiagnosticsAreOfflineAndSanitized(t *testing.T) {
	t.Parallel()

	binary := buildCLIBinary(t)
	authPath := filepath.Join(t.TempDir(), "missing.json")
	const query = "SENTINEL_PROCESS_QUERY_DO_NOT_PRINT"

	cmd := exec.Command(binary, "web", "--verbose", "--auth-file", authPath, query)
	cmd.Stdin = strings.NewReader("")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run: %v", err)
		}
		exitCode = exitError.ExitCode()
	}

	want := "{\"schema_version\":1,\"error\":{\"code\":\"auth_missing\",\"message\":\"No valid Grok credential was found. Run `grok login`.\",\"retryable\":false}}\n"
	if exitCode != 3 || stdout.String() != want {
		t.Fatalf("exit=%d stdout=%q, want exit 3 stdout %q", exitCode, stdout.String(), want)
	}
	if !strings.Contains(stderr.String(), "phase=auth") || !strings.Contains(stderr.String(), "code=auth_missing") {
		t.Fatalf("stderr = %q, want auth_missing diagnostic", stderr.String())
	}
	for _, forbidden := range []string{authPath, query, "Authorization", "SENTINEL"} {
		if strings.Contains(stdout.String(), forbidden) || strings.Contains(stderr.String(), forbidden) {
			t.Fatalf("process output leaked %q", forbidden)
		}
	}
}

func buildCLIBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grok-search")
	command := exec.Command("go", "build", "-o", path, "./cmd/grok-search")
	command.Dir = filepath.Join("..", "..")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return path
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
