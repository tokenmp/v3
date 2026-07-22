// Package sdk streaming port.
package sdk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

// MaxStreamEventDataBytes is the hard cap for one owned canonical provider
// payload. Keeping this shared boundary at 256 KiB bounds the future driver's
// pre-commit payload retention to 35 × 256 KiB rather than tens of MiB.
const MaxStreamEventDataBytes = 256 << 10

// StreamEvent joins the protocol-neutral lifecycle metadata with an owned,
// canonical protocol payload for a future renderer. Data is never shared with
// an SDK decoder or caller-owned input buffer, and is at most
// MaxStreamEventDataBytes bytes. EventNativeError has nil Data.
type StreamEvent struct {
	Sequence uint64
	Meta     streaming.Event
	Data     json.RawMessage
	// Classified is present only for EventNativeError. It is safe, owned
	// terminal metadata for retry/accounting decisions; it never carries raw
	// provider error text or payload.
	Classified *ClassifiedError
}

// CloneStreamEvent returns an independent owned copy suitable for crossing an
// execution boundary. It copies payload bytes and the safe classified error;
// it never retains caller-owned mutable data.
func CloneStreamEvent(event StreamEvent) StreamEvent {
	if event.Meta.Progress != nil {
		value := *event.Meta.Progress
		event.Meta.Progress = &value
	}
	if event.Meta.Usage != nil {
		value := *event.Meta.Usage
		event.Meta.Usage = &value
	}
	event.Data = append(json.RawMessage(nil), event.Data...)
	event.Classified = CloneClassifiedError(event.Classified)
	return event
}

// String, GoString, and Format expose only the fixed type, sequence, and
// protocol-neutral kind. In particular, provider event types, finish reasons,
// and payload data are never diagnostic output.
func (e StreamEvent) String() string {
	return fmt.Sprintf("sdk.StreamEvent{Sequence:%d Meta.Kind:%s}", e.Sequence, e.Meta.Kind)
}
func (e StreamEvent) GoString() string { return e.String() }
func (e StreamEvent) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(e.String()))
}

// StreamSource is a provider semantic stream. It deliberately differs from
// streaming.Source: protocol adapters own payload parsing/classification while
// the streaming core remains metadata-only. Next is serial and must honor its
// context. Close is idempotent, safe to call concurrently with an in-flight
// Next, and non-blocking (or bounded); it must cancel or otherwise unblock an
// in-flight Next where the provider permits it. After Close, the source must
// not be reused.
type StreamSource interface {
	Next(context.Context) (StreamEvent, error)
	Close() error
}

// StreamCall is one already-adapted streaming provider call. It is an
// execution capability, not diagnostic data: all ordinary formatting is a
// fixed marker so target URLs, requests, and credentials cannot leak.
type StreamCall struct {
	Candidate CandidateIdentity
	Target    Target
	Request   adapter.AppliedRequest
	Secret    CredentialSecret
}

func (StreamCall) String() string   { return "sdk.StreamCall([REDACTED])" }
func (StreamCall) GoString() string { return "sdk.StreamCall([REDACTED])" }
func (StreamCall) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte("sdk.StreamCall([REDACTED])"))
}

// StreamOpen contains only opening-response metadata and the caller-owned
// source. It intentionally does not expose an HTTP response, headers, body,
// URL, request payload, or credential.
type StreamOpen struct {
	Source    StreamSource
	Status    int
	RequestID string
}

// String, GoString, and Format expose only the HTTP status. In particular,
// they never format Source (which can be an arbitrary provider-owned value) or
// RequestID (which is not necessarily safe when a StreamOpen is constructed by
// a caller rather than an adapter).
func (o StreamOpen) String() string {
	return fmt.Sprintf("sdk.StreamOpen{Status:%d}", o.Status)
}
func (o StreamOpen) GoString() string { return o.String() }
func (o StreamOpen) Format(state fmt.State, verb rune) {
	_, _ = state.Write([]byte(o.String()))
}

// StreamClient opens exactly one provider stream. Pre-opening failures return
// safe sentinels or ClassifiedError values; a successful source is owned by
// the caller and must be closed.
type StreamClient interface {
	Stream(context.Context, StreamCall) (StreamOpen, error)
}
