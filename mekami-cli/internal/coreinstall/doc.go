// Package coreinstall implements `mekami core install`,
// `mekami core list`, and `mekami core uninstall` — the
// per-project indexer registry on top of
// .mekami/config.json indexers[] and the generated
// mekami-core/frontend/all_gen/all_gen.go file.
//
// Model:
//
//   - The mekami binary blank-imports the all_gen package at
//     startup. all_gen is generated; each blank import registers
//     one api.Frontend via its package's init().
//   - core install resolves a language to a Go module path
//     (github.com/Wolf258/mekami-core-<lang>) at a pinned version,
//     records it in .mekami/config.json indexers[], and rewrites
//     all_gen.go with a fresh blank import. The binary must be
//     rebuilt afterwards for the new frontend to be active;
//     core install does not recompile a binary in production (the
//     AUR-installed mekami is read-only). In dev, core install
//     runs from the mekami source tree and the next `./build.sh`
//     picks up the new all_gen.go.
//   - core uninstall removes a language from
//     .mekami/config.json indexers[] and regenerates all_gen.go
//     without the blank import for it. Idempotent: removing a
//     language that is not configured is a no-op.
//   - core list and core status read config.json and
//     api.Global.Names() to report which languages are installed,
//     which are loaded into the running binary, and which are
//     missing. core status adds a configured/loaded/missing
//     summary line; core list does not.
package coreinstall
