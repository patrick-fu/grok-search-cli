package search

import (
	"encoding/json"
	"testing"
)

func TestNewErrorContract(t *testing.T) {
	tests := []struct {
		code      ErrorCode
		message   string
		retryable bool
		exitCode  int
	}{
		{ErrorUsage, "The command arguments are invalid.", false, 2},
		{ErrorAuthMissing, "No valid Grok credential was found. Run `grok login`.", false, 3},
		{ErrorAuthUnreadable, "The Grok credential store could not be read.", false, 3},
		{ErrorAuthInsecure, "The Grok credential store failed security checks.", false, 3},
		{ErrorAuthInvalid, "The Grok credential store is invalid. Run `grok login`.", false, 3},
		{ErrorAuthExpired, "The Grok credential has expired. Run `grok login`.", false, 3},
		{ErrorAuthRejected, "The Grok credential was rejected. Run `grok login`.", false, 3},
		{ErrorPermissionDenied, "The provider denied permission for the search request.", false, 4},
		{ErrorRateLimited, "The provider rate-limited the search request.", true, 4},
		{ErrorRequestRejected, "The provider rejected the search request.", false, 4},
		{ErrorRequestTooLarge, "The search request is too large for the provider.", false, 4},
		{ErrorUnsupportedModel, "The requested model is not supported by the provider.", false, 4},
		{ErrorSearchModelUnavailable, "The provider did not list a Responses search model. Specify --model.", false, 4},
		{ErrorClientIncompatible, "The provider requires a newer version of this CLI. Upgrade grok-search.", false, 4},
		{ErrorRequestTimeout, "The search request timed out.", true, 4},
		{ErrorTransport, "The search request could not reach the provider.", true, 4},
		{ErrorProviderUnavailable, "The provider is temporarily unavailable.", true, 4},
		{ErrorProvider, "The provider could not complete the search request.", false, 4},
		{ErrorOutputTooLarge, "The provider response exceeded the allowed size.", false, 4},
		{ErrorOutputTruncated, "The provider truncated the search response.", false, 4},
		{ErrorInvalidProviderResponse, "The provider returned an invalid search response.", false, 4},
		{ErrorSearchNotPerformed, "The provider did not perform the requested search.", false, 4},
		{ErrorInternal, "An unexpected internal error occurred.", false, 70},
	}

	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			errorValue := NewError(test.code)
			if errorValue.Code != test.code {
				t.Errorf("Code = %q, want %q", errorValue.Code, test.code)
			}
			if errorValue.Message != test.message {
				t.Errorf("Message = %q, want %q", errorValue.Message, test.message)
			}
			if errorValue.Retryable != test.retryable {
				t.Errorf("Retryable = %t, want %t", errorValue.Retryable, test.retryable)
			}
			if got := errorValue.ExitCode(); got != test.exitCode {
				t.Errorf("ExitCode() = %d, want %d", got, test.exitCode)
			}
		})
	}
}

func TestErrorJSONMetadata(t *testing.T) {
	t.Run("omits unobserved metadata", func(t *testing.T) {
		data, err := json.Marshal(NewError(ErrorRateLimited))
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"code":"rate_limited","message":"The provider rate-limited the search request.","retryable":true}`
		if got := string(data); got != want {
			t.Errorf("JSON = %s, want %s", got, want)
		}
	})

	t.Run("includes verified metadata including zero retry after", func(t *testing.T) {
		errorValue := NewError(ErrorRateLimited).
			WithHTTPStatus(429).
			WithRetryAfterSeconds(0).
			WithRequestID("safe-request-id")
		data, err := json.Marshal(errorValue)
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"code":"rate_limited","message":"The provider rate-limited the search request.","retryable":true,"http_status":429,"retry_after_seconds":0,"request_id":"safe-request-id"}`
		if got := string(data); got != want {
			t.Errorf("JSON = %s, want %s", got, want)
		}
	})
}

func TestUnknownErrorCodeIsSanitized(t *testing.T) {
	errorValue := NewError("provider body text")
	if errorValue.Code != ErrorInternal {
		t.Errorf("Code = %q, want %q", errorValue.Code, ErrorInternal)
	}
	if got := errorValue.ExitCode(); got != 70 {
		t.Errorf("ExitCode() = %d, want 70", got)
	}
}

func TestSanitizedErrorDropsMutableFields(t *testing.T) {
	invalidRetryAfter := 3601
	errorValue := (&Error{
		Code:              ErrorRateLimited,
		Message:           "RAW_PROVIDER_BODY_SENTINEL",
		Retryable:         false,
		HTTPStatus:        700,
		RetryAfterSeconds: &invalidRetryAfter,
		RequestID:         "unsafe request id",
	}).Sanitized()

	if errorValue.Code != ErrorRateLimited || errorValue.Message != errorDefinitions[ErrorRateLimited].message || !errorValue.Retryable {
		t.Fatalf("Sanitized() = %#v, want closed rate-limit contract", errorValue)
	}
	if errorValue.HTTPStatus != 0 || errorValue.RetryAfterSeconds != nil || errorValue.RequestID != "" {
		t.Fatalf("Sanitized() retained untrusted metadata: %#v", errorValue)
	}
}

func TestNilErrorExitCode(t *testing.T) {
	var errorValue *Error
	if got := errorValue.ExitCode(); got != 0 {
		t.Errorf("ExitCode() = %d, want 0", got)
	}
}
