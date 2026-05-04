package pika

import (
	"testing"
)

func TestParseEnvelope_ValidOKTrue(t *testing.T) {
	raw := []byte(
		`{"ok": true, "data": {"status": "running", "uptime": 42}, "error": null}`,
	)
	env := ParseEnvelope(raw)

	if !env.OK {
		t.Fatal("expected OK=true")
	}
	if env.Error != nil {
		t.Fatalf("expected Error=nil, got %q", *env.Error)
	}
	if string(env.Data) == "" || string(env.Data) == "null" {
		t.Fatal("expected non-empty data")
	}
	if env.ErrorCode() != "" {
		t.Errorf("ErrorCode() = %q, want empty", env.ErrorCode())
	}
	if env.IsRetryable() {
		t.Error("ok=true envelope should not be retryable")
	}
}

func TestParseEnvelope_ValidOKFalse_UnknownOp(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "unknown_op: no such operation"}`,
	)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false")
	}
	if env.ErrorCode() != ErrUnknownOp {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrUnknownOp)
	}
	if env.IsRetryable() {
		t.Error("unknown_op should not be retryable")
	}
}

func TestParseEnvelope_ValidOKFalse_InvalidParams(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "invalid_params: missing field"}`,
	)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false")
	}
	if env.ErrorCode() != ErrInvalidParams {
		t.Errorf(
			"ErrorCode() = %q, want %q",
			env.ErrorCode(), ErrInvalidParams,
		)
	}
	if env.IsRetryable() {
		t.Error("invalid_params should not be retryable")
	}
}

func TestParseEnvelope_ValidOKFalse_Timeout(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "timeout: exceeded 30s limit"}`,
	)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false")
	}
	if env.ErrorCode() != ErrTimeout {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrTimeout)
	}
	if !env.IsRetryable() {
		t.Error("timeout should be retryable")
	}
}

func TestParseEnvelope_ValidOKFalse_ExecError(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "exec_error: compose restart failed"}`,
	)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false")
	}
	if env.ErrorCode() != ErrExecError {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrExecError)
	}
	if !env.IsRetryable() {
		t.Error("exec_error should be retryable")
	}
}

func TestParseEnvelope_ValidOKFalse_PermissionDenied(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "permission_denied: rm not allowed"}`,
	)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false")
	}
	if env.ErrorCode() != ErrPermissionDenied {
		t.Errorf(
			"ErrorCode() = %q, want %q",
			env.ErrorCode(), ErrPermissionDenied,
		)
	}
	if env.IsRetryable() {
		t.Error("permission_denied should not be retryable")
	}
}

func TestParseEnvelope_InvalidJSON(t *testing.T) {
	raw := []byte(`{not valid json}`)
	env := ParseEnvelope(raw)

	if env.OK {
		t.Fatal("expected OK=false for invalid JSON")
	}
	if env.ErrorCode() != ErrParseError {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrParseError)
	}
	if env.IsRetryable() {
		t.Error("parse_error should not be retryable")
	}
}

func TestParseEnvelope_EmptyInput(t *testing.T) {
	env := ParseEnvelope([]byte{})

	if env.OK {
		t.Fatal("expected OK=false for empty input")
	}
	if env.ErrorCode() != ErrParseError {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrParseError)
	}
}

func TestParseEnvelope_NilInput(t *testing.T) {
	env := ParseEnvelope(nil)

	if env.OK {
		t.Fatal("expected OK=false for nil input")
	}
	if env.ErrorCode() != ErrParseError {
		t.Errorf("ErrorCode() = %q, want %q", env.ErrorCode(), ErrParseError)
	}
}

func TestClassifyEnvelopeError_AllCodes(t *testing.T) {
	tests := []struct {
		code string
		want ErrorKind
	}{
		{ErrTimeout, Transient},
		{ErrExecError, Transient},
		{ErrUnknownOp, Permanent},
		{ErrInvalidParams, Permanent},
		{ErrPermissionDenied, Permanent},
		{ErrParseError, Permanent},
	}
	for _, tt := range tests {
		got := ClassifyEnvelopeError(tt.code)
		if got != tt.want {
			t.Errorf(
				"ClassifyEnvelopeError(%q) = %v, want %v",
				tt.code, got, tt.want,
			)
		}
	}
}

func TestClassifyEnvelopeError_UnknownCode(t *testing.T) {
	got := ClassifyEnvelopeError("some_unknown_code")
	if got != Permanent {
		t.Errorf(
			"ClassifyEnvelopeError(unknown) = %v, want Permanent",
			got,
		)
	}
}

func TestIsRetryable_AllCodes(t *testing.T) {
	tests := []struct {
		errStr string
		want   bool
	}{
		{"timeout: exceeded limit", true},
		{"exec_error: exit code 1", true},
		{"unknown_op: bad op", false},
		{"invalid_params: missing field", false},
		{"permission_denied: not allowed", false},
		{"parse_error: bad json", false},
	}
	for _, tt := range tests {
		errCopy := tt.errStr
		env := &Envelope{OK: false, Error: &errCopy}
		got := env.IsRetryable()
		if got != tt.want {
			t.Errorf(
				"IsRetryable() for %q = %v, want %v",
				tt.errStr, got, tt.want,
			)
		}
	}
}

func TestToToolResult_OKTrue(t *testing.T) {
	raw := []byte(`{"ok": true, "data": {"count": 5}, "error": null}`)
	env := ParseEnvelope(raw)
	tr := env.ToToolResult()

	if tr.IsError {
		t.Error("expected IsError=false for ok=true")
	}
	if tr.ForLLM == "" {
		t.Error("expected non-empty ForLLM")
	}
	if tr.Err != nil {
		t.Errorf("expected Err=nil, got %v", tr.Err)
	}
}

func TestToToolResult_OKFalse(t *testing.T) {
	raw := []byte(
		`{"ok": false, "data": null, "error": "exec_error: failed"}`,
	)
	env := ParseEnvelope(raw)
	tr := env.ToToolResult()

	if !tr.IsError {
		t.Error("expected IsError=true for ok=false")
	}
	if tr.ForLLM == "" {
		t.Error("expected non-empty ForLLM with error message")
	}
	if tr.Err == nil {
		t.Error("expected Err to be set")
	}
}

func TestToToolResult_OKTrue_NullData(t *testing.T) {
	raw := []byte(`{"ok": true, "data": null, "error": null}`)
	env := ParseEnvelope(raw)
	tr := env.ToToolResult()

	if tr.IsError {
		t.Error("expected IsError=false")
	}
	if tr.ForLLM != "" {
		t.Errorf("expected empty ForLLM for null data, got %q", tr.ForLLM)
	}
}

func TestErrorKind_String(t *testing.T) {
	if Transient.String() != "transient" {
		t.Errorf("Transient.String() = %q", Transient.String())
	}
	if Permanent.String() != "permanent" {
		t.Errorf("Permanent.String() = %q", Permanent.String())
	}
	if Degraded.String() != "degraded" {
		t.Errorf("Degraded.String() = %q", Degraded.String())
	}
}

func TestIsRetryable_OKTrue_NotRetryable(t *testing.T) {
	env := &Envelope{OK: true}
	if env.IsRetryable() {
		t.Error("ok=true envelope should never be retryable")
	}
}
