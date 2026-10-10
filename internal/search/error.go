package search

import "strings"

type ErrorCode string

const (
	ErrorUsage                   ErrorCode = "usage_error"
	ErrorAuthMissing             ErrorCode = "auth_missing"
	ErrorAuthUnreadable          ErrorCode = "auth_unreadable"
	ErrorAuthInsecure            ErrorCode = "auth_insecure"
	ErrorAuthInvalid             ErrorCode = "auth_invalid"
	ErrorAuthExpired             ErrorCode = "auth_expired"
	ErrorAuthRejected            ErrorCode = "auth_rejected"
	ErrorPermissionDenied        ErrorCode = "permission_denied"
	ErrorRateLimited             ErrorCode = "rate_limited"
	ErrorRequestRejected         ErrorCode = "request_rejected"
	ErrorRequestTooLarge         ErrorCode = "request_too_large"
	ErrorUnsupportedModel        ErrorCode = "unsupported_model"
	ErrorSearchModelUnavailable  ErrorCode = "search_model_unavailable"
	ErrorClientIncompatible      ErrorCode = "client_incompatible"
	ErrorRequestTimeout          ErrorCode = "request_timeout"
	ErrorTransport               ErrorCode = "transport_error"
	ErrorProviderUnavailable     ErrorCode = "provider_unavailable"
	ErrorProvider                ErrorCode = "provider_error"
	ErrorOutputTooLarge          ErrorCode = "output_too_large"
	ErrorOutputTruncated         ErrorCode = "output_truncated"
	ErrorInvalidProviderResponse ErrorCode = "invalid_provider_response"
	ErrorSearchNotPerformed      ErrorCode = "search_not_performed"
	ErrorInternal                ErrorCode = "internal_error"
)

type errorDefinition struct {
	message   string
	retryable bool
	exitCode  int
}

type Error struct {
	Code              ErrorCode `json:"code"`
	Message           string    `json:"message"`
	Retryable         bool      `json:"retryable"`
	HTTPStatus        int       `json:"http_status,omitempty"`
	RetryAfterSeconds *int      `json:"retry_after_seconds,omitempty"`
	RequestID         string    `json:"request_id,omitempty"`
}

// NewError creates a closed, client-owned error. Unknown codes are sanitized
// to internal_error so they cannot expand the public error contract.
func NewError(code ErrorCode) *Error {
	definition, ok := errorDefinitions[code]
	if !ok {
		code = ErrorInternal
		definition = errorDefinitions[code]
	}

	return &Error{
		Code:      code,
		Message:   definition.message,
		Retryable: definition.retryable,
	}
}

// WithHTTPStatus attaches a status observed from the trusted provider origin.
func (errorValue *Error) WithHTTPStatus(status int) *Error {
	if errorValue != nil {
		errorValue.HTTPStatus = status
	}
	return errorValue
}

// WithRetryAfterSeconds attaches an already-validated Retry-After value.
// A value of zero remains present in JSON.
func (errorValue *Error) WithRetryAfterSeconds(seconds int) *Error {
	if errorValue != nil {
		errorValue.RetryAfterSeconds = &seconds
	}
	return errorValue
}

// WithRequestID attaches an already-sanitized trusted-provider request ID.
func (errorValue *Error) WithRequestID(requestID string) *Error {
	if errorValue != nil {
		errorValue.RequestID = requestID
	}
	return errorValue
}

// Sanitized rebuilds an error from the closed public contract. It retains only
// metadata that has already passed the narrow trusted-value checks below.
func (errorValue *Error) Sanitized() *Error {
	if errorValue == nil {
		return NewError(ErrorInternal)
	}

	sanitized := NewError(errorValue.Code)
	if errorValue.HTTPStatus >= 100 && errorValue.HTTPStatus <= 599 {
		sanitized.WithHTTPStatus(errorValue.HTTPStatus)
	}
	if errorValue.RetryAfterSeconds != nil && *errorValue.RetryAfterSeconds >= 0 && *errorValue.RetryAfterSeconds <= 3600 {
		sanitized.WithRetryAfterSeconds(*errorValue.RetryAfterSeconds)
	}
	if isSafeRequestID(errorValue.RequestID) {
		sanitized.WithRequestID(errorValue.RequestID)
	}
	return sanitized
}

// ExitCode maps stable error codes to the command contract's process status.
func (errorValue *Error) ExitCode() int {
	if errorValue == nil {
		return 0
	}

	definition, ok := errorDefinitions[errorValue.Code]
	if !ok {
		return errorDefinitions[ErrorInternal].exitCode
	}
	return definition.exitCode
}

func isSafeRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > 128 {
		return false
	}
	for _, character := range requestID {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:-", character) {
			return false
		}
	}
	return true
}

var errorDefinitions = map[ErrorCode]errorDefinition{
	ErrorUsage:                   {message: "The command arguments are invalid.", exitCode: 2},
	ErrorAuthMissing:             {message: "No valid Grok credential was found. Run `grok login`.", exitCode: 3},
	ErrorAuthUnreadable:          {message: "The Grok credential store could not be read.", exitCode: 3},
	ErrorAuthInsecure:            {message: "The Grok credential store failed security checks.", exitCode: 3},
	ErrorAuthInvalid:             {message: "The Grok credential store is invalid. Run `grok login`.", exitCode: 3},
	ErrorAuthExpired:             {message: "The Grok credential has expired. Run `grok login`.", exitCode: 3},
	ErrorAuthRejected:            {message: "The Grok credential was rejected. Run `grok login`.", exitCode: 3},
	ErrorPermissionDenied:        {message: "The provider denied permission for the search request.", exitCode: 4},
	ErrorRateLimited:             {message: "The provider rate-limited the search request.", retryable: true, exitCode: 4},
	ErrorRequestRejected:         {message: "The provider rejected the search request.", exitCode: 4},
	ErrorRequestTooLarge:         {message: "The search request is too large for the provider.", exitCode: 4},
	ErrorUnsupportedModel:        {message: "The requested model is not supported by the provider.", exitCode: 4},
	ErrorSearchModelUnavailable:  {message: "The provider did not list a Responses search model. Specify --model.", exitCode: 4},
	ErrorClientIncompatible:      {message: "The provider requires a newer version of this CLI. Upgrade grok-search.", exitCode: 4},
	ErrorRequestTimeout:          {message: "The search request timed out.", retryable: true, exitCode: 4},
	ErrorTransport:               {message: "The search request could not reach the provider.", retryable: true, exitCode: 4},
	ErrorProviderUnavailable:     {message: "The provider is temporarily unavailable.", retryable: true, exitCode: 4},
	ErrorProvider:                {message: "The provider could not complete the search request.", exitCode: 4},
	ErrorOutputTooLarge:          {message: "The provider response exceeded the allowed size.", exitCode: 4},
	ErrorOutputTruncated:         {message: "The provider truncated the search response.", exitCode: 4},
	ErrorInvalidProviderResponse: {message: "The provider returned an invalid search response.", exitCode: 4},
	ErrorSearchNotPerformed:      {message: "The provider did not perform the requested search.", exitCode: 4},
	ErrorInternal:                {message: "An unexpected internal error occurred.", exitCode: 70},
}
