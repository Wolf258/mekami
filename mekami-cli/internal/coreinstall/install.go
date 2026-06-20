package coreinstall

import (
	"fmt"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// InstallOutcome reports what Install did, so the CLI can print
// a useful one-line summary to the user.
type InstallOutcome struct {
	// Language is the normalized language identifier that ended up
	// in the config (e.g. "go").
	Language string
	// Version is the resolved semver (with "v" prefix).
	Version string
	// AlreadyPresent is true when the (lang, version) pair was
	// already in the config and Install was a no-op. The CLI
	// uses this to print a "no changes" message.
	AlreadyPresent bool
	// Upgraded is true when an existing entry for the same lang
	// was rewritten with a different version.
	Upgraded bool
	// ConfigPath is the .mekami/config.json file that was
	// (potentially) rewritten.
	ConfigPath string
	// AllGenPath is the all_gen.go file that was (potentially)
	// regenerated. Empty when nothing was written.
	AllGenPath string
}

// Install is the high-level entry point used by `core install`.
// Given a "lang@version" reference (or just "lang" for @latest),
// it resolves the version, updates the project's
// .mekami/config.json indexers[], and regenerates
// mekami-core/frontend/all_gen/all_gen.go.
//
// The function fails hard (no partial writes) if any step
// errors out, so the on-disk state is always consistent. The
// caller is expected to be running from the mekami source tree
// (or a checkout of it) so that workDir contains go.work.
func Install(workDir, ref string) (InstallOutcome, error) {
	lang, wantVer, err := SplitLangRef(ref)
	if err != nil {
		return InstallOutcome{}, err
	}
	if !IsValidLang(lang) {
		return InstallOutcome{}, fmt.Errorf("invalid language %q (must match [a-z0-9_-]+)", lang)
	}

	r := NewResolver()
	if err := r.Available(); err != nil {
		return InstallOutcome{}, err
	}
	resolved, err := r.Resolve(lang, wantVer)
	if err != nil {
		return InstallOutcome{}, err
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return InstallOutcome{}, fmt.Errorf("load config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return InstallOutcome{}, fmt.Errorf("validate config: %w", err)
	}

	out := InstallOutcome{Language: lang, Version: resolved, ConfigPath: cfgPath}

	prev, exists := cfg.Indexers[lang]
	if !exists {
		if cfg.Indexers == nil {
			cfg.Indexers = make(map[string]string)
		}
		cfg.Indexers[lang] = resolved
	} else if prev == resolved {
		out.AlreadyPresent = true
		return out, nil
	} else {
		cfg.Indexers[lang] = resolved
		out.Upgraded = true
	}

	if err := config.Save(cfg, cfgPath); err != nil {
		return InstallOutcome{}, fmt.Errorf("save config: %w", err)
	}

	allGen := AllGenPath(workDir)
	if err := WriteAllGen(workDir, cfg.Indexers); err != nil {
		return InstallOutcome{}, err
	}
	out.AllGenPath = allGen
	return out, nil
}
