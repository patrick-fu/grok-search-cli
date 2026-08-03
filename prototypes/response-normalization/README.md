# Response-normalization prototype

> **PROTOTYPE — not production code.**

## Question

What exact version-1 `SearchResponse`, `Source`, warning, and error rules preserve
useful Web/X answers and citations without exposing provider events, promising
ranked hits, or fabricating unavailable metadata?

The pure `NormalizeSearch(request, events)` function is the prototype seam. The
interactive shell lets a user drive sanitized Web, X, missing-citation,
invalid-citation, no-tool, empty-answer, and malformed-event fixtures through
the same normalizer.

## Run

Interactive:

```bash
go run ./prototypes/response-normalization/*.go
```

One scenario:

```bash
go run ./prototypes/response-normalization/*.go web
go run ./prototypes/response-normalization/*.go x
go run ./prototypes/response-normalization/*.go missing-citations
```

## Fixture provenance

The Web and X shapes are sanitized reductions of live OAuth requests observed
on 2026-08-04. Raw SSE stayed in the system temporary directory and is not
committed.

- Web returned three completed `web_search_call` items containing 29 candidate
  URLs, but only two `url_citation` annotations. Tool candidates are therefore
  search evidence, not public `sources` or ranked hits.
- X returned a completed `custom_tool_call` named `x_keyword_search` and five
  `url_citation` annotations. Their titles repeated the URL and their spans were
  `0..0`, so V1 does not invent titles, authors, dates, snippets, or useful
  citation offsets.
- Both completed responses reported provider model `grok-4.5-build` for
  requested model `grok-4.5`.
- Missing/invalid citation and failure fixtures are synthetic edge reductions
  used to exercise the contract, not claims that those exact live events were
  observed.

## Proposed V1 interface

```json
{
  "schema_version": 1,
  "type": "web",
  "query": "Find the Web and X Search documentation.",
  "model": "grok-4.5",
  "provider_model": "grok-4.5-build",
  "answer": "The Web Search documentation is available at the cited URL.",
  "sources": [
    {
      "url": "https://docs.example.test/web"
    }
  ],
  "warnings": []
}
```

- `type`, `query`, and `model` are request-owned and always present.
- `provider_model` is present only when reliably observed and different.
- `sources` and `warnings` are always arrays, including when empty.
- `Source.url` is a valid absolute HTTP(S) citation URL. `title` is optional and
  omitted when empty, numeric citation-label text, or identical to the URL.
- Sources are exact-URL deduplicated in first citation appearance order. Their
  order is not a relevance rank.
- Warning codes are closed in V1: `missing_citations` and
  `invalid_citations_ignored`.
- Tool result candidates, tool operation names, citation spans, scores,
  snippets, authors, dates, raw SSE, reasoning, and provider messages are not
  part of the interface.

## Success and failure rules

Success requires exactly one terminal `response.completed`, a completed tool
event matching the requested search type, and non-empty answer text. The final
completed answer is authoritative, with output-text-done and deltas as
fallbacks.

- No matching completed tool event: `search_not_performed`.
- Missing, duplicate, non-terminal, or undecodable completion:
  `invalid_provider_response`.
- Completed tool with no answer: `invalid_provider_response`.
- Provider failure/error event: broad `provider_error`.
- No accepted citations: success with `missing_citations` and `sources: []`.
- Invalid URL citations: ignore them and add `invalid_citations_ignored`.

Technical success is account- and date-specific. It is not evidence of xAI
authorization or a stable third-party contract. The repository owner has chosen
to continue private use at their own risk; public distribution remains blocked.
