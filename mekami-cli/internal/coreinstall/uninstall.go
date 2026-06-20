package coreinstall

import (
	"fmt"
	"os"

	"github.com/Wolf258/mekami-cli/internal/config"
)

// UninstallOutcome reports what Uninstall did, so the CLI can
// print a useful one-line summary. The shape mirrors
// InstallOutcome (Language + ConfigPath + AllGenPath) and adds
// NotPresent, which is true when the language was not in the
// config to begin with — Uninstall treats that as a no-op and
// does not rewrite config.json or all_gen.go.
type UninstallOutcome struct {
	Language   string
	ConfigPath string
	AllGenPath string
	// NotPresent is true when the language was not in
	// .mekami/config.json indexers[]. The CLI uses this to
	// print a "no-op" message instead of an error.
	NotPresent bool
}

// Uninstall is the high-level entry point used by
// `mekami core uninstall`. It removes <lang> from the project's
// .mekami/config.json indexers[] and regenerates
// mekami-core/frontend/all_gen/all_gen.go without the blank
// import for it. Like Install, it is idempotent and fails hard
// (no partial writes) on any step error.
func Uninstall(workDir, lang string) (UninstallOutcome, error) {
	if !IsValidLang(lang) {
		return UninstallOutcome{}, fmt.Errorf("invalid language %q (must match [a-z0-9_-]+)", lang)
	}

	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return UninstallOutcome{}, fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return UninstallOutcome{}, fmt.Errorf("validate config: %w", err)
	}

	out := UninstallOutcome{Language: lang, ConfigPath: cfgPath}

	if _, exists := cfg.Indexers[lang]; !exists {
		out.NotPresent = true
		return out, nil
	}

	delete(cfg.Indexers, lang)
	if err := config.Save(cfg, cfgPath); err != nil {
		return UninstallOutcome{}, fmt.Errorf("save config: %w", err)
	}

	allGen := AllGenPath(workDir)
	// If the all_gen.go file is missing (e.g. AUR install with
	// a read-only source tree), we still consider Uninstall a
	// success once config.json is updated: the user has to
	// rebuild to load the change, and the file may live outside
	// the workDir. Report AllGenPath only when we actually
	// wrote the file.
	if _, err := os.Stat(allGen); err == nil {
		if err := WriteAllGen(workDir, cfg.Indexers); err != nil {
			return UninstallOutcome{}, err
		}
		out.AllGenPath = allGen
	}
	return out, nil
}
