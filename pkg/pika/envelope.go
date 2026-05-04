package pika

// PIKA-V3: envelope.go — unified tool response envelope (D-8, F7-9)

import (
	"encoding/json"
	"fmt"
	"strings"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// ErrorKind classifies envelope errors for retry/propagation (F7-9, PIKA-V3).
type ErrorKind int

const (
	// Transient — retryable error (timeout, exec failure). Retry with backoff.
	Transient ErrorKind = iota
	// Permanent — non-retryable error (bad params, permission denied). Stop.
	Permanent
	// Degraded — partial failure, service available at reduced quality.
	// Reserved for future use; not produced by ClassifyEnvelopeError.
	Degraded
)

// String returns the string representation of ErrorKind.
func (k ErrorKind) String() string {
	switch k {
	case Transient:
		return "transient"
	case Permanent:
		return "permanent"
	case Degraded:
		return "degraded"
	default:
		return "unknown"
	}
}

// PIKA-V3: Error code constants (D-8, Конфигурация §5).
const (
	ErrUnknownOp        = "unknown_op"        // операция не в enum tool'a
	ErrInvalidParams    = "invalid_params"    // отсутствуют/невалидные параметры
	ErrTimeout          = "timeout"           // превышен per-tool таймаут
	ErrExecError        = "exec_error"        // команда exit code ≠ 0
	ErrPermissionDenied = "permission_denied" // запрещено в текущем режиме
	ErrParseError       = "parse_error"       // невалидный JSON (наш, не от shell)
)

// Envelope represents a unified shell-tool response (D-8).
// Format: {"ok": bool, "data": object|null, "error": string|null}
type Envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *string         `json:"error"`
}

// ParseEnvelope parses raw JSON into an Envelope.
// Never panics, never returns error — always returns a valid Envelope.
// Invalid/empty input → Envelope with OK=false and parse_error code.
func ParseEnvelope(raw []byte) Envelope {
	if len(raw) == 0 {
		errStr := "parse_error: empty input"
		return Envelope{OK: false, Error: &errStr}
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		errStr := fmt.Sprintf("parse_error: %s", err.Error())
		return Envelope{OK: false, Error: &errStr}
	}

	return env
}

// ErrorCode extracts the error code prefix from the error string.
// Error strings follow "code: description" format.
// Returns "" if no error is present.
func (e *Envelope) ErrorCode() string {
	if e.Error == nil || *e.Error == "" {
		return ""
	}
	errStr := *e.Error
	if idx := strings.Index(errStr, ": "); idx != -1 {
		return errStr[:idx]
	}
	return errStr
}

// ClassifyEnvelopeError maps an error code to an ErrorKind (F7-9).
// Transient: timeout, exec_error (retry with backoff).
// Permanent: everything else (stop, do not retry).
func ClassifyEnvelopeError(code string) ErrorKind {
	switch code {
	case ErrTimeout, ErrExecError:
		return Transient
	case ErrUnknownOp, ErrInvalidParams, ErrPermissionDenied, ErrParseError:
		return Permanent
	default:
		return Permanent
	}
}

// IsRetryable returns true if the envelope error is transient (D-8).
// Only timeout and exec_error are retryable.
func (e *Envelope) IsRetryable() bool {
	return ClassifyEnvelopeError(e.ErrorCode()) == Transient
}

// ToToolResult converts the Envelope to an upstream ToolResult.
// ok=true  → ToolResult{ForLLM: data, IsError: false}
// ok=false → ToolResult{ForLLM: error, IsError: true, Err: ...}
func (e *Envelope) ToToolResult() *toolshared.ToolResult {
	if e.OK {
		return &toolshared.ToolResult{
			ForLLM:  formatData(e.Data),
			IsError: false,
		}
	}

	errMsg := ""
	if e.Error != nil {
		errMsg = *e.Error
	}
	return &toolshared.ToolResult{
		ForLLM:  errMsg,
		IsError: true,
		Err:     fmt.Errorf("pika/envelope: %s", errMsg),
	}
}

// formatData converts raw JSON data to a string for LLM consumption.
func formatData(data json.RawMessage) string {
	if len(data) == 0 || string(data) == "null" {
		return ""
	}
	return string(data)
}
