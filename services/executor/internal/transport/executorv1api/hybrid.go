package executorv1api

import (
	"context"
	"net/http"
	"reflect"

	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
)

// HybridOptions configures a generated plain ServerInterface which preserves
// the existing strict-server behavior for every non-stream operation and
// dispatches schema-valid stream:true Chat/Messages requests to StreamExecutor.
// Strict is deliberately a StrictServerInterface, not an http.Handler: the
// generated strict wrapper implements each plain operation method, whereas an
// http.Handler could only route the request a second time.
type HybridOptions struct {
	Strict         executorv1.StrictServerInterface
	StreamExecutor StreamExecutor
	RequestIDs     RequestIDSource
}

// Hybrid implements the generated plain ServerInterface. Its strict member is
// a generated operation wrapper, so delegation never re-enters the router.
type Hybrid struct {
	strict         executorv1.ServerInterface
	streamExecutor StreamExecutor
	requestIDs     RequestIDSource
}

var _ executorv1.ServerInterface = (*Hybrid)(nil)

// NewHybrid creates a plain generated server that supports both existing
// non-stream behavior and HTTP SSE streaming. It intentionally does not wire
// composition or register routes.
func NewHybrid(opts HybridOptions) *Hybrid {
	strict := opts.Strict
	if isNilStrictServer(strict) {
		strict = New()
	}
	return &Hybrid{
		strict:         executorv1.NewStrictHandlerWithOptions(strict, nil, SafeStrictOptions()),
		streamExecutor: opts.StreamExecutor,
		requestIDs:     opts.RequestIDs,
	}
}

func (h *Hybrid) ExecutorGetHealthz(w http.ResponseWriter, r *http.Request) {
	h.strictServer().ExecutorGetHealthz(w, r)
}

func (h *Hybrid) ExecutorHeadHealthz(w http.ResponseWriter, r *http.Request) {
	h.strictServer().ExecutorHeadHealthz(w, r)
}

func (h *Hybrid) CreateImage(w http.ResponseWriter, r *http.Request) {
	h.strictServer().CreateImage(w, r)
}

func (h *Hybrid) ListModels(w http.ResponseWriter, r *http.Request) {
	h.strictServer().ListModels(w, r)
}

func (h *Hybrid) CreateResponse(w http.ResponseWriter, r *http.Request) {
	h.strictServer().CreateResponse(w, r)
}

func (h *Hybrid) strictServer() executorv1.ServerInterface {
	if h != nil && h.strict != nil {
		return h.strict
	}
	// This is only reachable for a manually constructed Hybrid. Preserve the
	// package's fail-closed behavior without exposing an implementation detail.
	return executorv1.NewStrictHandlerWithOptions(New(), nil, SafeStrictOptions())
}

func (h *Hybrid) requestID(ctx context.Context) string {
	if h == nil || isNilRequestIDSource(h.requestIDs) {
		return defaultRequestIDSourceInstance.RequestID(ctx)
	}
	return h.requestIDs.RequestID(ctx)
}

func isNilStrictServer(server executorv1.StrictServerInterface) bool {
	return isNilHybridValue(server)
}

func isNilStreamExecutor(executor StreamExecutor) bool {
	return isNilHybridValue(executor)
}

func isNilHybridValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return reflected.IsNil()
	default:
		return false
	}
}
