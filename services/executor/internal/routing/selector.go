// Package routing contains pure routing input primitives.
package routing

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxSelectorLength = 256
	maxSegmentLength  = 128
)

// ErrInvalidSelector is returned when a model selector is malformed.
//
// It intentionally carries no input-specific context, so callers can expose it
// without reflecting potentially untrusted selector text.
var ErrInvalidSelector = errors.New("invalid selector")

// Selector is a parsed model selector. Its canonical representation is
// model[:group][@provider]. Auto is true precisely when Model is "auto".
type Selector struct {
	Model    string
	Group    string
	Provider string
	Auto     bool
}

// ParseSelector parses model[:group][@provider]. A selector may contain at
// most one group and one provider delimiter, in that order. The special model
// "auto" may be bare or specify a provider, but may not specify a group.
func ParseSelector(input string) (Selector, error) {
	if len(input) == 0 || len(input) > maxSelectorLength || !utf8.ValidString(input) {
		return Selector{}, ErrInvalidSelector
	}

	for _, r := range input {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return Selector{}, ErrInvalidSelector
		}
	}

	if strings.Count(input, "@") > 1 || strings.Count(input, ":") > 1 {
		return Selector{}, ErrInvalidSelector
	}

	modelAndGroup, provider, hasProvider := strings.Cut(input, "@")
	if hasProvider && provider == "" {
		return Selector{}, ErrInvalidSelector
	}

	model, group, hasGroup := strings.Cut(modelAndGroup, ":")
	// A group delimiter is only valid before the provider delimiter.
	if strings.Contains(provider, ":") {
		return Selector{}, ErrInvalidSelector
	}
	if model == "" || (hasGroup && group == "") {
		return Selector{}, ErrInvalidSelector
	}
	if len(model) > maxSegmentLength || len(group) > maxSegmentLength || len(provider) > maxSegmentLength {
		return Selector{}, ErrInvalidSelector
	}

	selector := Selector{
		Model:    model,
		Group:    group,
		Provider: provider,
		Auto:     model == "auto",
	}
	if selector.Auto && hasGroup {
		return Selector{}, ErrInvalidSelector
	}

	return selector, nil
}

// Canonical returns the selector in canonical grammar order.
func (s Selector) Canonical() string {
	var builder strings.Builder
	builder.Grow(len(s.Model) + len(s.Group) + len(s.Provider) + 2)
	builder.WriteString(s.Model)
	if s.Group != "" {
		builder.WriteByte(':')
		builder.WriteString(s.Group)
	}
	if s.Provider != "" {
		builder.WriteByte('@')
		builder.WriteString(s.Provider)
	}
	return builder.String()
}

// String implements fmt.Stringer using the canonical selector form.
func (s Selector) String() string {
	return s.Canonical()
}
