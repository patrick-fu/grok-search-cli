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
	updatecli "github.com/patrick-fu/grok-search-cli/internal/update"
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
	maxModelBytes = 256
)

type Runtime struct {
	Version         string
	StdinIsTerminal bool
	Now             func() time.Time
	LoadCredential  func(path string, now time.Time) (auth.Credential, *search.Error)
	Search          func(ctx context.Context, request search.Request, credential auth.Credential) (search.Response, *search.Error)
	Update          func(ctx context.Context, version string) (updatecli.Result, error)
}

type searchOptions struct {
	authPath        string
	model           string
	allowDomains    []string
	excludeDomains  []string
	allowHandles    []string
	excludeHandles  []string
	fromDate        string
	toDate          string
	positional      []string
	wantsHelp       bool
	timeout         time.Duration
	maxOutputTokens int
	verbose         bool
}

func defaultSearchOptions() searchOptions {
	return searchOptions{
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
	if len(args) > 0 && args[0] == "update" {
		if len(args) != 1 {
			return 2
		}
		updater := runtime.Update
		if updater == nil {
			updater = func(ctx context.Context, version string) (updatecli.Result, error) {
				return updatecli.Apply(ctx, updatecli.Config{CurrentVersion: version})
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		result, err := updater(ctx, runtime.Version)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "update failed: %s\n", err)
			return 4
		}
		if !result.Updated {
			return writeHuman(stdout, fmt.Sprintf("grok-search %s is already up to date.\n", result.CurrentVersion))
		}
		return writeHuman(stdout, fmt.Sprintf("Updated grok-search from %s to %s.\n", result.CurrentVersion, result.LatestVersion))
	}
	if len(args) == 0 || (args[0] != "web" && args[0] != "x") {
		return writeSearchError(stdout, search.NewError(search.ErrorUsage))
	}

	started := time.Now()
	searchType := search.Type(args[0])
	options, parseOK := parseSearchArgs(searchType, args[1:])
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
	request := search.Request{
		Type:            searchType,
		Query:           query,
		Model:           options.model,
		MaxOutputTokens: options.maxOutputTokens,
	}
	if searchType == search.TypeWeb && (len(options.allowDomains) > 0 || len(options.excludeDomains) > 0) {
		request.WebFilters = &search.WebFilters{
			AllowedDomains:  append([]string(nil), options.allowDomains...),
			ExcludedDomains: append([]string(nil), options.excludeDomains...),
		}
	}
	if searchType == search.TypeX && (len(options.allowHandles) > 0 || len(options.excludeHandles) > 0 || options.fromDate != "" || options.toDate != "") {
		request.XFilters = &search.XFilters{
			AllowedHandles:  append([]string(nil), options.allowHandles...),
			ExcludedHandles: append([]string(nil), options.excludeHandles...),
			FromDate:        options.fromDate,
			ToDate:          options.toDate,
		}
	}
	response, searchErr := runtime.Search(ctx, request, credential)
	if searchErr != nil {
		emitSearchErrorDiagnostic(stderr, options.verbose, "search", started, searchErr)
		return writeSearchError(stdout, searchErr)
	}
	emitDiagnostic(stderr, options.verbose, "search", started, "ok")
	return writeSearchResponse(stdout, response)
}

func parseSearchArgs(searchType search.Type, args []string) (searchOptions, bool) {
	options := defaultSearchOptions()
	seenAuthPath := false
	seenTimeout := false
	seenMaxOutputTokens := false
	seenVerbose := false
	seenModel := false
	seenFrom := false
	seenTo := false
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
		case argument == "--allow-domain" || argument == "--exclude-domain":
			if searchType != search.TypeWeb || index+1 >= len(args) {
				return searchOptions{}, false
			}
			domain, ok := validDomain(args[index+1])
			if !ok {
				return searchOptions{}, false
			}
			index++
			if argument == "--allow-domain" {
				if options.allowDomains, ok = appendUnique(options.allowDomains, domain, 5); !ok {
					return searchOptions{}, false
				}
			} else {
				if options.excludeDomains, ok = appendUnique(options.excludeDomains, domain, 5); !ok {
					return searchOptions{}, false
				}
			}
		case strings.HasPrefix(argument, "--allow-domain=") || strings.HasPrefix(argument, "--exclude-domain="):
			if searchType != search.TypeWeb {
				return searchOptions{}, false
			}
			isAllow := strings.HasPrefix(argument, "--allow-domain=")
			value := strings.TrimPrefix(argument, "--exclude-domain=")
			if isAllow {
				value = strings.TrimPrefix(argument, "--allow-domain=")
			}
			domain, ok := validDomain(value)
			if !ok {
				return searchOptions{}, false
			}
			if isAllow {
				if options.allowDomains, ok = appendUnique(options.allowDomains, domain, 5); !ok {
					return searchOptions{}, false
				}
			} else {
				if options.excludeDomains, ok = appendUnique(options.excludeDomains, domain, 5); !ok {
					return searchOptions{}, false
				}
			}
		case argument == "--allow-handle" || argument == "--exclude-handle":
			if searchType != search.TypeX || index+1 >= len(args) {
				return searchOptions{}, false
			}
			handle, ok := validHandle(args[index+1])
			if !ok {
				return searchOptions{}, false
			}
			index++
			if argument == "--allow-handle" {
				if options.allowHandles, ok = appendUnique(options.allowHandles, handle, 20); !ok {
					return searchOptions{}, false
				}
			} else {
				if options.excludeHandles, ok = appendUnique(options.excludeHandles, handle, 20); !ok {
					return searchOptions{}, false
				}
			}
		case strings.HasPrefix(argument, "--allow-handle=") || strings.HasPrefix(argument, "--exclude-handle="):
			if searchType != search.TypeX {
				return searchOptions{}, false
			}
			isAllow := strings.HasPrefix(argument, "--allow-handle=")
			value := strings.TrimPrefix(argument, "--exclude-handle=")
			if isAllow {
				value = strings.TrimPrefix(argument, "--allow-handle=")
			}
			handle, ok := validHandle(value)
			if !ok {
				return searchOptions{}, false
			}
			if isAllow {
				if options.allowHandles, ok = appendUnique(options.allowHandles, handle, 20); !ok {
					return searchOptions{}, false
				}
			} else {
				if options.excludeHandles, ok = appendUnique(options.excludeHandles, handle, 20); !ok {
					return searchOptions{}, false
				}
			}
		case argument == "--from" || argument == "--to":
			if searchType != search.TypeX || index+1 >= len(args) {
				return searchOptions{}, false
			}
			value, ok := validDate(args[index+1])
			if !ok || (argument == "--from" && seenFrom) || (argument == "--to" && seenTo) {
				return searchOptions{}, false
			}
			index++
			if argument == "--from" {
				seenFrom = true
				options.fromDate = value
			} else {
				seenTo = true
				options.toDate = value
			}
		case strings.HasPrefix(argument, "--from=") || strings.HasPrefix(argument, "--to="):
			if searchType != search.TypeX {
				return searchOptions{}, false
			}
			isFrom := strings.HasPrefix(argument, "--from=")
			value := strings.TrimPrefix(argument, "--to=")
			if isFrom {
				value = strings.TrimPrefix(argument, "--from=")
			}
			value, ok := validDate(value)
			if !ok || (isFrom && seenFrom) || (!isFrom && seenTo) {
				return searchOptions{}, false
			}
			if isFrom {
				seenFrom = true
				options.fromDate = value
			} else {
				seenTo = true
				options.toDate = value
			}
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
			return options, validFilterCombinations(options)
		case strings.HasPrefix(argument, "-"):
			return searchOptions{}, false
		default:
			options.positional = append(options.positional, argument)
			if len(options.positional) > 1 {
				return searchOptions{}, false
			}
		}
	}
	return options, validFilterCombinations(options)
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

func validDomain(value string) (string, bool) {
	if value == "" || len(value) > 253 || strings.ContainsAny(value, ":/?#@*\\") {
		return "", false
	}
	value = strings.ToLower(value)
	labels := strings.Split(value, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return "", false
			}
		}
	}
	return value, true
}

func validFilterCombinations(options searchOptions) bool {
	return !(len(options.allowDomains) > 0 && len(options.excludeDomains) > 0) &&
		!(len(options.allowHandles) > 0 && len(options.excludeHandles) > 0) &&
		!(options.fromDate != "" && options.toDate != "" && options.fromDate > options.toDate)
}

func validHandle(value string) (string, bool) {
	if value == "" || len(value) > 64 || !utf8.ValidString(value) || strings.Contains(value, "@") {
		return "", false
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", false
		}
	}
	return value, true
}

func validDate(value string) (string, bool) {
	if len(value) != len("2006-01-02") {
		return "", false
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return "", false
	}
	return value, true
}

func appendUnique(values []string, value string, limit int) ([]string, bool) {
	for _, existing := range values {
		if existing == value {
			return values, true
		}
	}
	if len(values) >= limit {
		return nil, false
	}
	return append(values, value), true
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
  grok-search update

Default model: first Responses search model from the provider catalog
`
