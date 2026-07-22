// Package nonstreamfacade is the transport-neutral composition root for
// non-stream Executor requests. It implements nonstream.Executor by pinning the
// current compiled snapshot per request, building a protocol-filtered routing
// Resolver that preserves the resolver-owned Plan, requiring and defensively
// revalidating a trusted authenticated Principal, issuing a CSPRNG reservation
// identifier, and delegating exactly one execution.Run to an injected Runner
// port.
//
// The facade owns no HTTP, database, env, main, or route registration, and it
// imports no transport code: it composes against the transport-neutral
// nonstream port so a future transport layer (or any other caller) may wire it
// without coupling. Every unsafe input (nil/typed-nil dependency, missing or
// invalid Principal, missing snapshot, unknown model) fails closed to a safe
// sentinel that a transport renderer reduces to a protocol-native response
// carrying no upstream, request, credential, or routing detail.
package nonstreamfacade

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/nonstream"
	"github.com/tokenmp/v3/services/executor/internal/requestid"
	"github.com/tokenmp/v3/services/executor/internal/routing"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// Bound the safe Principal fields. Subject mirrors identityenv's
// maxSubjectBytes (256) and KeyID its maxKeyIDBytes (128); the printable range
// mirrors identityenv's validToken (0x21..0x7e). Keeping a local copy keeps
// this package decoupled from the identity port while enforcing the same
// safe surface a facade must accept.
const (
	maxSubjectBytes = 256
	maxKeyIDBytes   = 128
)

var (
	// ErrMisconfigured means a required facade dependency is nil or typed-nil.
	// It is returned before any snapshot read, identity check, reservation, or
	// upstream call, and is rendered as a safe internal error.
	ErrMisconfigured = nonstream.ErrMisconfigured
	// ErrUnauthenticated means the request carried no trusted authenticated
	// Principal. It is rendered as a safe 401: the outer transport auth
	// boundary already rejects unauthenticated /v1 traffic with a
	// protocol-native 401, so reaching the facade without a Principal is a
	// composition fault, not a client-auth outcome, and must not disclose
	// routing or execution state.
	ErrUnauthenticated = nonstream.ErrUnauthorized
	// ErrNoSnapshot means no compiled snapshot is currently published. It is
	// rendered as a safe internal error.
	ErrNoSnapshot = errors.New("nonstreamfacade: no compiled snapshot")
	// ErrRouting means resolution failed for a reason other than model-not-found
	// (for example quarantine state unavailable). It is rendered as a safe
	// internal error and carries no routing detail.
	ErrRouting = errors.New("nonstreamfacade: routing failed")
	// ErrInvalidProtocol means the request protocol is not one of the supported
	// non-stream protocols. It is rendered as a safe internal error.
	ErrInvalidProtocol = errors.New("nonstreamfacade: invalid request protocol")
	// ErrReservationID means the reservation identifier source returned no
	// usable identifier. It is rendered as a safe internal error.
	ErrReservationID = errors.New("nonstreamfacade: reservation id unavailable")
)

// Runner is the single-call execution port. The facade invokes Run at most
// once per Execute: on the execution path it is called exactly once, and on
// every pre-execution failure path it is not called at all. The default
// implementation is *execution.Runner; tests inject a double to assert the
// exactly-once contract and to observe the pinned snapshot, resolver, and
// reservation identifier handed to it.
type Runner interface {
	Run(context.Context, execution.Input) (execution.Result, error)
}

// ReservationIDSource supplies a per-request, unguessable reservation
// identifier. A nil or typed-nil source falls back to the package default
// CSPRNG source so request handling is never blocked by a misconfigured
// injection.
type ReservationIDSource = requestid.Source

// ReservationIDSourceFunc adapts a function to ReservationIDSource. It is the
// test injection point for a deterministic or counting source.
type ReservationIDSourceFunc = requestid.SourceFunc

// Options configures a Facade. The named dependencies fall into two classes:
//
//   - Required (no safe default): Store, Runner, and Quarantine. A nil or
//     typed-nil value for any of these fails every request closed with
//     ErrMisconfigured before any snapshot read, identity check, reservation,
//     routing, or upstream call. In particular a nil or typed-nil Quarantine
//     is NEVER admissible: it would silently bypass quarantine filtering,
//     which is a security degradation. A deployment with no quarantine state
//     must inject a reader that returns routing.ErrNotFound (state consulted,
//     nothing excluded) rather than a nil reader (state skipped).
//   - Optional with a true documented default: Credentials, Clock, and
//     ReservationIDs. Only a clean, untyped nil (genuinely absent) is
//     admissible for these; a typed-nil injection is a misconfiguration and
//     also fails closed with ErrMisconfigured so an injected-but-broken value
//     can never silently degrade security.
//
// Credentials is optional because AuthNone-only configurations need no
// resolver; the Runner resolves credentials per route, so a clean nil surfaces
// as a per-route failure rather than a silent bypass. Clock is optional and
// uses wall time. ReservationIDs is optional and uses the package CSPRNG
// source, which is cryptographically equivalent to a custom source and thus
// not a security degradation.
type Options struct {
	// Store is the atomic compiled-snapshot store. The current snapshot is
	// pinned per request. Required.
	Store *snapshot.Store
	// Runner executes exactly one non-stream request lifecycle. It is the seam
	// for testability. Required.
	Runner Runner
	// Credentials resolves configured credential references into opaque
	// call-local secrets. A clean nil is admissible for AuthNone-only
	// configurations; a typed-nil injection fails closed.
	Credentials routing.CredentialResolver
	// Quarantine is the routing quarantine reader. Required: a nil or typed-nil
	// value would silently bypass quarantine filtering and fails closed.
	Quarantine routing.QuarantineReader
	// Clock supplies the instant used to evaluate quarantine expiry. A clean
	// nil or typed-nil value uses wall time (the resolver's documented default).
	Clock routing.Clock
	// ReservationIDs supplies per-request reservation identifiers. A clean nil
	// or typed-nil value uses the CSPRNG default (a true documented default).
	ReservationIDs ReservationIDSource
}

// Facade implements nonstream.Executor by composing per-request snapshot
// pinning, protocol-filtered routing, trusted-Principal gating, CSPRNG
// reservation identifiers, and a single Runner.Run delegation.
type Facade struct {
	store          *snapshot.Store
	runner         Runner
	credentials    routing.CredentialResolver
	quarantine     routing.QuarantineReader
	clock          routing.Clock
	reservationIDs ReservationIDSource
}

var _ nonstream.Executor = (*Facade)(nil)

// New returns a Facade over opts. New itself does not fail: every dependency
// is revalidated on each Execute so a facade constructed before its store is
// published still fails closed per request rather than panicking.
func New(opts Options) *Facade {
	return &Facade{
		store:          opts.Store,
		runner:         opts.Runner,
		credentials:    opts.Credentials,
		quarantine:     opts.Quarantine,
		clock:          opts.Clock,
		reservationIDs: opts.ReservationIDs,
	}
}

// Execute runs one non-stream request. It pins the current snapshot, requires
// and defensively revalidates a trusted Principal, resolves a protocol-filtered
// Plan, issues a reservation identifier, and calls Runner.Run exactly once.
// Every failure path returns a safe sentinel; the zero Result is returned
// alongside any error.
func (f *Facade) Execute(ctx context.Context, req nonstream.Request) (nonstream.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nonstream.Result{}, err
	}
	// Fail closed on a nil facade or a missing/broken required dependency
	// before any snapshot read, identity check, reservation, routing, or
	// upstream call. Store, Runner, and Quarantine are required with no safe
	// default: a nil or typed-nil Quarantine would silently bypass quarantine
	// filtering, which is a security degradation and is never admissible. A
	// typed-nil Credentials injection is also a misconfiguration that fails
	// closed; a clean untyped nil Credentials remains admissible for
	// AuthNone-only configurations. A typed-nil Runner, Store, Quarantine, or
	// Credentials wrapped in an interface would otherwise panic on dispatch.
	if f == nil || isNilStore(f.store) || isNilRunner(f.runner) || isNilQuarantine(f.quarantine) || isTypedNilCredentialResolver(f.credentials) {
		return nonstream.Result{}, ErrMisconfigured
	}
	// The request protocol selects the route universe. Only the two non-stream
	// protocols are admissible; an absent or unknown protocol fails closed
	// rather than resolving cross-protocol routes.
	if !nonStreamProtocol(req.Protocol) {
		return nonstream.Result{}, ErrInvalidProtocol
	}
	// A trusted, defensively revalidated Principal is required. The outer
	// transport auth boundary populates the Principal; reaching the facade with
	// none, or one whose status/role/subject/keyID are unsafe, is a composition
	// fault and fails closed to a safe 401. The facade must not trust the
	// transport's own revalidation: it re-checks active status, service/admin
	// role, and non-empty bounded printable subject/keyID here so a miswired
	// boundary can never drive an authenticated execution.
	if !validPrincipal(req.Principal) {
		return nonstream.Result{}, ErrUnauthenticated
	}
	// The trusted request identifier is the only request ID the Runner may
	// place on logs; reject an empty one before reservation or routing.
	if strings.TrimSpace(req.RequestID) == "" {
		return nonstream.Result{}, ErrMisconfigured
	}

	// Pin the current snapshot for this request. Current returns an independent
	// deep-cloned view, so a later publication cannot mutate this request's
	// frozen revision or generation.
	source, err := f.store.Current()
	if err != nil {
		return nonstream.Result{}, ErrNoSnapshot
	}

	// Build a resolver bound to the pinned snapshot. Its private identity
	// becomes the Plan owner, which the Runner later validates. Quarantine is
	// guaranteed non-nil by the misconfiguration gate; a typed-nil Clock is
	// normalized to nil so the resolver applies its documented wall-time
	// default rather than panicking on dispatch.
	clock := f.clock
	if isNilClock(clock) {
		clock = nil
	}
	resolver, err := routing.NewResolver(source, f.quarantine, clock)
	if err != nil {
		return nonstream.Result{}, ErrNoSnapshot
	}

	// Parse the client selector. A syntactically invalid selector is a client
	// error (400); an unknown but well-formed model is a not-found (404).
	selector, err := routing.ParseSelector(req.Selector)
	if err != nil {
		return nonstream.Result{}, nonstream.ErrInvalidRequest
	}
	// Apply the protocol filter so a chat completion request can never resolve
	// an anthropic_messages route and vice versa. The Plan remains owner-bound
	// by this resolver, preserving the Runner's ValidatePlan contract.
	selector.Protocol = req.Protocol

	plan, err := resolver.Resolve(ctx, selector)
	if err != nil {
		if errors.Is(err, routing.ErrNotFound) {
			return nonstream.Result{}, nonstream.ErrModelNotFound
		}
		// Quarantine-unavailable, snapshot-invalid, or context cancellation.
		// Context cancellation is returned unchanged so the transport makes no
		// write; every other routing failure fails closed to a safe internal
		// error carrying no routing detail.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nonstream.Result{}, err
		}
		return nonstream.Result{}, ErrRouting
	}

	// Issue the per-request reservation identifier. The Runner owns the single
	// Reserve call; the facade only supplies the identifier. The default
	// CSPRNG source draws 16 crypto-random bytes and encodes them RawURLEncoding
	// with the res_ prefix; a nil or typed-nil source falls back to it.
	reservationID := f.reservationID(ctx)
	if !requestid.ValidReservationID(reservationID) {
		return nonstream.Result{}, ErrReservationID
	}

	// Credentials is either a clean nil (admissible for AuthNone-only configs;
	// the Runner resolves per route) or a non-nil resolver: typed-nil was
	// rejected by the misconfiguration gate above, so no normalization is
	// needed and an injected-but-broken value can never reach the Runner.
	credentials := f.credentials

	in := execution.Input{
		RequestID:     req.RequestID,
		QuotaIdentity: execution.QuotaIdentity{Subject: req.Principal.Subject, KeyID: req.Principal.KeyID, Protocol: string(req.Protocol)},
		ReservationID: reservationID,
		Plan:          plan,
		Resolver:      resolver,
		Credentials:   credentials,
		Body:          req.Body,
		Thinking:      req.Thinking,
	}

	// Delegate exactly one Run. The Runner owns Reserve, retry, terminal
	// actions, and safe execution events; it returns only safe errors.
	result, err := f.runner.Run(ctx, in)
	if err != nil {
		return nonstream.Result{}, err
	}
	return result, nil
}

// reservationID returns the per-request reservation identifier, invoking the
// configured source and falling back to the CSPRNG default for a nil or
// typed-nil source.
func (f *Facade) reservationID(ctx context.Context) string {
	if f.reservationIDs != nil && !isNilReservationIDSource(f.reservationIDs) {
		return f.reservationIDs.ReservationID(ctx)
	}
	return requestid.Default.ReservationID(ctx)
}

// nonStreamProtocol reports whether p is an executable non-stream protocol.
func nonStreamProtocol(p adapter.Protocol) bool {
	return p == adapter.ProtocolOpenAIChat || p == adapter.ProtocolAnthropic || p == adapter.ProtocolOpenAIImages
}

// validPrincipal reports whether p is a trusted, active service/admin caller
// with non-empty bounded printable subject and keyID. It is defense-in-depth:
// the transport auth boundary already checks these, but the facade revalidates
// so a miswired boundary can never drive an authenticated execution. The
// bounds and printable range mirror identityenv's safe surface.
func validPrincipal(p nonstream.Principal) bool {
	if p.Status != nonstream.StatusActive {
		return false
	}
	if p.Role != nonstream.RoleService && p.Role != nonstream.RoleAdmin {
		return false
	}
	return validSafeToken(p.Subject, maxSubjectBytes) && validSafeToken(p.KeyID, maxKeyIDBytes)
}

// validSafeToken reports whether v is non-empty, bounded, UTF-8 valid, and
// printable (0x21..0x7e), mirroring identityenv.validToken.
func validSafeToken(v string, max int) bool {
	if len(v) == 0 || len(v) > max || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

// isNilStore reports whether store is nil or a typed-nil *snapshot.Store.
func isNilStore(store *snapshot.Store) bool {
	if store == nil {
		return true
	}
	v := reflect.ValueOf(store)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// isNilRunner reports whether runner is an untyped nil interface or a typed-nil
// value wrapped in the Runner interface.
func isNilRunner(runner Runner) bool {
	if runner == nil {
		return true
	}
	v := reflect.ValueOf(runner)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

// isNilQuarantine reports whether q is an untyped nil or a typed-nil value
// wrapped in the routing.QuarantineReader interface. The facade requires a
// concrete reader: a nil quarantine silently bypasses quarantine filtering
// and is never admissible.
func isNilQuarantine(q routing.QuarantineReader) bool {
	if q == nil {
		return true
	}
	v := reflect.ValueOf(q)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

// isTypedNilCredentialResolver reports whether resolver is a typed-nil value
// wrapped in the routing.CredentialResolver interface. A clean untyped nil is
// admissible for AuthNone-only configurations and returns false; a typed-nil
// injection is a misconfiguration that fails closed so an injected-but-broken
// resolver can never silently degrade to a no-credentials execution.
func isTypedNilCredentialResolver(resolver routing.CredentialResolver) bool {
	if resolver == nil {
		return false
	}
	v := reflect.ValueOf(resolver)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

// isNilClock reports whether clock is an untyped nil or a typed-nil value
// wrapped in the routing.Clock interface. Clock is not security-sensitive; a
// nil or typed-nil value is normalized to the resolver's documented wall-time
// default rather than panicking on dispatch.
func isNilClock(clock routing.Clock) bool {
	if clock == nil {
		return true
	}
	v := reflect.ValueOf(clock)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}

// isNilReservationIDSource reports whether source is a typed-nil value wrapped
// in the ReservationIDSource interface.
func isNilReservationIDSource(source ReservationIDSource) bool {
	if source == nil {
		return true
	}
	v := reflect.ValueOf(source)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func:
		return v.IsNil()
	}
	return false
}
