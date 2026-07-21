// Package routing resolves a frozen compiled snapshot into safe upstream candidates.
package routing

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/tokenmp/v3/services/executor/internal/adapter"
	"github.com/tokenmp/v3/services/executor/internal/runtime"
	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

var (
	// ErrNotFound means the resolver found no usable candidate. It is also the
	// sentinel a QuarantineReader returns when no state has been stored.
	ErrNotFound = runtime.ErrNotFound
	// ErrInvalidSnapshot means a resolver cannot safely pin its input snapshot.
	ErrInvalidSnapshot = errors.New("routing resolver requires a compiled snapshot")
	// ErrQuarantineUnavailable means quarantine state could not be read. It
	// fails resolution closed rather than risking a known-bad target.
	ErrQuarantineUnavailable = errors.New("routing quarantine state unavailable")
	// ErrInvalidPlan means the plan did not originate from this resolver's
	// private frozen identity and pins. It deliberately discloses no plan data.
	ErrInvalidPlan = errors.New("routing plan is not available")
)

// Clock supplies the instant used to evaluate quarantine expiry.
type Clock interface{ Now() time.Time }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// QuarantineTarget names exactly one routable target. It intentionally has no
// string serialization: callers must preserve all four dimensions when reading
// runtime state.
type QuarantineTarget struct {
	ModelID      string
	ProviderID   string
	RouteID      string
	CredentialID string
}

// Quarantine is a temporary target exclusion. An exclusion is active only when
// Until is strictly after the resolver clock's current time.
type Quarantine struct{ Until time.Time }

// QuarantineReader supplies temporary exclusions. Implementations must return
// ErrNotFound when no state exists for target.
type QuarantineReader interface {
	GetQuarantine(context.Context, QuarantineTarget) (Quarantine, error)
}

// Credential identifies a credential without exposing its reference.
type Credential struct {
	ID       string
	Priority int
}

// Provider is the safe provider identity attached to a selected route.
type Provider struct {
	ID       string
	Selector string
}

// Candidate contains no credential material. It pins the snapshot revision and
// generation used to construct the plan.
type Candidate struct {
	ModelID    string
	Provider   Provider
	RouteID    string
	Group      string
	Credential Credential
	AdapterID  string
	Upstream   string
	Protocol   adapter.Protocol
	Priority   int
	Revision   string
	Generation uint64
}

// Target returns Candidate's complete quarantine identity. ProviderID is the
// immutable provider ID, not its request selector; AuthNone candidates retain
// their intentionally empty CredentialID.
func (c Candidate) Target() QuarantineTarget {
	return QuarantineTarget{ModelID: c.ModelID, ProviderID: c.Provider.ID, RouteID: c.RouteID, CredentialID: c.Credential.ID}
}

func (c Candidate) target() QuarantineTarget { return c.Target() }

// candidateState retains the private retry universe. It deliberately contains
// only a safe Candidate: credential resolution belongs to a future port.
type candidateState struct {
	candidate Candidate
}

// Plan is a revision-pinned, deterministically ordered candidate list. Its
// retry metadata is private so callers cannot broaden the selected snapshot.
type Plan struct {
	Revision   string
	Generation uint64
	Candidates []Candidate

	owner              *resolverIdentity
	auto               bool
	autoModelIDs       []string
	fallbackModels     map[string][]string
	fallbackRoutes     map[string][]string
	fallbackCandidates []candidateState
}

// Next returns the next allowed candidate for action. current identifies the
// attempted target; visited excludes prior targets for advancing actions.
// RetrySameCredential intentionally permits current even when it is visited.
func (p Plan) Next(action adapter.RetryAction, current QuarantineTarget, visited map[QuarantineTarget]struct{}) (Candidate, bool) {
	findCurrent := func() (candidateState, bool) {
		for _, state := range p.fallbackCandidates {
			if state.candidate.target() == current {
				return state, true
			}
		}
		return candidateState{}, false
	}
	currentCandidate, found := findCurrent()
	if !found {
		return Candidate{}, false
	}
	usable := func(state candidateState) bool {
		_, seen := visited[state.candidate.target()]
		return !seen
	}
	first := func(match func(Candidate) bool) (Candidate, bool) {
		for _, state := range p.fallbackCandidates {
			if match(state.candidate) && usable(state) {
				return state.candidate, true
			}
		}
		return Candidate{}, false
	}

	switch action {
	case adapter.RetrySameCredential:
		return currentCandidate.candidate, true
	case adapter.RetryNextCredential:
		return first(func(candidate Candidate) bool {
			return candidate.ModelID == currentCandidate.candidate.ModelID && candidate.Provider.ID == currentCandidate.candidate.Provider.ID && candidate.RouteID == currentCandidate.candidate.RouteID && candidate.Credential.ID != currentCandidate.candidate.Credential.ID
		})
	case adapter.RetryNextRoute:
		for _, routeID := range p.fallbackRoutes[currentCandidate.candidate.RouteID] {
			if candidate, ok := first(func(candidate Candidate) bool {
				return candidate.ModelID == currentCandidate.candidate.ModelID && candidate.Group == currentCandidate.candidate.Group && candidate.RouteID == routeID
			}); ok {
				return candidate, true
			}
		}
		return first(func(candidate Candidate) bool {
			return candidate.ModelID == currentCandidate.candidate.ModelID && candidate.Group == currentCandidate.candidate.Group && candidate.RouteID != currentCandidate.candidate.RouteID
		})
	case adapter.RetryNextProvider:
		return first(func(candidate Candidate) bool {
			return candidate.ModelID == currentCandidate.candidate.ModelID && candidate.Group == currentCandidate.candidate.Group && candidate.Provider.ID != currentCandidate.candidate.Provider.ID
		})
	case adapter.RetryNextModel:
		modelIDs := p.fallbackModels[currentCandidate.candidate.ModelID]
		if p.auto {
			for index, modelID := range p.autoModelIDs {
				if modelID == currentCandidate.candidate.ModelID {
					modelIDs = p.autoModelIDs[index+1:]
					break
				}
			}
		}
		for _, modelID := range modelIDs {
			if candidate, ok := first(func(candidate Candidate) bool {
				return candidate.ModelID == modelID && candidate.Group == currentCandidate.candidate.Group
			}); ok {
				return candidate, true
			}
		}
	}
	return Candidate{}, false
}

// resolverIdentity is an unexported, allocation-unique capability. Plans copy
// its pointer by value, allowing value clones to remain valid while preventing
// another Resolver (even over the same snapshot) from accepting the plan.
type resolverIdentity struct{ _ byte }

// Resolver owns a deep-copied compiled config, so later Store publications or
// callers mutating a snapshot Value cannot affect an in-flight resolver.
type Resolver struct {
	identity   *resolverIdentity
	revision   string
	generation uint64
	config     *snapshot.CompiledConfig
	quarantine QuarantineReader
	clock      Clock
}

// NewResolver freezes the supplied snapshot's revision, generation, and
// compiled config. A nil Clock uses wall time; a nil QuarantineReader means no
// quarantine state is consulted.
func NewResolver(source *snapshot.CompiledSnapshot, quarantine QuarantineReader, clock Clock) (*Resolver, error) {
	if source == nil || strings.TrimSpace(source.Revision()) == "" || source.Generation() == 0 {
		return nil, ErrInvalidSnapshot
	}
	config := source.Value()
	if config == nil {
		return nil, ErrInvalidSnapshot
	}
	if clock == nil {
		clock = wallClock{}
	}
	return &Resolver{identity: &resolverIdentity{}, revision: source.Revision(), generation: source.Generation(), config: config, quarantine: quarantine, clock: clock}, nil
}

// ValidatePlan confirms that plan was issued by this exact Resolver and is
// pinned to its current frozen revision and generation. It is intentionally
// safe to return directly: it includes neither selectors nor routing data.
func (r *Resolver) ValidatePlan(plan Plan) error {
	if r == nil || r.config == nil || r.identity == nil || strings.TrimSpace(r.revision) == "" || r.generation == 0 || plan.owner != r.identity || plan.Revision != r.revision || plan.Generation != r.generation {
		return ErrInvalidPlan
	}
	return nil
}

// Resolve builds enabled, non-quarantined candidates for selector. Quarantine
// read failures fail closed; context cancellation is returned unchanged.
func (r *Resolver) Resolve(ctx context.Context, selector Selector) (Plan, error) {
	if r == nil || r.config == nil || strings.TrimSpace(r.revision) == "" || r.generation == 0 {
		return Plan{}, ErrInvalidSnapshot
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, err
	}
	plan := Plan{Revision: r.revision, Generation: r.generation, owner: r.identity, auto: selector.Auto, autoModelIDs: append([]string(nil), r.config.AutoModelIDs...), fallbackModels: make(map[string][]string, len(r.config.Models)), fallbackRoutes: make(map[string][]string, len(r.config.Routes))}
	for modelID, model := range r.config.Models {
		plan.fallbackModels[modelID] = append([]string(nil), model.FallbackModelIDs...)
	}
	candidateModels := plan.candidateModels(selector)
	autoRank := make(map[string]int, len(r.config.AutoModelIDs))
	if selector.Auto {
		for index, modelID := range r.config.AutoModelIDs {
			autoRank[modelID] = index
		}
	}
	for _, route := range r.config.Routes {
		plan.fallbackRoutes[route.ID] = append([]string(nil), route.FallbackRouteIDs...)
		_, autoEligible := autoRank[route.ModelID]
		if !route.Enabled || (selector.Group != "" && route.RouteGroup != selector.Group) {
			continue
		}
		// A non-empty programmatic protocol filter admits only routes whose
		// compiled protocol matches the request protocol, so a chat completion
		// request can never resolve an anthropic_messages route and vice versa.
		if selector.Protocol != "" && route.Protocol != selector.Protocol {
			continue
		}
		provider, providerOK := r.config.Providers[route.ProviderID]
		if !providerOK || provider.ID == "" || (selector.Provider != "" && provider.Selector != selector.Provider) {
			continue
		}
		credentials := route.Credentials
		// AuthNone routes compile without credential entries. They still require a
		// routable candidate, whose zero Credential deliberately represents no
		// credential rather than an authenticated empty credential.
		if compiledAdapter, ok := r.config.Adapters[route.AdapterID]; ok && compiledAdapter.Auth.Kind == adapter.AuthNone {
			credentials = []adapter.CompiledCredential{{Enabled: true}}
		}
		for _, credential := range credentials {
			if !credential.Enabled {
				continue
			}
			state := candidateState{
				candidate: Candidate{ModelID: route.ModelID, Provider: Provider{ID: provider.ID, Selector: provider.Selector}, RouteID: route.ID, Group: route.RouteGroup, Credential: Credential{ID: credential.ID, Priority: credential.Priority}, AdapterID: route.AdapterID, Upstream: route.UpstreamModel, Protocol: route.Protocol, Priority: route.Priority, Revision: r.revision, Generation: r.generation},
			}
			excluded, err := r.quarantined(ctx, state.candidate)
			if err != nil {
				return Plan{}, err
			}
			if excluded {
				continue
			}
			if !candidateModels[route.ModelID] {
				continue
			}
			plan.fallbackCandidates = append(plan.fallbackCandidates, state)
			if (selector.Auto && autoEligible) || (!selector.Auto && (selector.Model == "" || route.ModelID == selector.Model)) {
				plan.Candidates = append(plan.Candidates, state.candidate)
			}
		}
	}
	if selector.Auto {
		sort.SliceStable(plan.Candidates, func(i, j int) bool {
			return autoRank[plan.Candidates[i].ModelID] < autoRank[plan.Candidates[j].ModelID]
		})
		sort.SliceStable(plan.fallbackCandidates, func(i, j int) bool {
			return autoRank[plan.fallbackCandidates[i].candidate.ModelID] < autoRank[plan.fallbackCandidates[j].candidate.ModelID]
		})
	}
	if len(plan.Candidates) == 0 {
		return plan, ErrNotFound
	}
	return plan, nil
}

// candidateModels returns the selector-scoped, transitive model fallback universe.
// It is frozen into Plan so retries never consult live resolver configuration.
func (p Plan) candidateModels(selector Selector) map[string]bool {
	models := make(map[string]bool, len(p.fallbackModels))
	if selector.Auto {
		for _, modelID := range p.autoModelIDs {
			models[modelID] = true
		}
		return models
	}
	if selector.Model == "" {
		for modelID := range p.fallbackModels {
			models[modelID] = true
		}
		return models
	}
	var add func(string)
	add = func(modelID string) {
		if models[modelID] {
			return
		}
		models[modelID] = true
		for _, fallbackID := range p.fallbackModels[modelID] {
			add(fallbackID)
		}
	}
	add(selector.Model)
	return models
}

func (r *Resolver) quarantined(ctx context.Context, candidate Candidate) (bool, error) {
	if r.quarantine == nil {
		return false, nil
	}
	targets := []QuarantineTarget{
		{ModelID: candidate.ModelID},
		{ProviderID: candidate.Provider.ID},
		{RouteID: candidate.RouteID},
	}
	// An AuthNone candidate has no credential dimension. Querying an empty
	// credential would conflate it with unrelated no-credential routes.
	if candidate.Credential.ID != "" {
		targets = append(targets, QuarantineTarget{CredentialID: candidate.Credential.ID})
	}
	for _, target := range targets {
		state, err := r.quarantine.GetQuarantine(ctx, target)
		if err == nil {
			if state.Until.After(r.clock.Now()) {
				return true, nil
			}
			continue
		}
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		if contextErr := ctx.Err(); contextErr != nil {
			return false, contextErr
		}
		return false, ErrQuarantineUnavailable
	}
	return false, nil
}
