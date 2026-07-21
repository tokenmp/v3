package executorv1api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"reflect"
)

// defaultRequestIDSource generates an opaque, service-local request identifier
// using crypto/rand. It is the fallback when an Adapter has no RequestIDSource
// injected; identity and idempotency remain outside this transport package.
type defaultRequestIDSource struct{}

// requestIDPrefix marks the identifier as service-generated and never accepted
// from a client request. The suffix is unguessable random material.
const requestIDPrefix = "req_"

func (defaultRequestIDSource) RequestID(context.Context) string {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		// A non-cryptographic failure must not block the request; the renderer
		// omits request_id when SafeRequestID receives an empty string.
		return ""
	}
	return requestIDPrefix + base64.RawURLEncoding.EncodeToString(buffer[:])
}

var defaultRequestIDSourceInstance RequestIDSource = defaultRequestIDSource{}

// requestID returns the trusted request identifier for this request, invoking
// the configured RequestIDSource at most once. A nil or typed-nil source is
// treated as absent and falls back to the package default, avoiding a
// nil-pointer dereference panic. This is a safe, recoverable degradation and
// differs from the Executor, which is fail-closed: a missing request ID source
// must never block or reveal a request, whereas a missing executor must never
// attempt an execution.
func (a *Adapter) requestID(ctx context.Context) string {
	if isNilRequestIDSource(a.requestIDs) {
		return defaultRequestIDSourceInstance.RequestID(ctx)
	}
	return a.requestIDs.RequestID(ctx)
}

// isNilRequestIDSource reports whether source is an untyped nil interface or a
// typed-nil value wrapped in the interface (for example a typed pointer or
// function value that is nil). A typed-nil RequestIDSource would otherwise
// panic when its method is dispatched; falling back keeps request handling
// non-blocking and non-leaking.
func isNilRequestIDSource(source RequestIDSource) bool {
	if source == nil {
		return true
	}
	value := reflect.ValueOf(source)
	switch value.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return value.IsNil()
	}
	return false
}

// isNilExecutor reports whether executor is nil or a typed-nil value wrapped in
// the interface, so a misconfigured non-stream Adapter fails closed instead of
// panicking on a nil-pointer dereference at Execute time.
func isNilExecutor(executor NonStreamExecutor) bool {
	if executor == nil {
		return true
	}
	value := reflect.ValueOf(executor)
	switch value.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return value.IsNil()
	}
	return false
}
