package coreinstall

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// Resolver maps a language and an "@latest" / explicit version
// into a concrete semver tag, talking to the Go module proxy via
// `go list -m -versions`. The resolver is stateless; the methods
// are safe for concurrent use (each invokes a fresh `go list`).
type Resolver struct{}

// NewResolver returns a ready-to-use Resolver.
func NewResolver() *Resolver { return &Resolver{} }

// Available reports whether the `go` tool is on PATH and can be
// invoked. core-install short-circuits with a clear error when it
// is not, rather than failing deep in a go list call.
func (r *Resolver) Available() error {
	out, err := exec.Command("go", "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("go tool not available: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListVersions returns the published versions of the language
// module in ascending semver order (without the leading "v" in
// the slice elements, but the strings retain the prefix `go
// list` emits).
func (r *Resolver) ListVersions(lang string) ([]string, error) {
	mod := ModulePath(lang)
	out, err := exec.Command("go", "list", "-m", "-versions", mod).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// `go list` prints "no versions found" to stdout when the
		// module exists but has no tagged releases. Distinguish
		// that from "module does not exist" so the caller can pick
		// a useful error message.
		if strings.Contains(msg, "no versions found") {
			return nil, fmt.Errorf("%s: no published versions (module exists but has no tagged releases)", mod)
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%s: %s", mod, msg)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, fmt.Errorf("%s: empty version list", mod)
	}
	// Output shape: "<mod>: <v1> <v2> ..." or "<mod>" (no versions).
	colon := strings.IndexByte(line, ':')
	if colon < 0 {
		// `go list` with GOPROXY=off and a local module that has no
		// published tags returns just the module path. Treat that
		// as "no versions" rather than a parse error.
		if line == mod {
			return nil, fmt.Errorf("%s: no published versions (GOPROXY may be off or module unpublished)", mod)
		}
		return nil, fmt.Errorf("%s: unexpected `go list` output %q", mod, line)
	}
	rest := strings.TrimSpace(line[colon+1:])
	if rest == "" {
		return nil, fmt.Errorf("%s: no published versions", mod)
	}
	return strings.Fields(rest), nil
}

// Latest returns the highest semver version in the published
// list. Empty slice or parse errors produce a clear error.
func (r *Resolver) Latest(lang string) (string, error) {
	vs, err := r.ListVersions(lang)
	if err != nil {
		return "", err
	}
	best, err := highestVersion(vs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ModulePath(lang), err)
	}
	return best, nil
}

// Resolve returns the version to use for `lang@version`.
// Empty version triggers a proxy lookup for the latest. The
// returned string includes the "v" prefix (e.g. "v0.1.0") to
// match what `go list` emits and what gets written into the
// generated blank import comment.
func (r *Resolver) Resolve(lang, version string) (string, error) {
	if !IsValidLang(lang) {
		return "", fmt.Errorf("invalid language %q (must match [a-z0-9_-]+)", lang)
	}
	if version == "" {
		return r.Latest(lang)
	}
	return normalizeVersion(version)
}

// normalizeVersion ensures the version string is in `vX.Y.Z` form
// (the same shape `go list -m -versions` produces and that
// config.isValidIndexerVersion accepts). Inputs like "0.1.0"
// gain the leading "v"; "v1" stays as is; "v0.1.0-rc1" passes
// through unchanged.
func normalizeVersion(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("empty version")
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	// Loose check: starts with v, the rest is [0-9.] plus an
	// optional pre-release suffix. The full semver grammar is
	// larger; this matches what we need for an indexer version.
	body := v[1:]
	if body == "" {
		return "", fmt.Errorf("malformed version %q", v)
	}
	for i, r := range body {
		if (r >= '0' && r <= '9') || r == '.' {
			continue
		}
		// pre-release separator: '-' starts the suffix
		if r == '-' && i > 0 {
			break
		}
		return "", fmt.Errorf("malformed version %q", v)
	}
	return v, nil
}

// semverRe matches a version's major/minor/patch components.
// Used by highestVersion to pick the maximum.
var semverRe = regexp.MustCompile(`^v(\d+)\.(\d+)(?:\.(\d+))?`)

// highestVersion returns the lexicographically-and-numerically
// maximum version from vs. The input is what `go list -m
// -versions` emits (sorted ascending), so picking the last
// element is normally enough; this helper is defensive in case
// the proxy ever returns unsorted lists.
func highestVersion(vs []string) (string, error) {
	if len(vs) == 0 {
		return "", fmt.Errorf("no versions to choose from")
	}
	best := vs[0]
	bestMaj, bestMin, bestPat := parseTriplet(best)
	for _, v := range vs[1:] {
		maj, min, pat := parseTriplet(v)
		if maj > bestMaj ||
			(maj == bestMaj && min > bestMin) ||
			(maj == bestMaj && min == bestMin && pat > bestPat) {
			best, bestMaj, bestMin, bestPat = v, maj, min, pat
		}
	}
	return best, nil
}

func parseTriplet(v string) (int, int, int) {
	m := semverRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0, 0
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return maj, min, pat
}

// EnsureGoGet is a no-op helper kept for future expansion. The
// `core-install` flow does not need to `go get` anything: the
// generated blank import in all_gen.go is resolved by the Go
// toolchain at build time of the consumer (mekami-cli). A future
// "side-store" path that pre-downloads modules would call
// `go mod download` from a shim go.mod here.
func (r *Resolver) EnsureGoGet(lang, version string) error {
	// Reserved for future side-store implementation. See the
	// package doc comment for the architectural note.
	var buf bytes.Buffer
	buf.WriteString("reserved: ")
	buf.WriteString(ModulePath(lang))
	buf.WriteByte('@')
	buf.WriteString(version)
	_ = buf.Bytes()
	return nil
}
