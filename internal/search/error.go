package search

type ErrorCode string

const (
	ErrorUsage          ErrorCode = "usage_error"
	ErrorAuthMissing    ErrorCode = "auth_missing"
	ErrorAuthUnreadable ErrorCode = "auth_unreadable"
	ErrorAuthInsecure   ErrorCode = "auth_insecure"
	ErrorAuthInvalid    ErrorCode = "auth_invalid"
	ErrorAuthExpired    ErrorCode = "auth_expired"
	ErrorInternal       ErrorCode = "internal_error"
)

type Error struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable"`
}

func NewError(code ErrorCode) *Error {
	return &Error{
		Code:      code,
		Message:   errorMessage(code),
		Retryable: false,
	}
}

func errorMessage(code ErrorCode) string {
	switch code {
	case ErrorUsage:
		return "The command arguments are invalid."
	case ErrorAuthMissing:
		return "No valid Grok credential was found. Run `grok login`."
	case ErrorAuthUnreadable:
		return "The Grok credential store could not be read."
	case ErrorAuthInsecure:
		return "The Grok credential store failed security checks."
	case ErrorAuthInvalid:
		return "The Grok credential store is invalid. Run `grok login`."
	case ErrorAuthExpired:
		return "The Grok credential has expired. Run `grok login`."
	default:
		return "An unexpected internal error occurred."
	}
}
