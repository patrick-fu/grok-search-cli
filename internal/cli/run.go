package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/patrick-fu/grok-search-cli/internal/auth"
	"github.com/patrick-fu/grok-search-cli/internal/grok"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const maxQueryBytes = 64 << 10

type Runtime struct {
	Version         string
	StdinIsTerminal bool
	Now             func() time.Time
	LoadCredential  func(path string, now time.Time) (auth.Credential, *search.Error)
	Search          func(ctx context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error)
}

func DefaultRuntime() Runtime {
	loader := auth.NewLoader()
	return Runtime{
		Version:        "dev",
		Now:            time.Now,
		LoadCredential: loader.Load,
	}
}

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer, runtime Runtime) int {
	_ = stderr
	if runtime.Version == "" {
		runtime.Version = "dev"
	}

	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		return writeHuman(stdout, helpText)
	}
	if len(args) == 1 && (args[0] == "version" || args[0] == "--version") {
		return writeHuman(stdout, fmt.Sprintf("grok-search %s\n", runtime.Version))
	}
	if len(args) == 0 || (args[0] != "web" && args[0] != "x") {
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}

	authPath, positional, wantsHelp, parseOK := parseSearchArgs(args[1:])
	if !parseOK {
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}
	if wantsHelp {
		return writeHuman(stdout, helpText)
	}
	query, ok := readQuery(positional, stdin, runtime.StdinIsTerminal)
	if !ok {
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}

	if authPath == "" {
		var ok bool
		authPath, ok = defaultAuthPath()
		if !ok {
			return writeSearchError(stdout, search.NewError(search.ErrorAuthMissing))
		}
	}
	if runtime.Now == nil || runtime.LoadCredential == nil {
		return writeSearchError(stdout, search.NewError(search.ErrorInternal))
	}
	credential, loadErr := runtime.LoadCredential(authPath, runtime.Now())
	if loadErr != nil {
		return writeSearchError(stdout, loadErr)
	}

	if runtime.Search == nil {
		version := runtime.Version
		runtime.Search = func(ctx context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error) {
			return grok.NewClient(credential.AccessToken, version, nil).Search(ctx, request)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	response, searchErr := runtime.Search(ctx, search.Request{
		Type:  search.Type(args[0]),
		Query: query,
		Model: "grok-4.5",
	}, credential)
	if searchErr != nil {
		return writeSearchError(stdout, searchErr)
	}
	return writeSearchResponse(stdout, response)
}

func parseSearchArgs(args []string) (authPath string, positional []string, wantsHelp bool, ok bool) {
	seenAuthPath := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--help" || argument == "-h":
			if len(args) != 1 {
				return "", nil, false, false
			}
			return "", nil, true, true
		case argument == "--auth-file":
			if seenAuthPath || index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
				return "", nil, false, false
			}
			seenAuthPath = true
			index++
			authPath = args[index]
		case strings.HasPrefix(argument, "--auth-file="):
			if seenAuthPath {
				return "", nil, false, false
			}
			seenAuthPath = true
			authPath = strings.TrimPrefix(argument, "--auth-file=")
			if authPath == "" {
				return "", nil, false, false
			}
		case argument == "--":
			if index+2 != len(args) {
				return "", nil, false, false
			}
			positional = append(positional, args[index+1])
			return authPath, positional, false, true
		case strings.HasPrefix(argument, "-"):
			return "", nil, false, false
		default:
			positional = append(positional, argument)
			if len(positional) > 1 {
				return "", nil, false, false
			}
		}
	}
	return authPath, positional, false, true
}

func readQuery(positional []string, stdin io.Reader, stdinIsTerminal bool) (string, bool) {
	if stdinIsTerminal {
		if len(positional) != 1 {
			return "", false
		}
		return validateQuery(positional[0])
	}

	data, err := io.ReadAll(io.LimitReader(stdin, maxQueryBytes+1))
	if err != nil || len(data) > maxQueryBytes {
		return "", false
	}
	if len(data) > 0 && len(positional) > 0 {
		return "", false
	}
	if len(data) == 0 {
		if len(positional) != 1 {
			return "", false
		}
		return validateQuery(positional[0])
	}

	if data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
		if len(data) > 0 && data[len(data)-1] == '\r' {
			data = data[:len(data)-1]
		}
	}
	return validateQuery(string(data))
}

func validateQuery(query string) (string, bool) {
	if len(query) == 0 || len(query) > maxQueryBytes || !utf8.ValidString(query) || strings.TrimSpace(query) == "" {
		return "", false
	}
	return query, true
}

func defaultAuthPath() (string, bool) {
	if grokHome := os.Getenv("GROK_HOME"); grokHome != "" {
		return filepath.Join(grokHome, "auth.json"), true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".grok", "auth.json"), true
}

type errorEnvelope struct {
	SchemaVersion int           `json:"schema_version"`
	Error         *search.Error `json:"error"`
}

func writeSearchError(stdout io.Writer, searchErr *search.Error) int {
	searchErr = searchErr.Sanitized()
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(errorEnvelope{SchemaVersion: 1, Error: searchErr}); err != nil {
		return 70
	}
	return searchErr.ExitCode()
}

func writeSearchResponse(stdout io.Writer, response search.Response) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		return 70
	}
	return 0
}

func writeHuman(stdout io.Writer, value string) int {
	if _, err := io.WriteString(stdout, value); err != nil {
		return 70
	}
	return 0
}

const helpText = `grok-search provides agent-friendly Grok Web and X search.

Usage:
  grok-search web [--auth-file <path>] [query]
  grok-search x [--auth-file <path>] [query]
  grok-search help
  grok-search version
`
