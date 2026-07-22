// Package configsource provides the strict, fail-closed file source that loads
// a raw Executor configuration snapshot from disk and bootstraps the initial
// immutable compiled snapshot.
//
// All errors returned by this package are stable sentinel values. They never
// wrap raw JSON decode errors, OS errors, or filesystem paths (via %w or in
// their message text), so a caller cannot accidentally surface sensitive
// content or path information through errors.Unwrap or Error(). The sentinels
// are also non-wrapping: errors.Unwrap returns nil for each of them.
//
// # Security model: configuration must be secret-free
//
// A raw configuration file MUST be secret-free. Every credential is an
// opaque, out-of-band reference (CredentialRef such as vault://provider/default)
// that is resolved outside the snapshot; the file itself must never carry
// secret material (API keys, bearer tokens, passwords, private keys, JWTs, or
// URLs whose query string embeds a credential). The secret scanner
// (ScanSecrets) is a fail-closed defense-in-depth gate that rejects any file
// whose raw bytes or decoded JSON tokens match a forbidden secret-bearing key,
// value marker, JWT shape, or URL query credential. It combines a raw lexical
// pass with a semantic decoded pass so JSON-escape obfuscation (e.g.
// "se\u0063ret") and URL percent-encoding (e.g. api%5Fkey) cannot bypass it.
// The compiler independently rejects any BaseURL that carries a query string,
// forced query, or fragment, and any credential reference that carries a query.
//
// # File permissions are intentionally not enforced
//
// This source deliberately does NOT enforce restrictive file permissions (such
// as 0600) on the configuration file. The repository's three sanitized
// fixtures are checked into version control with the platform default
// 0644 mode so they remain readable for tooling, diffing, and fixture-driven
// tests across environments; rejecting non-0600 regular files would make those
// fixtures unloadable in place. Instead of trusting the filesystem ACL, the
// source trusts the secret-free content invariant above: a file is safe to load
// because its content has been scanned and compiled, not because of its mode
// bits. Operators who need defense-in-depth at rest should enforce restrictive
// permissions via deployment (container image layers, configmaps, or the
// secret store backing CredentialRef), not via this loader. Symlinks,
// non-regular files, and TOCTOU path swaps are still rejected structurally
// (Lstat + post-open SameFile), because those are correctness and safety
// properties independent of the secret-free content invariant.
package configsource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/tokenmp/v3/services/executor/internal/snapshot"
)

// MaxConfigBytes bounds the size of a single raw configuration file. A file
// larger than this is rejected before its content is fully read.
const MaxConfigBytes int64 = 1 << 20 // 1 MiB

// Structural decode limits applied before schema decoding. They bound the work
// of the streaming structural walk so a hostile or pathological document cannot
// exhaust stack or memory.
const (
	// maxJSONDepth bounds object/array nesting depth. A document nested deeper
	// is rejected as malformed before schema decoding.
	maxJSONDepth = 256
	// maxJSONNodes bounds the total number of JSON tokens (keys, values, and
	// delimiters) the structural walk consumes. A document with more tokens is
	// rejected as malformed.
	maxJSONNodes = 100000
)

// Sentinel errors. Each is a non-wrapping errors.New value: errors.Unwrap on
// any of them returns nil, and no message embeds a path, OS error text, or raw
// JSON content. Callers classify with errors.Is.
var (
	// ErrConfigBlankPath is returned when the path trims to empty.
	ErrConfigBlankPath = errors.New("config source: path is blank")
	// ErrConfigNotFound is returned when the path does not exist or is not
	// accessible for stat/open.
	ErrConfigNotFound = errors.New("config source: file not found or inaccessible")
	// ErrConfigNotRegular is returned when the path is a symlink, directory,
	// device, named pipe, socket, or any other non-regular file.
	ErrConfigNotRegular = errors.New("config source: path is not a regular file")
	// ErrConfigTooLarge is returned when the file exceeds MaxConfigBytes.
	ErrConfigTooLarge = errors.New("config source: file exceeds maximum size")
	// ErrConfigEmpty is returned when the file has zero bytes.
	ErrConfigEmpty = errors.New("config source: file is empty")
	// ErrConfigUnreadable is returned when the file exists and is regular but
	// its content cannot be read.
	ErrConfigUnreadable = errors.New("config source: file could not be read")
	// ErrConfigMalformed is returned when the content is not strict, valid
	// JSON for the configuration schema: bad syntax, invalid UTF-8, unknown
	// fields, duplicate object keys, prototype-pollution keys, excessive
	// nesting/node count, or trailing data after the top-level value.
	ErrConfigMalformed = errors.New("config source: config is malformed")
	// ErrConfigSecretDetected is returned when the raw content matches the
	// forbidden secret scanner.
	ErrConfigSecretDetected = errors.New("config source: forbidden secret material detected")
	// ErrConfigCompileFailed is returned when the decoded snapshot fails
	// compiler validation/normalization.
	ErrConfigCompileFailed = errors.New("config source: config failed to compile")
	// ErrConfigPublishFailed is returned when the initial snapshot cannot be
	// atomically published (for example because the store already holds a
	// snapshot at generation >= the bootstrap generation, or the store is nil).
	ErrConfigPublishFailed = errors.New("config source: initial snapshot could not be published")
)

// protoPollutionKeys are object keys that must never appear at any depth in a
// configuration document because they are classic JavaScript prototype
// pollution vectors. They are rejected even inside map keys or free JSON where
// DisallowUnknownFields cannot see them. Matching is exact and case-sensitive
// (JSON keys are case-sensitive).
var protoPollutionKeys = map[string]struct{}{
	"__proto__":   {},
	"prototype":   {},
	"constructor": {},
}

// LoadFile reads, strictly decodes, and secret-scans a raw configuration file.
// It is fail-closed: every non-happy-path outcome is a stable sentinel error
// (or a standard context error) and never leaks the path, OS error text, or raw
// JSON content.
//
// Safety properties:
//   - The path is trimmed of surrounding whitespace and rejected as blank
//     before any filesystem access, via a stable no-leak sentinel.
//   - The path is stat-ed with Lstat; symlinks and non-regular files are
//     rejected without following links.
//   - After opening the file, a fail-closed post-open verification confirms
//     that the open descriptor is still a regular file and refers to the same
//     file identity (device+inode) as the prior Lstat via os.SameFile. This
//     closes the Lstat-then-Open TOCTOU window in which an attacker swaps the
//     path (e.g. to a symlink) between the two calls; any mismatch yields
//     ErrConfigNotRegular.
//   - The file size is bounded by MaxConfigBytes both via the stat and via a
//     LimitReader so a file that lies about its size or grows mid-read cannot
//     exhaust memory.
//   - The content is validated for strict UTF-8, then structurally walked for
//     duplicate keys, prototype-pollution keys, depth, and node count, then
//     decoded with DisallowUnknownFields and trailing-data rejection.
//   - The raw bytes are secret-scanned after a successful decode.
//   - Context cancellation is honored before stat, between stat and open,
//     after read, and between the read stage and the parse/scan/decode stage.
func LoadFile(ctx context.Context, path string) (snapshot.ConfigSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return snapshot.ConfigSnapshot{}, err
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return snapshot.ConfigSnapshot{}, ErrConfigBlankPath
	}

	info, err := os.Lstat(path)
	if err != nil {
		return snapshot.ConfigSnapshot{}, ErrConfigNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return snapshot.ConfigSnapshot{}, ErrConfigNotRegular
	}
	if !info.Mode().IsRegular() {
		return snapshot.ConfigSnapshot{}, ErrConfigNotRegular
	}
	if info.Size() > MaxConfigBytes {
		return snapshot.ConfigSnapshot{}, ErrConfigTooLarge
	}
	if info.Size() == 0 {
		return snapshot.ConfigSnapshot{}, ErrConfigEmpty
	}

	raw, err := readConfigFile(ctx, path, info)
	if err != nil {
		return snapshot.ConfigSnapshot{}, err
	}
	// Honor cancellation between the I/O read stage and the CPU-bound
	// parse/scan/decode stage so a context already canceled by the caller is
	// observed before decoding work begins. readConfigFile already honors
	// cancellation between stat and open and immediately after the read.
	if err := ctx.Err(); err != nil {
		return snapshot.ConfigSnapshot{}, err
	}
	return parseConfig(raw)
}

// readConfigFile opens and reads a regular configuration file, bounded by
// MaxConfigBytes via a LimitReader so a file that grows between stat and read
// cannot exceed the cap. It maps read failures to ErrConfigUnreadable and
// oversize reads to ErrConfigTooLarge, never leaking OS error text.
//
// lstat is the FileInfo captured by the prior os.Lstat of path. After a
// successful open the descriptor is re-stat-ed (following links) and verified
// to still be a regular file referring to the same identity (os.SameFile) as
// lstat. This closes the Lstat-then-Open TOCTOU: if the path was swapped to a
// symlink (or replaced with a different file) between the two calls, the open
// descriptor either is non-regular or has a different identity, and the call
// fails closed with ErrConfigNotRegular. No OS error text is leaked.
func readConfigFile(ctx context.Context, path string, lstat os.FileInfo) ([]byte, error) {
	// Honor cancellation between the stat and the open.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, ErrConfigUnreadable
	}
	defer f.Close()

	// Fail-closed post-open verification: the open descriptor must be a regular
	// file and must refer to the same file identity as the prior Lstat. This
	// rejects a path swapped to a symlink (or otherwise replaced) between Lstat
	// and Open. f.Stat follows symlinks, so a symlinked target that is regular
	// but a different file is caught by os.SameFile.
	fi, err := f.Stat()
	if err != nil {
		return nil, ErrConfigUnreadable
	}
	if !fi.Mode().IsRegular() || !os.SameFile(lstat, fi) {
		return nil, ErrConfigNotRegular
	}

	// LimitReader ensures we read at most MaxConfigBytes+1 bytes; the +1 lets
	// us detect an oversize file that grew since the stat.
	lr := io.LimitReader(f, MaxConfigBytes+1)
	raw, err := io.ReadAll(lr)
	if err != nil {
		return nil, ErrConfigUnreadable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(raw)) > MaxConfigBytes {
		return nil, ErrConfigTooLarge
	}
	if len(raw) == 0 {
		return nil, ErrConfigEmpty
	}
	return raw, nil
}

// parseConfig strictly decodes and secret-scans raw configuration bytes. It is
// the pure, I/O-free core of LoadFile so it can be fuzzed directly.
func parseConfig(raw []byte) (snapshot.ConfigSnapshot, error) {
	if !utf8.Valid(raw) {
		return snapshot.ConfigSnapshot{}, ErrConfigMalformed
	}
	dup, proto, tooDeep, tooMany := validateJSONStructure(raw)
	if dup || proto || tooDeep || tooMany {
		return snapshot.ConfigSnapshot{}, ErrConfigMalformed
	}
	if !topLevelIsObject(raw) {
		return snapshot.ConfigSnapshot{}, ErrConfigMalformed
	}
	var cfg snapshot.ConfigSnapshot
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return snapshot.ConfigSnapshot{}, ErrConfigMalformed
	}
	if hasTrailingData(raw[dec.InputOffset():]) {
		return snapshot.ConfigSnapshot{}, ErrConfigMalformed
	}
	if findings := ScanSecrets(raw); len(findings) != 0 {
		return snapshot.ConfigSnapshot{}, ErrConfigSecretDetected
	}
	return cfg, nil
}

// hasTrailingData reports whether rest contains any non-whitespace byte after
// the decoded top-level JSON value. This rejects trailing garbage, a second
// concatenated JSON document, or a trailing comma.
func hasTrailingData(rest []byte) bool {
	return len(bytes.TrimLeft(rest, " \t\n\r")) > 0
}

// topLevelIsObject reports whether the first non-whitespace byte of data is
// '{', i.e. the document's top-level JSON value is an object. Strict JSON for
// this source requires a single top-level object: null, scalars (numbers,
// strings, booleans), and arrays are rejected as malformed at the structural
// gate, independent of how encoding/json's Decoder.Decode(&struct{}) treats
// them. Decoder.Decode accepts null and silently leaves the struct at its zero
// value, so without this gate a top-level null would pass structural parsing
// and only fail later at bootstrap compilation. The remainder of the document
// is validated by the structural walk and the strict schema decode, so this
// check only gates the top-level shape.
func topLevelIsObject(data []byte) bool {
	trimmed := bytes.TrimLeft(data, " \t\n\r")
	return len(trimmed) > 0 && trimmed[0] == '{'
}

// validateJSONStructure walks the raw JSON token stream and reports four
// independent structural safety problems without allocating a generic tree:
//
//   - dup: any object contains a duplicate key, anywhere in the tree. Go's
//     encoding/json silently keeps the last value for duplicate keys (last
//     wins); for a strict configuration source that ambiguity is unsafe.
//   - proto: any object key is a prototype-pollution vector
//     (__proto__, prototype, constructor) at any depth, including inside map
//     keys or free JSON where DisallowUnknownFields cannot see them.
//   - tooDeep: nesting depth exceeds maxJSONDepth.
//   - tooMany: token count exceeds maxJSONNodes.
//
// If the document is malformed the walk stops early; the strict decode
// performed afterwards reports the malformation. The function never panics on
// malformed input.
func validateJSONStructure(data []byte) (dup, proto, tooDeep, tooMany bool) {
	dec := json.NewDecoder(bytes.NewReader(data))
	s := &structureStats{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		if s.bump() {
			return false, false, false, true
		}
		if d, ok := tok.(json.Delim); ok {
			r := s.walkContainer(dec, d, 1)
			dup = dup || r.dup
			proto = proto || r.proto
			tooDeep = tooDeep || r.tooDeep
			tooMany = tooMany || r.tooMany
			if tooDeep || tooMany {
				return
			}
		}
	}
}

// structureStats tracks the token count across one structural walk.
type structureStats struct{ nodes int }

// bump increments the token counter and reports whether the node budget is
// exceeded.
func (s *structureStats) bump() bool {
	s.nodes++
	return s.nodes > maxJSONNodes
}

// walkResult aggregates the four structural problems found within one container.
type walkResult struct{ dup, proto, tooDeep, tooMany bool }

// walkContainer walks the JSON container whose opening delimiter was just
// consumed. depth is the nesting depth of this container (1 for the top-level
// object/array). It mutates s to track the token budget and returns any
// structural problem found within (recursing into nested containers).
func (s *structureStats) walkContainer(dec *json.Decoder, open json.Delim, depth int) walkResult {
	var res walkResult
	if depth > maxJSONDepth {
		res.tooDeep = true
		return res
	}
	if open == '{' {
		seen := make(map[string]struct{})
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return res
			}
			if s.bump() {
				res.tooMany = true
				return res
			}
			key, ok := keyTok.(string)
			if !ok {
				return res
			}
			if _, bad := protoPollutionKeys[key]; bad {
				res.proto = true
			}
			if _, dupKey := seen[key]; dupKey {
				res.dup = true
			}
			seen[key] = struct{}{}
			valTok, err := dec.Token()
			if err != nil {
				return res
			}
			if s.bump() {
				res.tooMany = true
				return res
			}
			if d, ok := valTok.(json.Delim); ok {
				sub := s.walkContainer(dec, d, depth+1)
				res.dup = res.dup || sub.dup
				res.proto = res.proto || sub.proto
				res.tooDeep = res.tooDeep || sub.tooDeep
				res.tooMany = res.tooMany || sub.tooMany
				if res.tooDeep || res.tooMany {
					return res
				}
			}
		}
		if _, err := dec.Token(); err != nil { // consume closing '}'
			return res
		}
		if s.bump() {
			res.tooMany = true
			return res
		}
	} else { // '['
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				return res
			}
			if s.bump() {
				res.tooMany = true
				return res
			}
			if d, ok := tok.(json.Delim); ok {
				sub := s.walkContainer(dec, d, depth+1)
				res.dup = res.dup || sub.dup
				res.proto = res.proto || sub.proto
				res.tooDeep = res.tooDeep || sub.tooDeep
				res.tooMany = res.tooMany || sub.tooMany
				if res.tooDeep || res.tooMany {
					return res
				}
			}
		}
		if _, err := dec.Token(); err != nil { // consume closing ']'
			return res
		}
		if s.bump() {
			res.tooMany = true
			return res
		}
	}
	return res
}

// InitialSnapshotMeta is the package-owned, safe metadata returned after
// bootstrapping the initial compiled snapshot. It exposes only non-sensitive
// publication metadata (revision, generation, and structural counts). It never
// exposes the compiled config or any pointer into Store-owned state, so callers
// cannot mutate published configuration through it.
//
// The value is a concrete value type with unexported fields: there are no
// setters, and a returned value is an independent copy of the publication
// metadata at bootstrap time. A later Publish to the Store does not alter a
// previously returned InitialSnapshotMeta.
type InitialSnapshotMeta struct {
	revision   string
	generation uint64
	models     int
	providers  int
	routes     int
	adapters   int
}

// Revision returns the immutable source revision of the published snapshot.
func (m InitialSnapshotMeta) Revision() string { return m.revision }

// Generation returns the monotonic generation number assigned at publication.
func (m InitialSnapshotMeta) Generation() uint64 { return m.generation }

// ModelCount returns the number of compiled models in the published snapshot.
func (m InitialSnapshotMeta) ModelCount() int { return m.models }

// ProviderCount returns the number of compiled providers in the published
// snapshot.
func (m InitialSnapshotMeta) ProviderCount() int { return m.providers }

// RouteCount returns the number of compiled routes in the published snapshot.
func (m InitialSnapshotMeta) RouteCount() int { return m.routes }

// AdapterCount returns the number of compiled adapters in the published
// snapshot.
func (m InitialSnapshotMeta) AdapterCount() int { return m.adapters }

// CompileAndPublishInitial loads a configuration file with LoadFile, compiles it
// via the real snapshot compiler, and atomically publishes the resulting
// compiled snapshot as the bootstrap generation (1) of the provided Store.
//
// It is fail-closed and never wraps raw JSON/path/OS errors: the only errors it
// returns are the package sentinels (propagated from LoadFile, or
// ErrConfigCompileFailed / ErrConfigPublishFailed) plus standard context errors.
//
// On success the returned InitialSnapshotMeta carries only safe publication
// metadata (revision, generation, structural counts). It deliberately does NOT
// return the *snapshot.CompiledSnapshot or any mutable compiled config: callers
// cannot reach or mutate published configuration state through the return value.
// The Store retains an independent deep copy.
func CompileAndPublishInitial(ctx context.Context, store *snapshot.Store, path string) (InitialSnapshotMeta, error) {
	if store == nil {
		return InitialSnapshotMeta{}, ErrConfigPublishFailed
	}
	cfg, err := LoadFile(ctx, path)
	if err != nil {
		return InitialSnapshotMeta{}, err
	}
	// Trim the revision on the loaded snapshot copy before compilation so the
	// compiled config value, the published snapshot, and the store entry all
	// carry the identical trimmed revision. The global compiler only requires
	// a non-blank revision (it does not trim); NewCompiledSnapshot trims its
	// external revision argument and would otherwise disagree with the
	// untrimmed compiled.Revision value. Trimming here keeps the compiler's
	// semantics unchanged while guaranteeing meta/store/value agreement.
	cfg.Revision = strings.TrimSpace(cfg.Revision)
	// Honor cancellation between the load and compile stages of bootstrap.
	if err := ctx.Err(); err != nil {
		return InitialSnapshotMeta{}, err
	}
	compiled, err := snapshot.Compile(cfg)
	if err != nil {
		return InitialSnapshotMeta{}, ErrConfigCompileFailed
	}
	frozen, err := snapshot.NewCompiledSnapshotWithTime(cfg.Revision, &compiled, 1, cfg.CreatedAt)
	if err != nil {
		return InitialSnapshotMeta{}, ErrConfigCompileFailed
	}
	// Honor cancellation between compile and publish so a caller who canceled
	// mid-bootstrap does not publish a snapshot they no longer want.
	if err := ctx.Err(); err != nil {
		return InitialSnapshotMeta{}, err
	}
	if err := store.Publish(frozen); err != nil {
		return InitialSnapshotMeta{}, ErrConfigPublishFailed
	}
	return InitialSnapshotMeta{
		revision:   frozen.Revision(),
		generation: frozen.Generation(),
		models:     len(compiled.Models),
		providers:  len(compiled.Providers),
		routes:     len(compiled.Routes),
		adapters:   len(compiled.Adapters),
	}, nil
}
