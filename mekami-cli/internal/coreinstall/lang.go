package coreinstall

import (
	"fmt"
	"sort"
	"strings"
)

// ModulePath returns the Go module path for a given language
// indexer (e.g. "go" -> "github.com/Wolf258/mekami-core-go").
func ModulePath(lang string) string {
	return "github.com/Wolf258/mekami-core-" + lang
}

// IsValidLang returns true if lang is a legal indexer identifier.
// Same rule the config package uses for indexer names so a
// generated blank import is always syntactically valid.
func IsValidLang(lang string) bool {
	if lang == "" {
		return false
	}
	for _, r := range lang {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return true
}

// SplitLangRef splits a "lang@version" reference into its parts.
// Empty version means "latest" and must be resolved by the caller.
func SplitLangRef(ref string) (lang, version string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", fmt.Errorf("empty language reference")
	}
	i := strings.IndexByte(ref, '@')
	if i < 0 {
		return ref, "", nil
	}
	return ref[:i], ref[i+1:], nil
}

// sortIndexers sorts the indexers in place: by Name. The result
// is a fresh slice; the input is not modified.
func sortIndexers(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
