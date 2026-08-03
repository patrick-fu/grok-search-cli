package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

//go:embed fixtures/*.json
var fixtures embed.FS

type scenario struct {
	Name    string
	File    string
	Request SearchRequest
}

var scenarios = []scenario{
	{Name: "web", File: "web.json", Request: SearchRequest{Type: "web", Query: "Find the Web and X Search documentation.", Model: "grok-4.5"}},
	{Name: "x", File: "x.json", Request: SearchRequest{Type: "x", Query: "Find two public X posts about Grok Build.", Model: "grok-4.5"}},
	{Name: "missing-citations", File: "missing-citations.json", Request: SearchRequest{Type: "web", Query: "Search, then answer without a citation.", Model: "grok-4.5"}},
	{Name: "invalid-citation", File: "invalid-citation.json", Request: SearchRequest{Type: "web", Query: "Search with one malformed citation.", Model: "grok-4.5"}},
	{Name: "no-tool", File: "no-tool.json", Request: SearchRequest{Type: "web", Query: "Return an answer without searching.", Model: "grok-4.5"}},
	{Name: "empty-answer", File: "empty-answer.json", Request: SearchRequest{Type: "x", Query: "Search but return no answer.", Model: "grok-4.5"}},
	{Name: "malformed-event", File: "malformed-event.json", Request: SearchRequest{Type: "web", Query: "Exercise an incompatible event.", Model: "grok-4.5"}},
}

func main() {
	if len(os.Args) > 1 {
		renderNamed(os.Args[1], false)
		return
	}

	reader := bufio.NewReader(os.Stdin)
	selected := 0
	for {
		fmt.Print("\033[2J\033[H")
		render(scenarios[selected], true)
		fmt.Print("\n[1] web  [2] x  [3] missing citations  [4] invalid citation\n")
		fmt.Print("[5] no tool  [6] empty answer  [7] malformed event  [q] quit\n> ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		choice := strings.TrimSpace(line)
		if choice == "q" {
			return
		}
		if len(choice) == 1 && choice[0] >= '1' && choice[0] <= '7' {
			selected = int(choice[0] - '1')
		}
	}
}

func renderNamed(name string, interactive bool) {
	for _, candidate := range scenarios {
		if candidate.Name == name {
			render(candidate, interactive)
			return
		}
	}
	fmt.Fprintf(os.Stderr, "unknown scenario %q\n", name)
	os.Exit(2)
}

func render(selected scenario, interactive bool) {
	raw, err := fixtures.ReadFile("fixtures/" + selected.File)
	if err != nil {
		panic(err)
	}

	var events []json.RawMessage
	if err := json.Unmarshal(raw, &events); err != nil {
		panic(err)
	}

	response, normalizedError := NormalizeSearch(selected.Request, events)
	var output any = response
	if normalizedError != nil {
		output = normalizedError
	}
	encoded, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		panic(err)
	}

	if interactive {
		fmt.Printf("Response normalization prototype — %s\n\n", selected.Name)
		fmt.Printf("Request: type=%s model=%s\n\n", selected.Request.Type, selected.Request.Model)
	}
	fmt.Println(string(encoded))
}
