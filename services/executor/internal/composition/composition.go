// Package composition is the Executor runtime composition root. It assembles
// the immutable compiled-snapshot store, the strict secret-free config file
// source, the credential and identity environment resolvers, the in-memory
// runtime/quarantine/quota/execution-log state, the exact official SDK
// registry, the non-stream Runner and facade, the generated strict handler,
// and the outer authentication + raw-body capture middleware into a single
// http.Handler ready to be served by app.Run.
//
// Build is fail-closed: any missing, invalid, or unsupported configuration
// returns an error and no handler. It performs no network I/O and reads no
// secrets; secrets remain call-local to the SDK adapters. Errors never wrap
// raw JSON, filesystem paths, or secret material: the config source and the
// credential/identity resolvers return stable, redacted sentinels, and this
// package wraps only those sentinels (or its own generic, non-leaking
// messages).
package composition

import (
	"context"
	"errors"
	"net/http"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/config"
	"github.com/tokenmp/v3/services/executor/internal/configsource"
	executorv1 "github.com/tokenmp/v3/services/executor/internal/contract/executorv1"
	"github.com/tokenmp/v3/services/executor/internal/credentialenv"
	"github.com/tokenmp/v3/services/executor/internal/execution"
	"github.com/tokenmp/v3/services/executor/internal/identityenv"
	"github.com/tokenmp/v3/services/executor/internal/nonstreamfacade"
	"github.com/tokenmp/v3/services/executor/internal/quarantinebridge"
	"github.com/tokenmp/v3/services/executor/internal/quota"
	"github.com/tokenmp/v3/services/executor/internal/requestlog"
	"github.com/tokenmp/v3/services/executor/internal/runtime"
	"github.com/tokenmp/v3/services/executor/internal/sdk/anthropicadapter"
	"github.com/tokenmp/v3/services/executor/internal/sdk/openaiadapter"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
	"github.com/tokenmp/v3/services/executor/internal/streamfacade"
	"github.com/tokenmp/v3/services/executor/internal/transport/executorv1api"
)

// runtimeVersion is the process-local version string surfaced through the
// runtime state port. It carries no deployment or secret meaning.
const runtimeVersion = "executor"

// Sentinel errors. Each is generic and non-leaking: it never embeds a path,
// JSON content, credential reference, or secret.
var (
	// ErrConfigSource means the initial compiled snapshot could not be loaded
	// or published. The underlying config source sentinel is wrapped; that
	// sentinel is itself non-leaking (no path/content).
	ErrConfigSource = errors.New("composition: configuration source unavailable")
	// ErrSnapshotUnavailable means no compiled snapshot is available after
	// bootstrap.
	ErrSnapshotUnavailable = errors.New("composition: compiled snapshot unavailable")
	// ErrCredentialResolver means the credential environment mapping could not
	// be constructed or validated against the compiled snapshot.
	ErrCredentialResolver = errors.New("composition: credential resolver unavailable")
	// ErrIdentityResolver means the identity environment source could not be
	// constructed.
	ErrIdentityResolver = errors.New("composition: identity resolver unavailable")
	// ErrUnsupportedRoute means an enabled route declares an SDK/protocol pair
	// for which no official non-stream adapter is registered. Only OpenAI Chat
	// (openai/openai_chat) and Anthropic Messages (anthropic/anthropic_messages)
	// are supported at runtime.
	ErrUnsupportedRoute = errors.New("composition: enabled route uses unsupported sdk or protocol")
	// ErrSDKAdapter means an official SDK adapter could not be constructed or
	// registered. This is a startup misconfiguration, not a runtime outcome.
	ErrSDKAdapter = errors.New("composition: sdk adapter unavailable")
)

// supportedSDKPairs is the exact set of (SDKKind, Protocol) pairs for which
// composition registers both official completion and stream capabilities.
// It is a defense-in-depth allowlist for the startup registry capability gate.
var supportedSDKPairs = map[execution.SDKClientKey]struct{}{
	{SDKKind: adapter.SDKKindOpenAI, Protocol: adapter.ProtocolOpenAIChat}:   {},
	{SDKKind: adapter.SDKKindAnthropic, Protocol: adapter.ProtocolAnthropic}: {},
}

// Build assembles the Executor runtime composition root and returns the
// http.Handler serving all seven generated routes: anonymous GET/HEAD
// /healthz, authenticated /v1/models|/v1/responses|/v1/images/generations
// (each 501), and authenticated Chat/Messages execution in non-stream or
// SSE stream mode. lookupEnv is the process environment accessor
// (typically os.LookupEnv); it is retained by the credential and identity
// resolvers so per-attempt/per-request secret rotation is observed.
//
// Build performs no network I/O. All errors are fail-closed and non-leaking.
func Build(ctx context.Context, cfg config.Config, lookupEnv func(string) (string, bool)) (http.Handler, error) {
	// ── Snapshot store + initial bootstrap from the strict file source ──
	store := &snapshot.Store{}
	if _, err := configsource.CompileAndPublishInitial(ctx, store, cfg.ConfigFile); err != nil {
		return nil, ErrConfigSource
	}
	current, err := store.Current()
	if err != nil {
		return nil, ErrSnapshotUnavailable
	}
	compiled := current.Value()
	if compiled == nil {
		return nil, ErrSnapshotUnavailable
	}

	// ── SDK registry + startup capability gate ──
	// Both completion and stream capabilities must be registered for every
	// enabled route before listening. This makes stream:true fail closed at
	// startup rather than discovering a partial registry at request time.
	registry, err := buildSDKRegistry()
	if err != nil {
		return nil, ErrSDKAdapter
	}
	if err := rejectUnsupportedEnabledRoutes(*compiled, registry); err != nil {
		return nil, err
	}

	// ── Credential environment resolver + startup validation ──
	credentialResolver, err := credentialenv.NewFromJSON(ctx, cfg.CredentialRefMapJSON, lookupEnv)
	if err != nil {
		return nil, ErrCredentialResolver
	}
	if err := credentialResolver.ValidateCompiled(*compiled); err != nil {
		return nil, ErrCredentialResolver
	}

	// ── Identity environment resolver (re-reads the map env internally) ──
	identitySource, err := identityenv.NewFromEnv(ctx, lookupEnv)
	if err != nil {
		return nil, ErrIdentityResolver
	}

	// ── Runtime + quarantine + quota + execution log (in-memory) ──
	statePort := runtime.NewInMemory(runtimeVersion)
	quarantineReader := quarantinebridge.New(statePort)
	quotaPort := quota.NewInMemory()
	executionLog := requestlog.NewInMemoryExecution()

	// ── Completion + stream runners and facades ──
	runner := &execution.Runner{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      executionLog,
	}
	facade := nonstreamfacade.New(nonstreamfacade.Options{
		Store:       store,
		Runner:      runner,
		Credentials: credentialResolver,
		Quarantine:  quarantineReader,
	})
	streamDriver := &execution.StreamDriver{
		Quota:       quotaPort,
		SDKRegistry: registry,
		Logger:      executionLog,
	}
	streamFacade := streamfacade.New(streamfacade.Options{
		Store:       store,
		Driver:      streamDriver,
		Credentials: credentialResolver,
		Quarantine:  quarantineReader,
	})

	// ── Generated handler with strict non-stream + SSE stream dispatch ──
	nonStreamAdapter := executorv1api.NewNonStream(executorv1api.Options{Executor: facade})
	hybrid := executorv1api.NewHybrid(executorv1api.HybridOptions{Strict: nonStreamAdapter, StreamExecutor: streamFacade})
	generated := executorv1.Handler(hybrid)

	// AuthMiddleware is the outer boundary: it protects all /v1 paths,
	// including unknown paths that will become 404, and keeps /healthz
	// anonymous. CaptureRawBody sits inside it so rejected requests never
	// read or parse their body.
	handler := executorv1api.AuthMiddleware(identitySource)(executorv1api.CaptureRawBody(generated))
	return handler, nil
}

// rejectUnsupportedEnabledRoutes fails closed if any enabled route lacks the
// exact official completion or streaming SDK capability. Disabled routes are
// not required to be supported. The error carries only
// the composition sentinel; it never names the offending route, provider, or
// adapter so misconfiguration cannot leak topology.
func rejectUnsupportedEnabledRoutes(compiled adapter.CompiledConfig, registry *execution.SDKRegistry) error {
	for _, route := range compiled.Routes {
		if !route.Enabled {
			continue
		}
		provider, ok := compiled.Providers[route.ProviderID]
		if !ok {
			// The compiler already rejects providerless enabled routes; a
			// missing provider here is a compile/config invariant violation.
			return ErrUnsupportedRoute
		}
		key := execution.SDKClientKey{SDKKind: provider.SDKKind, Protocol: route.Protocol}
		if _, supported := supportedSDKPairs[key]; !supported || registry == nil {
			return ErrUnsupportedRoute
		}
		if _, err := registry.Client(key.SDKKind, key.Protocol); err != nil {
			return ErrUnsupportedRoute
		}
		if _, err := registry.StreamClient(key.SDKKind, key.Protocol); err != nil {
			return ErrUnsupportedRoute
		}
		// The adapter's SDKKind/Protocol must also agree with the provider, an
		// invariant the compiler enforces; check it here for defense-in-depth.
		adapterEntry, ok := compiled.Adapters[route.AdapterID]
		if !ok || adapterEntry.SDKKind != provider.SDKKind || adapterEntry.Protocol != route.Protocol {
			return ErrUnsupportedRoute
		}
	}
	return nil
}

// buildSDKRegistry registers exactly the two official completion and stream
// capabilities: openai/openai_chat (openai-go v3) and
// anthropic/anthropic_messages (anthropic-sdk-go). Each pair uses the same
// target-agnostic client instance; the target URL,
// upstream model, and opaque secret are supplied per call. A construction or
// registration failure is a startup misconfiguration.
func buildSDKRegistry() (*execution.SDKRegistry, error) {
	registry := execution.NewSDKRegistry()
	openaiClient, err := openaiadapter.NewClient()
	if err != nil {
		return nil, ErrSDKAdapter
	}
	if err := registry.Register(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, openaiClient); err != nil {
		return nil, ErrSDKAdapter
	}
	if err := registry.RegisterStream(adapter.SDKKindOpenAI, adapter.ProtocolOpenAIChat, openaiClient); err != nil {
		return nil, ErrSDKAdapter
	}
	anthropicClient, err := anthropicadapter.NewClient()
	if err != nil {
		return nil, ErrSDKAdapter
	}
	if err := registry.Register(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, anthropicClient); err != nil {
		return nil, ErrSDKAdapter
	}
	if err := registry.RegisterStream(adapter.SDKKindAnthropic, adapter.ProtocolAnthropic, anthropicClient); err != nil {
		return nil, ErrSDKAdapter
	}
	return registry, nil
}
