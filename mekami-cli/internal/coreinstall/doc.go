// Package coreinstall implements `mekami core-install` and
// `mekami core-list` — the per-project indexer registry on top
// of .mekami/config.json indexers[] and the generated
// mekami-core/frontend/all_gen/all_gen.go file.
//
// Model:
//
//   - The mekami binary blank-imports the all_gen package at
//     startup. all_gen is generated; each blank import registers
//     one api.Frontend via its package's init().
//   - core-install resolves a language to a Go module path
//     (github.com/Wolf258/mekami-core-<lang>) at a pinned version,
//     records it in .mekami/config.json indexers[], and rewrites
//     all_gen.go with a fresh blank import. The binary must be
//     rebuilt afterwards for the new frontend to be active;
//     core-install does not recompile a binary in production (the
//     AUR-installed mekami is read-only). In dev, core-install
//     runs from the mekami source tree and the next `./build.sh`
//     picks up the new all_gen.go.
//   - core-list reads config.json and api.Global.Names() to
//     report which languages are installed, which are loaded into
//     the running binary, and which are missing.
package coreinstall
