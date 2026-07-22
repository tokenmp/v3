package anthropicadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk"
	"github.com/tokenmp/v3/services/executor/internal/streaming"
)

const streamQueueCapacity = 16

// streamOpening owns the official SDK stream and its opening-only safe
// metadata. The SDK owns decoding; the paired observer sees the same response
// body and is the sole authority for raw SSE framing and semantic parsing.
type streamOpening struct {
	Stream    *ssestream.Stream[anthropic.MessageStreamEventUnion]
	Status    int
	RequestID string

	cancel   context.CancelFunc
	observer *anthropicSSEObserver
	closeMu  sync.Mutex
	closed   bool
}

// cleanup synchronously cancels and closes the official stream. The official
// Stream.Close delegates to its HTTP response body Close; StreamSource relies
// on that bounded Close contract rather than orphaning a cleanup goroutine.
// A concurrent caller returns immediately once another caller owns cleanup.
func (o *streamOpening) cleanup() {
	if o == nil {
		return
	}
	o.closeMu.Lock()
	if o.closed {
		o.closeMu.Unlock()
		return
	}
	o.closed = true
	cancel, stream := o.cancel, o.Stream
	o.closeMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stream != nil {
		_ = stream.Close()
	}
}

// abort has the same synchronous close semantics as cleanup. It exists to
// make the source shutdown path explicit.
func (o *streamOpening) abort() { o.cleanup() }
func (o *streamOpening) String() string {
	if o == nil {
		return "anthropicadapter.streamOpening(nil)"
	}
	return fmt.Sprintf("anthropicadapter.streamOpening{Status:%d}", o.Status)
}
func (o *streamOpening) GoString() string                  { return o.String() }
func (o *streamOpening) Format(state fmt.State, verb rune) { _, _ = state.Write([]byte(o.String())) }

// openingCapture is per-call because the official SDK keeps its response
// local. It wraps the body before the SDK constructs its decoder, making the
// observer a tee rather than a competing reader.
type openingCapture struct {
	mu       sync.Mutex
	response *http.Response
	observer *anthropicSSEObserver
}

func (c *openingCapture) RoundTrip(req *http.Request, next http.RoundTripper) (*http.Response, error) {
	response, err := next.RoundTrip(req)
	if response != nil {
		if response.Body != nil && c.observer != nil {
			response.Body = observingAnthropicBody(response.Body, c.observer)
		}
		c.mu.Lock()
		c.response = response
		c.mu.Unlock()
	}
	return response, err
}
func (c *openingCapture) get() *http.Response { c.mu.Lock(); defer c.mu.Unlock(); return c.response }

type captureTransport struct {
	capture *openingCapture
	next    http.RoundTripper
}

func (t captureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.capture.RoundTrip(req, t.next)
}

// Stream opens one official Anthropic stream. Its source starts a read-driver
// goroutine immediately: typed SDK events are deliberately not cross-checked
// or used for sequencing; Next only drives the official decoder over the
// observer-wrapped response body.
func (c *Client) Stream(ctx context.Context, call sdk.StreamCall) (sdk.StreamOpen, error) {
	opening, err := c.openStream(ctx, call)
	if err != nil {
		return sdk.StreamOpen{}, err
	}
	return sdk.StreamOpen{Source: newMessageSource(opening, opening.observer), Status: opening.Status, RequestID: opening.RequestID}, nil
}

func (c *Client) openStream(ctx context.Context, call sdk.StreamCall) (*streamOpening, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if call.Target.Protocol != adapter.ProtocolAnthropic {
		return nil, ErrUnsupportedProtocol
	}
	if err := ctx.Err(); err != nil {
		return nil, classifyContextError(err)
	}
	baseURL, err := parseBaseURL(call.Target.BaseURL)
	if err != nil {
		return nil, ErrInvalidBaseURL
	}
	if strings.TrimSpace(call.Target.UpstreamModel) == "" {
		return nil, ErrMissingUpstreamModel
	}
	if err := validateInjection(call.Request.InjectionPlan); err != nil {
		return nil, ErrInvalidInjection
	}
	params, err := decodeMessageParamsMode(call.Request.Body, call.Request.Thinking, call.Target.UpstreamModel, true)
	if err != nil {
		if ctx.Err() != nil {
			return nil, classifyContextError(ctx.Err())
		}
		return nil, ErrInvalidRequest
	}

	var apiKey string
	if err := call.Secret.Use(func(key []byte) error {
		if strings.TrimSpace(string(key)) == "" {
			return ErrMissingAPIKey
		}
		apiKey = string(key)
		return nil
	}); err != nil {
		return nil, err
	}
	openingCtx, cancel := context.WithCancel(ctx)
	observer := newAnthropicSSEObserver(openingCtx)
	capture := &openingCapture{observer: observer}
	base := c.perCallHTTPClient(sdk.Call{Candidate: call.Candidate, Target: call.Target, Request: call.Request, Secret: call.Secret}, apiKey, "text/event-stream")
	perCall := &http.Client{Transport: captureTransport{capture: capture, next: base.Transport}, CheckRedirect: base.CheckRedirect, Jar: base.Jar, Timeout: base.Timeout}
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithBaseURL(baseURL.String()), option.WithHTTPClient(perCall), option.WithMaxRetries(0), option.WithAPIKey(apiKey)}
	opts = append(opts, injectionOptions(call.Request.InjectionPlan)...)
	opts = append(opts, option.WithHeader("x-api-key", apiKey))
	client := anthropic.NewClient(opts...)
	stream := client.Messages.NewStreaming(openingCtx, params)
	response := capture.get()
	if response == nil || response.StatusCode < 200 || response.StatusCode >= 300 || stream.Err() != nil {
		cancel()
		observer.abort()
		if stream != nil {
			_ = stream.Close()
		}
		if err := ctx.Err(); err != nil {
			return nil, classifyContextError(err)
		}
		if response != nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
			return nil, sdk.NewClassifiedError(kindForHTTPStatus(response.StatusCode), response.StatusCode, response.Header.Get("request-id"), "", "")
		}
		if stream != nil && stream.Err() != nil {
			return nil, classifyStreamOpenError(stream.Err(), response)
		}
		return nil, sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
	}
	return &streamOpening{Stream: stream, Status: response.StatusCode, RequestID: sdk.SafeRequestID(response.Header.Get("request-id")), cancel: cancel, observer: observer}, nil
}

func classifyStreamOpenError(err error, response *http.Response) *sdk.ClassifiedError {
	if errors.Is(err, context.DeadlineExceeded) {
		return sdk.NewClassifiedError(context.DeadlineExceeded, 0, "", "", "")
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return classifyError(err, response)
	}
	if response != nil {
		return sdk.NewClassifiedError(sdk.ErrProtocol, response.StatusCode, response.Header.Get("request-id"), "", "")
	}
	return sdk.NewClassifiedError(sdk.ErrTransport, 0, "", "", "")
}

// messageSource consumes observer events while its pump continuously invokes
// the official SDK decoder. The observer queue applies bounded backpressure
// directly to response Body.Read; no unbounded raw-frame retention exists.
type messageSource struct {
	opening  *streamOpening
	observer *anthropicSSEObserver
	pumpDone chan struct{}
	closed   chan struct{}
	closeMu  sync.Mutex
	closing  bool
	mu       sync.Mutex
	sequence uint64
	nextMu   sync.Mutex
}

func newMessageSource(opening *streamOpening, observer *anthropicSSEObserver) *messageSource {
	s := &messageSource{
		opening:  opening,
		observer: observer,
		pumpDone: make(chan struct{}),
		closed:   make(chan struct{}),
	}
	go s.pump()
	return s
}
func (s *messageSource) pump() {
	defer close(s.pumpDone)
	if s.opening == nil || s.opening.Stream == nil {
		s.observer.finish(sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", ""))
		return
	}
	for s.opening.Stream.Next() { /* official typed decoding is read-driver only */
	}
	// The semantic observer owns an in-band native error. Once it has queued
	// that terminal event, an SDK decoder error (which can be caused by the
	// same provider error frame) must not overwrite its classified result.
	if s.opening.Stream.Err() != nil && !s.isClosed() && !s.observer.hasTerminal() {
		s.observer.finish(sdk.NewClassifiedError(sdk.ErrProtocol, 0, "", "", ""))
		return
	}
	s.observer.finish(nil)
}
func (s *messageSource) Next(ctx context.Context) (sdk.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s.isClosed() {
		return sdk.StreamEvent{}, ErrStreamClosed
	}
	if err := ctx.Err(); err != nil {
		_ = s.Close()
		return sdk.StreamEvent{}, err
	}
	if !s.nextMu.TryLock() {
		return sdk.StreamEvent{}, errConcurrentNext
	}
	defer s.nextMu.Unlock()
	if s.isClosed() {
		return sdk.StreamEvent{}, ErrStreamClosed
	}
	select {
	case <-s.closed:
		return sdk.StreamEvent{}, ErrStreamClosed
	case <-ctx.Done():
		if s.isClosed() {
			return sdk.StreamEvent{}, ErrStreamClosed
		}
		_ = s.Close()
		return sdk.StreamEvent{}, ctx.Err()
	case ev, ok := <-s.observer.events:
		// A Close racing a ready event always wins. The initial precheck above
		// avoids returning a queued event when Close already happened; this
		// second check removes select's random ready-case choice during a race.
		if s.isClosed() {
			return sdk.StreamEvent{}, ErrStreamClosed
		}
		if !ok {
			return sdk.StreamEvent{}, s.observer.terminalError()
		}
		s.mu.Lock()
		s.sequence++
		sequence := s.sequence
		s.mu.Unlock()
		ev.Sequence, ev.Meta.Sequence = sequence, sequence
		if ev.Meta.Kind == streaming.EventNativeError {
			// finishFrame deliberately publishes native terminal state after the
			// event enters its queue. Synchronizing here ensures that, once this
			// event is returned, the next Next call observes that terminal state
			// rather than a racing SDK decoder failure.
			_ = s.observer.hasTerminal()
		}
		return ev, nil
	}
}
func (s *messageSource) Close() error {
	s.closeMu.Lock()
	if s.closing {
		s.closeMu.Unlock()
		return nil
	}
	s.closing = true
	// Signal before doing any provider cleanup. This makes every in-flight Next
	// promptly observable as closed, while concurrent Close callers return
	// rather than waiting on an official SDK Close.
	close(s.closed)
	s.closeMu.Unlock()

	// Abort the parser queue before synchronously closing the official stream:
	// a decoder blocked on observer backpressure is released before its HTTP
	// response body is closed.
	if s.observer != nil {
		s.observer.abort()
	}
	if s.opening != nil {
		s.opening.abort()
	}
	return nil
}
func (s *messageSource) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

var _ sdk.StreamClient = (*Client)(nil)
var _ sdk.StreamSource = (*messageSource)(nil)
var _ io.ReadCloser = (*observedAnthropicBody)(nil)
