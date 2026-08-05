package main

import (
	"os"

	"github.com/patrick-fu/grok-search-cli/internal/cli"
)

var version = "dev"

func main() {
	runtime := cli.DefaultRuntime()
	runtime.Version = version
	runtime.StdinIsTerminal = stdinIsTerminal(os.Stdin)
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, runtime))
}

func stdinIsTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
