# Grok Search

Grok Search provides agents with one normalized Web or X search answer while
keeping provider-specific transport details outside the product language.

## Language

**Search Type**:
The requested search surface: `web` or `x`. It belongs to the request and is not
inferred from provider output.
_Avoid_: Mode, tool type

**Search Response**:
A successful normalized answer to one search request, including its Sources and
any Search Warnings. It is not the provider's raw response.
_Avoid_: Provider response, raw response

**Source**:
A distinct provider-supplied URL citation attached to the final answer. It is
not a candidate search hit and carries no ranking promise.
_Avoid_: Search hit, result, ranked result

**Search Warning**:
A stable condition describing degraded evidence in an otherwise usable Search
Response.
_Avoid_: Error, provider warning

**Requested Model**:
The model identity selected for a search. It remains the request's model when
the provider resolves it to a different alias.
_Avoid_: Actual model

**Provider Model**:
The resolved model identity reported by the provider when it differs from the
Requested Model. The difference does not imply client-side fallback.
_Avoid_: Fallback model
