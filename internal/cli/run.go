package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/patrick-fu/grok-search-cli/internal/auth"
	"github.com/patrick-fu/grok-search-cli/internal/grok"
	"github.com/patrick-fu/grok-search-cli/internal/search"
)

const (
	maxQueryBytes = 64 << 10

	defaultTimeout         = 90 * time.Second
	minTimeout             = time.Second
	maxTimeout             = 180 * time.Second
	defaultMaxOutputTokens = 1024
	minMaxOutputTokens     = 128
	maxMaxOutputTokens     = 4096
)

const (
	defaultModel  = "grok-4.5"
	maxModelBytes = 256
)

type Runtime struct {
	Version         string
	StdinIsTerminal bool
	Now             func() time.Time
	LoadCredential  func(path string, now time.Time) (auth.Credential, *search.Error)
	Search          func(ctx context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error)
}

type searchOptions struct {
	authPath        string
	model           string
	positional      []string
	wantsHelp       bool
	timeout         time.Duration
	maxOutputTokens int
	verbose         bool
}

func defaultSearchOptions() searchOptions {
	return searchOptions{
		model:           defaultModel,
		timeout:         defaultTimeout,
		maxOutputTokens: defaultMaxOutputTokens,
	}
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

	started := time.Now()
	options, parseOK := parseSearchArgs(args[1:])
	if !parseOK {
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}
	if options.wantsHelp {
		return writeHuman(stdout, helpText)
	}
	query, ok := readQuery(options.positional, stdin, runtime.StdinIsTerminal)
	if !ok {
		emitDiagnostic(stderr, options.verbose, "parse", started, "usage_error")
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}
	emitDiagnostic(stderr, options.verbose, "parse", started, "ok")

	if options.authPath == "" {
		var ok bool
		options.authPath, ok = defaultAuthPath()
		if !ok {
			emitDiagnostic(stderr, options.verbose, "auth", started, string(search.ErrorAuthMissing))
			return writeSearchError(stdout, search.NewError(search.ErrorAuthMissing))
		}
	}
	if runtime.Now == nil || runtime.LoadCredential == nil {
		emitDiagnostic(stderr, options.verbose, "auth", started, string(search.ErrorInternal))
		return writeSearchError(stdout, search.NewError(search.ErrorInternal))
	}
	credential, loadErr := runtime.LoadCredential(options.authPath, runtime.Now())
	if loadErr != nil {
		emitSearchErrorDiagnostic(stderr, options.verbose, "auth", started, loadErr)
		return writeSearchError(stdout, loadErr)
	}
	emitDiagnostic(stderr, options.verbose, "auth", started, "ok")

	if runtime.Search == nil {
		version := runtime.Version
		runtime.Search = func(ctx context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error) {
			return grok.NewClient(credential.AccessToken, version, nil).Search(ctx, request)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), options.timeout)
	defer cancel()
	response, searchErr := runtime.Search(ctx, search.Request{
		Type:            search.Type(args[0]),
		Query:           query,
		Model:           options.model,
		MaxOutputTokens: options.maxOutputTokens,
	}, credential)
	if searchErr != nil {
		emitSearchErrorDiagnostic(stderr, options.verbose, "search", started, searchErr)
		return writeSearchError(stdout, searchErr)
	}
	emitDiagnostic(stderr, options.verbose, "search", started, "ok")
	return writeSearchResponse(stdout, response)
}

func parseSearchArgs(args []string) (searchOptions, bool) {
	options := defaultSearchOptions()
	seenAuthPath := false
	seenTimeout := false
	seenMaxOutputTokens := false
	seenVerbose := false
	seenModel := false
	for index := 0; index < len(args); index++ {
		argument := args[index]
		switch {
		case argument == "--help" || argument == "-h":
			if len(args) != 1 {
				return searchOptions{}, false
			}
			options.wantsHelp = true
			return options, true
		case argument == "--auth-file":
			if seenAuthPath || index+1 >= len(args) || args[index+1] == "" || strings.HasPrefix(args[index+1], "-") {
				return searchOptions{}, false
			}
			seenAuthPath = true
			index++
			options.authPath = args[index]
		case strings.HasPrefix(argument, "--auth-file="):
			if seenAuthPath {
				return searchOptions{}, false
			}
			seenAuthPath = true
			options.authPath = strings.TrimPrefix(argument, "--auth-file=")
			if options.authPath == "" {
				return searchOptions{}, false
			}
		case argument == "--timeout":
			if seenTimeout || index+1 >= len(args) {
				return searchOptions{}, false
			}
			timeout, ok := parseTimeout(args[index+1])
			if !ok {
				return searchOptions{}, false
			}
			seenTimeout = true
			index++
			options.timeout = timeout
		case strings.HasPrefix(argument, "--timeout="):
			if seenTimeout {
				return searchOptions{}, false
			}
			timeout, ok := parseTimeout(strings.TrimPrefix(argument, "--timeout="))
			if !ok {
				return searchOptions{}, false
			}
			seenTimeout = true
			options.timeout = timeout
		case argument == "--max-output-tokens":
			if seenMaxOutputTokens || index+1 >= len(args) {
				return searchOptions{}, false
			}
			tokens, ok := parseMaxOutputTokens(args[index+1])
			if !ok {
				return searchOptions{}, false
			}
			seenMaxOutputTokens = true
			index++
			options.maxOutputTokens = tokens
		case strings.HasPrefix(argument, "--max-output-tokens="):
			if seenMaxOutputTokens {
				return searchOptions{}, false
			}
			tokens, ok := parseMaxOutputTokens(strings.TrimPrefix(argument, "--max-output-tokens="))
			if !ok {
				return searchOptions{}, false
			}
			seenMaxOutputTokens = true
			options.maxOutputTokens = tokens
		case argument == "--model":
			if seenModel || index+1 >= len(args) || !validModel(args[index+1]) {
				return searchOptions{}, false
			}
			seenModel = true
			index++
			options.model = args[index]
		case strings.HasPrefix(argument, "--model="):
			if seenModel {
				return searchOptions{}, false
			}
			model := strings.TrimPrefix(argument, "--model=")
			if !validModel(model) {
				return searchOptions{}, false
			}
			seenModel = true
			options.model = model
		case argument == "--verbose":
			if seenVerbose {
				return searchOptions{}, false
			}
			seenVerbose = true
			options.verbose = true
		case argument == "--":
			if index+2 != len(args) {
				return searchOptions{}, false
			}
			options.positional = append(options.positional, args[index+1])
			return options, true
		case strings.HasPrefix(argument, "-"):
			return searchOptions{}, false
		default:
			options.positional = append(options.positional, argument)
			if len(options.positional) > 1 {
				return searchOptions{}, false
			}
		}
	}
	return options, true
}

func parseTimeout(value string) (time.Duration, bool) {
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout < minTimeout || timeout > maxTimeout {
		return 0, false
	}
	return timeout, true
}

func parseMaxOutputTokens(value string) (int, bool) {
	tokens, err := strconv.Atoi(value)
	if err != nil || tokens < minMaxOutputTokens || tokens > maxMaxOutputTokens {
		return 0, false
	}
	return tokens, true
}

func validModel(value string) bool {
	if value == "" || len(value) > maxModelBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

// emitDiagnostic writes one allowlisted diagnostic line to stderr. A line
// carries only the phase name, elapsed time, and a stable code plus optional
// allowlisted evidence; it never carries query, credential, path, body,
// answer, or citation content.
func emitDiagnostic(writer io.Writer, verbose bool, phase string, started time.Time, code string, evidence ...string) {
	if !verbose || writer == nil {
		return
	}
	var line strings.Builder
	line.WriteString("phase=")
	line.WriteString(phase)
	line.WriteString(" elapsed=")
	line.WriteString(time.Since(started).String())
	line.WriteString(" code=")
	line.WriteString(code)
	for _, item := range evidence {
		line.WriteByte(' ')
		line.WriteString(item)
	}
	line.WriteByte('\n')
	_, _ = io.WriteString(writer, line.String())
}

func emitSearchErrorDiagnostic(writer io.Writer, verbose bool, phase string, started time.Time, searchErr *search.Error) {
	if !verbose || writer == nil || searchErr == nil {
		return
	}
	sanitized := searchErr.Sanitized()
	evidence := []string{}
	if sanitized.HTTPStatus != 0 {
		evidence = append(evidence, fmt.Sprintf("http_status=%d", sanitized.HTTPStatus))
	}
	emitDiagnostic(writer, true, phase, started, string(sanitized.Code), evidence...)
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
  grok-search web [--model <id>] [--auth-file <path>] [--timeout <duration>] [--max-output-tokens <n>] [--verbose] [query]
  grok-search x [--model <id>] [--auth-file <path>] [--timeout <duration>] [--max-output-tokens <n>] [--verbose] [query]
  grok-search help
  grok-search version
`
