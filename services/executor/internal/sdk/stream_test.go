package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

func TestStreamCallFormattingIsFixedRedacted(t *testing.T) {
	t.Parallel()
	const secret = "stream-secret-value"
	call := StreamCall{
		Candidate: CandidateIdentity{ModelID: "m", ProviderID: "p", RouteID: "r", CredentialID: "c", AdapterID: "a"},
		Target:    Target{BaseURL: "https://provider.example/v1", UpstreamModel: "upstream-model", Protocol: adapter.ProtocolOpenAIChat},
		Request:   adapter.AppliedRequest{Body: []byte(`{"model":"caller"}`)},
		Secret:    NewCredentialSecret([]byte(secret)),
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
		if got := fmt.Sprintf(format, call); got != "sdk.StreamCall([REDACTED])" {
			t.Fatalf("format %q = %q", format, got)
		}
	}
}

func TestStreamCallCopiesCredentialInput(t *testing.T) {
	t.Parallel()
	input := []byte("original")
	call := StreamCall{Secret: NewCredentialSecret(input)}
	input[0] = 'X'
	if err := call.Secret.Use(func(value []byte) error {
		if string(value) != "original" {
			t.Fatalf("credential copy = %q, want original", value)
		}
		return nil
	}); err != nil {
		t.Fatalf("Secret.Use: %v", err)
	}
}

func TestStreamOpenCarriesOnlySafeValues(t *testing.T) {
	t.Parallel()
	open := StreamOpen{Source: nilSource{}, Status: 200, RequestID: "req_123"}
	if open.Source == nil || open.Status != 200 || open.RequestID != "req_123" {
		t.Fatal("StreamOpen did not retain its fields")
	}
}

func TestStreamOpenFormattingNeverFormatsSourceOrRequestID(t *testing.T) {
	t.Parallel()
	const secret = "stream-open-source-secret"
	open := StreamOpen{
		Source:    formattingSource{marker: secret},
		Status:    201,
		RequestID: secret,
	}
	const want = "sdk.StreamOpen{Status:201}"
	// These are every value-formatting verb routed to fmt.Formatter. %T and %p
	// are formatter-bypassing type/address inspection verbs, and %w is only
	// meaningful to Errorf; none is an ordinary StreamOpen representation.
	for _, verb := range []rune("vtbcdoOqxXUeEfFgGs") {
		format := "%+08.3" + string(verb)
		got := fmt.Sprintf(format, open)
		if got != want {
			t.Errorf("format %q = %q, want %q", format, got, want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("format %q leaked secret: %q", format, got)
		}
	}
}

func TestStreamEventFormattingOnlyExposesSequenceAndKind(t *testing.T) {
	t.Parallel()
	const secret = "provider-payload-secret"
	ev := StreamEvent{
		Sequence: 7,
		Meta:     streaming.Event{Kind: streaming.EventFinish, EventType: "provider-" + secret, FinishReason: secret},
		Data:     json.RawMessage(`{"secret":"` + secret + `"}`),
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X"} {
		if got := fmt.Sprintf(format, ev); got != "sdk.StreamEvent{Sequence:7 Meta.Kind:finish}" {
			t.Fatalf("format %q = %q", format, got)
		}
	}
}

func TestStreamClientHasOnlyStream(t *testing.T) {
	t.Parallel()
	var _ StreamClient = onlyStreamClient{}
}

type nilSource struct{}

func (nilSource) Next(context.Context) (StreamEvent, error) {
	return StreamEvent{}, nil
}
func (nilSource) Close() error { return nil }

type formattingSource struct{ marker string }

func (s formattingSource) Next(context.Context) (StreamEvent, error) {
	return StreamEvent{}, nil
}
func (s formattingSource) Close() error { return nil }
func (s formattingSource) String() string {
	return "formattingSource(" + s.marker + ")"
}
func (s formattingSource) GoString() string {
	return "formattingSource(" + s.marker + ")"
}
func (s formattingSource) Format(state fmt.State, verb rune) {
	_, _ = fmt.Fprintf(state, "formattingSource(%s)", s.marker)
}

type onlyStreamClient struct{}

func (onlyStreamClient) Stream(context.Context, StreamCall) (StreamOpen, error) {
	return StreamOpen{Source: nilSource{}, Status: 200}, nil
}
