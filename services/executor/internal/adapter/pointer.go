package adapter

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

var errPointerNotFound = errors.New("JSON pointer does not exist")

func pointerGet(document any, pointer string) (any, error) {
	parts, err := pointerParts(pointer)
	if err != nil {
		return nil, err
	}
	current := document
	for _, part := range parts {
		switch node := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = node[part]
			if !ok {
				return nil, errPointerNotFound
			}
		case []any:
			index, err := arrayIndex(part, len(node), false)
			if err != nil {
				return nil, err
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("JSON pointer traverses a scalar")
		}
	}
	return current, nil
}

func pointerSet(document any, pointer string, value any) error {
	parts, err := pointerParts(pointer)
	if err != nil {
		return err
	}
	_, err = pointerMutate(document, parts, func(parent any, token string) (any, error) {
		switch node := parent.(type) {
		case map[string]any:
			node[token] = value
			return node, nil
		case []any:
			index, err := arrayIndex(token, len(node), false)
			if err != nil {
				return nil, err
			}
			node[index] = value
			return node, nil
		default:
			return nil, fmt.Errorf("JSON pointer parent is a scalar")
		}
	})
	return err
}

func pointerAppend(document any, pointer string, value any) error {
	parts, err := pointerParts(pointer)
	if err != nil {
		return err
	}
	_, err = pointerMutate(document, parts, func(parent any, token string) (any, error) {
		node, ok := parent.([]any)
		if !ok || token != "-" {
			return nil, fmt.Errorf("JSON pointer append target is not an array")
		}
		return append(node, value), nil
	})
	return err
}

func pointerRemove(document any, pointer string) error {
	parts, err := pointerParts(pointer)
	if err != nil {
		return err
	}
	_, err = pointerMutate(document, parts, func(parent any, token string) (any, error) {
		switch node := parent.(type) {
		case map[string]any:
			if _, ok := node[token]; !ok {
				return nil, fmt.Errorf("JSON pointer does not exist")
			}
			delete(node, token)
			return node, nil
		case []any:
			index, err := arrayIndex(token, len(node), false)
			if err != nil {
				return nil, err
			}
			return append(node[:index:index], node[index+1:]...), nil
		default:
			return nil, fmt.Errorf("JSON pointer parent is a scalar")
		}
	})
	return err
}

// pointerRename follows JSON Patch move ordering: remove source, then add at
// destination. Same, ancestor, and descendant paths are rejected before any
// mutation. The operation applies to a private clone and commits only after
// every step succeeds, so even an invalid destination cannot delete source.
func pointerRename(document any, from, to string, value any) error {
	fromParts, err := pointerParts(from)
	if err != nil {
		return err
	}
	toParts, err := pointerParts(to)
	if err != nil {
		return err
	}
	if pointerPartsOverlap(fromParts, toParts) {
		return fmt.Errorf("JSON pointer rename paths overlap")
	}
	root, ok := document.(map[string]any)
	if !ok {
		return fmt.Errorf("JSON pointer document is not an object")
	}
	cloned, err := deepCopyJSON(root)
	if err != nil {
		return err
	}
	moved, err := deepCopyJSON(value)
	if err != nil {
		return err
	}
	candidate, ok := cloned.(map[string]any)
	if !ok {
		return fmt.Errorf("JSON pointer document is not an object")
	}
	if err := pointerRenameInPlace(candidate, fromParts, toParts, moved); err != nil {
		return err
	}
	for key := range root {
		delete(root, key)
	}
	for key, item := range candidate {
		root[key] = item
	}
	return nil
}

func pointerRenameInPlace(document any, fromParts, toParts []string, value any) error {
	if _, err := pointerGet(document, pointerFromParts(fromParts)); err != nil {
		return err
	}
	if err := pointerRemove(document, pointerFromParts(fromParts)); err != nil {
		return err
	}
	to := pointerFromParts(toParts)
	if toParts[len(toParts)-1] == "-" {
		return pointerAppend(document, to, value)
	}
	_, err := pointerMutate(document, toParts, func(parent any, token string) (any, error) {
		switch node := parent.(type) {
		case map[string]any:
			node[token] = value
			return node, nil
		case []any:
			index, err := arrayIndex(token, len(node), true)
			if err != nil {
				return nil, err
			}
			node = append(node, nil)
			copy(node[index+1:], node[index:])
			node[index] = value
			return node, nil
		default:
			return nil, fmt.Errorf("JSON pointer parent is a scalar")
		}
	})
	return err
}

func pointerPartsOverlap(a, b []string) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pointerFromParts is only used with already-validated tokens to reuse the
// existing pointer traversal primitives without reparsing untrusted text.
func pointerFromParts(parts []string) string {
	encoded := make([]string, len(parts))
	for i, part := range parts {
		encoded[i] = strings.NewReplacer("~", "~0", "/", "~1").Replace(part)
	}
	return "/" + strings.Join(encoded, "/")
}

// pointerMutate finds a pointer's parent and writes replacement containers
// back while unwinding. It rejects document-root mutations by construction.
func pointerMutate(current any, parts []string, mutate func(any, string) (any, error)) (any, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("invalid JSON pointer")
	}
	if len(parts) == 1 {
		return mutate(current, parts[0])
	}
	token := parts[0]
	switch node := current.(type) {
	case map[string]any:
		child, ok := node[token]
		if !ok {
			return nil, fmt.Errorf("JSON pointer does not exist")
		}
		replacement, err := pointerMutate(child, parts[1:], mutate)
		if err != nil {
			return nil, err
		}
		node[token] = replacement
		return node, nil
	case []any:
		index, err := arrayIndex(token, len(node), false)
		if err != nil {
			return nil, err
		}
		replacement, err := pointerMutate(node[index], parts[1:], mutate)
		if err != nil {
			return nil, err
		}
		node[index] = replacement
		return node, nil
	default:
		return nil, fmt.Errorf("JSON pointer traverses a scalar")
	}
}

func pointerParts(pointer string) ([]string, error) {
	// Do not rely only on compiler validation: pointers in a manually-built
	// CompiledAdapter are untrusted runtime input. Bound parsing before split
	// so pathological values do not allocate an unbounded token slice.
	if pointer == "" || pointer == "/" || len(pointer) > maxJSONPointerLength || !utf8.ValidString(pointer) || !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("invalid JSON pointer")
	}
	raw := strings.Split(pointer[1:], "/")
	if len(raw) > maxJSONPointerDepth {
		return nil, fmt.Errorf("JSON pointer depth limit exceeded")
	}
	parts := make([]string, len(raw))
	for i, part := range raw {
		decoded, err := unescapePointerToken(part)
		if err != nil {
			return nil, err
		}
		// Reject prototype-family tokens so a rule path can never introduce
		// __proto__/prototype/constructor as a JSON object key in the output.
		if forbiddenName(decoded) {
			return nil, fmt.Errorf("unsafe JSON pointer token")
		}
		parts[i] = decoded
	}
	return parts, nil
}

func unescapePointerToken(token string) (string, error) {
	var out strings.Builder
	out.Grow(len(token))
	for i := 0; i < len(token); i++ {
		if token[i] != '~' {
			out.WriteByte(token[i])
			continue
		}
		if i+1 == len(token) || (token[i+1] != '0' && token[i+1] != '1') {
			return "", fmt.Errorf("invalid JSON pointer escape")
		}
		if token[i+1] == '0' {
			out.WriteByte('~')
		} else {
			out.WriteByte('/')
		}
		i++
	}
	return out.String(), nil
}

func arrayIndex(token string, length int, allowEnd bool) (int, error) {
	if token == "" || token == "-" || (len(token) > 1 && token[0] == '0') {
		return 0, fmt.Errorf("invalid JSON array index")
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 || index > length || (!allowEnd && index == length) {
		return 0, fmt.Errorf("JSON array index out of range")
	}
	return index, nil
}
