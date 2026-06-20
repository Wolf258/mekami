package coreinstall

import (
	"testing"
)

func TestSplitLangRef(t *testing.T) {
	cases := []struct {
		in        string
		lang      string
		version   string
		wantError bool
	}{
		{"go", "go", "", false},
		{"go@v0.1.0", "go", "v0.1.0", false},
		{"rust@1.2.3", "rust", "1.2.3", false},
		{"  go  ", "go", "", false},
		{"go@v0.1.0-rc1", "go", "v0.1.0-rc1", false},
		{"", "", "", true},
	}
	for _, c := range cases {
		lang, ver, err := SplitLangRef(c.in)
		if (err != nil) != c.wantError {
			t.Errorf("SplitLangRef(%q) err=%v, wantError=%v", c.in, err, c.wantError)
			continue
		}
		if lang != c.lang || ver != c.version {
			t.Errorf("SplitLangRef(%q) = (%q,%q), want (%q,%q)", c.in, lang, ver, c.lang, c.version)
		}
	}
}

func TestIsValidLang(t *testing.T) {
	good := []string{"go", "rust", "c", "cpp2", "zig-2024", "my_lang", "x"}
	bad := []string{"", "Go", "go!", "go/rust", "go rust"}
	for _, s := range good {
		if !IsValidLang(s) {
			t.Errorf("IsValidLang(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsValidLang(s) {
			t.Errorf("IsValidLang(%q) = true, want false", s)
		}
	}
}

func TestNormalizeVersion(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"v0.1.0", "v0.1.0", false},
		{"0.1.0", "v0.1.0", false},
		{"1.2", "v1.2", false},
		{"v1", "v1", false},
		{"v0.1.0-rc1", "v0.1.0-rc1", false},
		{"", "", true},
		{"v", "", true},
		{"vX.Y.Z", "", true},
	}
	for _, c := range cases {
		got, err := normalizeVersion(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeVersion(%q) err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHighestVersion(t *testing.T) {
	vs := []string{"v0.1.0", "v0.1.1", "v0.2.0", "v1.0.0", "v0.9.0", "v0.2.0"}
	best, err := highestVersion(vs)
	if err != nil {
		t.Fatalf("highestVersion: %v", err)
	}
	if best != "v1.0.0" {
		t.Errorf("highestVersion = %q, want v1.0.0", best)
	}
	// Unparseable versions fall back to the last element picked
	// from the parseTriplet return value of 0,0,0 — defensive.
	best, err = highestVersion([]string{"v0.1.0", "garbage", "v0.1.1"})
	if err != nil {
		t.Fatalf("highestVersion mixed: %v", err)
	}
	if best != "v0.1.1" {
		t.Errorf("highestVersion mixed = %q, want v0.1.1", best)
	}
	if _, err := highestVersion(nil); err == nil {
		t.Errorf("highestVersion(nil) should error")
	}
}
