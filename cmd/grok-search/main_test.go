package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBinaryProcessContractIsOfflineAndFailClosed(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	binaryName := "grok-search-test"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-ldflags", "-X main.version=0.0.0-test", "-o", binaryPath, "./cmd/grok-search")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, output)
	}

	t.Run("version", func(t *testing.T) {
		exitCode, stdout, stderr := runBinary(t, binaryPath, "", "version")
		if exitCode != 0 || stdout != "grok-search 0.0.0-test\n" || stderr != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
		}
	})

	t.Run("usage precedes auth", func(t *testing.T) {
		exitCode, stdout, stderr := runBinary(t, binaryPath, "", "web", "--auth-file", filepath.Join(t.TempDir(), "missing"))
		if exitCode != 2 || !strings.Contains(stdout, `"code":"usage_error"`) || stderr != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
		}
	})

	t.Run("missing auth", func(t *testing.T) {
		exitCode, stdout, stderr := runBinary(t, binaryPath, "", "web", "--auth-file", filepath.Join(t.TempDir(), "missing"), "query")
		if exitCode != 3 || !strings.Contains(stdout, `"code":"auth_missing"`) || stderr != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
		}
	})

	t.Run("valid auth never reaches a network path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "auth.json")
		store := map[string]any{
			"production": map[string]any{
				"key":           "BINARY_TEST_ACCESS_SENTINEL_DO_NOT_USE",
				"refresh_token": "BINARY_TEST_REFRESH_SENTINEL_DO_NOT_USE",
				"auth_mode":     "oidc",
				"oidc_issuer":   "https://auth.x.ai",
				"create_time":   time.Now().Add(-time.Hour).Format(time.RFC3339),
				"expires_at":    time.Now().Add(time.Hour).Format(time.RFC3339),
			},
		}
		data, err := json.Marshal(store)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}

		exitCode, stdout, stderr := runBinary(t, binaryPath, "", "x", "--auth-file", path, "query")
		if exitCode != 70 || !strings.Contains(stdout, `"code":"internal_error"`) || stderr != "" {
			t.Fatalf("exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
		}
		for _, forbidden := range []string{"BINARY_TEST_ACCESS_SENTINEL_DO_NOT_USE", "BINARY_TEST_REFRESH_SENTINEL_DO_NOT_USE", "Authorization", path} {
			if strings.Contains(stdout, forbidden) || strings.Contains(stderr, forbidden) {
				t.Fatalf("process output leaked %q", forbidden)
			}
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, after) {
			t.Fatal("credential bytes changed")
		}
	})
}

func runBinary(t *testing.T, binaryPath, stdin string, args ...string) (int, string, string) {
	t.Helper()
	command := exec.Command(binaryPath, args...)
	command.Stdin = strings.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !strings.Contains(err.Error(), "exit status") || !errors.As(err, &exitError) {
			t.Fatalf("run binary: %v", err)
		}
		exitCode = exitError.ExitCode()
	}
	return exitCode, stdout.String(), stderr.String()
}
