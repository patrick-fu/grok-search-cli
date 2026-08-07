// Package search defines the provider-independent language shared by the CLI,
// transport, and response normalizer.
package search

const SchemaVersion = 1

// Type is the requested search surface. It is request-owned and never inferred
// from a provider event.
type Type string

const (
	TypeWeb Type = "web"
	TypeX   Type = "x"
)

// Request describes one requested search.
type Request struct {
	Type  Type
	Query string
	Model string
	// MaxOutputTokens bounds the provider's output size. Zero means the
	// transport default applies.
	MaxOutputTokens int
}

// Response is a successful normalized answer to one Request.
type Response struct {
	SchemaVersion int       `json:"schema_version"`
	Type          Type      `json:"type"`
	Query         string    `json:"query"`
	Model         string    `json:"model"`
	ProviderModel string    `json:"provider_model,omitempty"`
	Answer        string    `json:"answer"`
	Sources       []Source  `json:"sources"`
	Warnings      []Warning `json:"warnings"`
}

// Source is an unranked provider-supplied URL citation attached to the final
// answer.
type Source struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// Warning describes degraded evidence in an otherwise usable Response.
type Warning string

const (
	WarningInvalidCitationsIgnored Warning = "invalid_citations_ignored"
	WarningMissingCitations        Warning = "missing_citations"
)
