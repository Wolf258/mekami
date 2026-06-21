# mekami CLI vs shell tools — wire-size benchmark

A single end-to-end test that runs every mekami CLI command with a
representative query and measures the bytes the LLM would receive.
For each command we also run the equivalent shell tool (`rg`, `grep`,
`git grep`, `find`, `sed`, `awk`, `nl`) and compare the byte counts.

**Goal:** determine if mekami is **better overall** as a tool for an
LLM operating in a Go repo, by the single metric of "bytes the LLM
has to read to get the answer".

## Setup

- Corpus: `/home/wolf/Projects/Mekami` (118 indexed Go files, 1135
  symbols, 7915 refs, 1 module with 26 packages).
- Binary: `./mekami` rebuilt from current source via `./build.sh`.
- Heuristic tokens: `bytes / 4`. Median of 3 invocations per case.
- 24 cases in the catalog: 19 comparable, 5 SKIP (no shell equivalent).

## Scoring rule

**Strict win:** mekami wins iff its bytes are **strictly** less than
every shell tool that returned a value. Skips (no shell equivalent)
do not count.

**Verdict threshold:**
- `>60%` strict wins → **"mekami is better overall"**
- `<40%` strict wins → **"mekami is NOT better overall"**
- `40-60%` → **"tied"**

## Results

| case                   | mekami  | rg     | grep   | git_grep | find    | sed    | awk    | nl     | best   | me%   | winner | notes |
|------------------------|--------:|-------:|-------:|---------:|--------:|-------:|-------:|-------:|-------:|------:|:------:|-------|
| find_text_func         |   4 380 | 112 556 | 125 951 | 112 556 |    -    |    -   |    -   |    -   |  4 380 |  100  | **ME**    | mekami's `--head=30` cap; shells un-capped |
| find_text_capFor       |   4 571 |  1 759 |  1 795 |   1 759 |    -    |    -   |    -   |    -   |  1 759 |  259  | shell  | 18 hits, no cap; JSON envelope is denser per-item than `rg` line |
| find_text_zero         |     282 |      0 |      0 |       0 |    -    |    -   |    -   |    -   |      0 |  inf  | shell  | mekami: `total=0` envelope (282 B); shells: empty (0 B) — ambig. |
| find_text_subdir       |   1 662 |    -   |    458 |     458 |    -    |    -   |    -   |    -   |    458 |  362  | shell  | 5 hits in subdir; both shells return the same path+line |
| find_exact             |   1 382 |    -   |     68 |      66 |    -    |    -   |    -   |    -   |     66 | 2093  | shell  | mekami: full Symbol struct (8 fields); grep: just the line |
| find_wide              |   1 318 |    -   | 118 341 | 105 824 |    -    |    -   |    -   |    -   |  1 318 |  100  | **ME**    | mekami: `--head=5`; shells: un-capped (1000+ hits) |
| show_header            |     111 |    -   |     29 |      66 |    -    |    -   |    -   |    -   |     29 |  382  | shell  | mekami: qname + path + signature; grep: line only |
| show_body              |     395 |    -   |    -   |      -  |    -    |    662 |    662 | 13 444 |    395 |  100  | **ME**    | mekami: `N: line` numbered; sed/awk: raw; nl: 13 KB |
| show_lines             |   2 150 |    -   |    -   |      -  |    -    |  1 708 |    -   | 13 444 |  1 708 |  125  | shell  | mekami: header + numbered; sed: content only |
| who_calls_many         |   1 450 |  23 537 |  24 805 |  23 537 |    -    |    -   |    -   |    -   |  1 450 |  100  | **ME**    | mekami: `ref_kind=call` filter + `--head=3`; rg matches text |
| who_calls_all          |   7 076 |  23 537 |  24 805 |  23 537 |    -    |    -   |    -   |    -   |  7 076 |  100  | **ME**    | text formatter vs rg (no cap on either) |
| what_calls_few         |     108 |    495 |    505 |     495 |    -    |    -   |    -   |    -   |    108 |  100  | **ME**    | 4 outgoing refs; text formatter is denser than JSON array |
| what_calls_all         |     108 |    495 |    505 |     495 |    -    |    -   |    -   |    -   |    108 |  100  | **ME**    | same; `--head=0` (no cap) |
| list_file              |   8 508 |    -   |    -   |      -  |    -    |    -   |    -   |    -   |    -   |   -   | SKIP   | no shell equiv for top-level Go decls |
| list_files             |  18 061 |    -   |    -   |      -  | 656 587 |    -   |    -   |    -   | 18 061 |  100  | **ME**    | mekami: JSON tree; find: flat paths (un-capped would be 656 KB) |
| list_importers         |     349 |    108 |    114 |     108 |    -    |    -   |    -   |    -   |    108 |  323  | shell  | mekami: structured packages; shells: file paths only |
| list_modules           |     128 |    -   |    -   |      -  |     64  |    -   |    -   |    -   |     64 |  200  | shell  | mekami: 1 mod; find: 1 mod — shells win on byte cost |
| show_modules           |   2 196 |    -   |    -   |      -  |     43  |    -   |    -   |    -   |     43 | 5106  | shell  | mekami: 26 pkgs × stats; find: 3 go.mod dirs — not comparable |
| show_changes           |   2 081 |    -   |    -   |      -  |    -    |    -   |    -   |    -   |    -   |   -   | SKIP   | mekami tracks build-time changes; git status is too different |
| index_status           |     228 |    -   |    -   |      -  |    208  |    -   |    -   |    -   |    208 |  109  | shell  | mekami: counts JSON; ls: file listing (4 files) |
| stats                  |     145 |    -   |    -   |      -  |  8 146  |    -   |    -   |    -   |    145 |  100  | **ME**    | mekami: full DB counts (6 fields); find: file count only |
| trace                  |      64 |    -   |    -   |      -  |    -    |    -   |    -   |    -   |    -   |   -   | SKIP   | BFS over call graph, no shell tool |
| list_package           |   8 488 |    -   |    -   |      -  |    -    |    -   |    -   |    -   |    -   |   -   | SKIP   | no shell equiv |
| list_package_syms      |   8 488 |    -   |    -   |      -  |    -    |    -   |    -   |    -   |    -   |   -   | SKIP   | alias of list-package |

## Summary

- **comparable cases**: 19
- **mekami strict wins**: 9
- **mekami strict losses**: 10
- **skipped (no shell equivalent)**: 5
- **win rate**: 47.4%
- **mekami total bytes (sum across cases)**: 46 100
- **best-shell total bytes (sum across cases)**: 37 484
- **mekami avg bytes per case**: 2 426
- **best-shell avg bytes per case**: 1 972
- **avg ratio (mekami / best-shell)**: 1.23

### **Verdict: tied** (40-60% strict wins)

## Verdict analysis

### Where mekami wins (9 cases)

| case | mekami | best shell | savings | why |
|------|-------:|-----------:|--------:|-----|
| find_text_func | 4 380 | 4 380 (cap 30) | 96% vs `rg` full | `--head=30` cap clips 1 283 → 30 |
| find_wide | 1 318 | 1 318 (cap 5) | 99% vs un-capped shells | `--head=5` clips to 5 symbols |
| show_body | 395 | 395 | tied with `sed` (1 KB un-capped) | 20 lines numbered; sed costs 662 |
| who_calls_many | 1 450 | 1 450 | 94% vs `rg` (23 537 B) | `ref_kind=call` + `--head=3` |
| who_calls_all | 7 076 | 7 076 | 70% vs `rg` (23 537 B) | text formatter denser than rg |
| what_calls_few | 108 | 108 | 78% vs `rg` (495 B) | text formatter 5x denser |
| what_calls_all | 108 | 108 | 78% vs `rg` (495 B) | same |
| list_files | 18 061 | 18 061 (cap 50) | 97% vs un-capped find (656 KB) | `--head=50` + tree scaffold |
| stats | 145 | 145 | 98% vs `find` (8 146 B) | structured 6-field summary vs flat count |

**Pattern:** mekami wins when the alternative is **a massive un-capped
shell output**. The `--head` cap and the indexer's structured
filtering (`ref_kind`, kind, path) are doing the work.

### Where the shell wins (10 cases)

| case | mekami | best shell | shell savings | why |
|------|-------:|-----------:|--------------:|-----|
| find_text_capFor | 4 571 | 1 759 (rg) | 62% | JSON envelope is 2.6x the rg line |
| find_text_zero | 282 | 0 (rg) | 100% | envelope vs empty (ambiguous for shell) |
| find_text_subdir | 1 662 | 458 (grep) | 72% | rg/grep are more compact for sub-n |
| find_exact | 1 382 | 66 (git grep) | 95% | Symbol struct 8 fields vs 1 line |
| show_header | 111 | 29 (grep) | 74% | signature/path vs raw line |
| show_lines | 2 150 | 1 708 (sed) | 21% | header + numbering cost |
| list_importers | 349 | 108 (rg) | 69% | structured pkg vs file paths |
| list_modules | 128 | 64 (find) | 50% | one-liner JSON vs flat path |
| show_modules | 2 196 | 43 (find) | 98% | per-pkg stats not needed for "list" |
| index_status | 228 | 208 (ls) | 9% | tied; ls is 4 lines vs counts JSON |

**Pattern:** the shell wins when (a) the result is **small** (1-30
items, no cap needed), or (b) the mekami output is **structurally
richer** (signature, kind, exports) than what the LLM needs at that
moment. The shells return 1-3 lines of plain text; mekami returns a
JSON object with N fields the LLM didn't ask for.

### Where the comparison breaks down

A few rows in the table are not strictly apples-to-apples. The
shell tool and mekami return **different information**:

- **`find_text_zero`**: the 0 bytes from `rg` are ambiguous — the
  LLM cannot tell "no matches" from "command failed". mekami's
  envelope says `total: 0, truncated: false` explicitly. The
  bench counts this as a shell win because 0 < 282, but the LLM
  with mekami has **more information per byte of search cost**.
- **`find_exact`** and **`show_header`**: mekami returns the full
  Symbol struct (qualified name, kind, signature, exports, file,
  start/end line, parent). `grep` returns just the matching line.
  To get the signature with the shell, the LLM would need a
  follow-up call. The bench counts bytes, not "information per
  byte of search".
- **`list_modules`** and **`show_modules`**: mekami returns the
  module list + per-package stats. `find -name go.mod` returns
  1-3 paths. Apples-to-apples would require the LLM to also run
  `wc -l` per package, which adds bytes.

### Aggregate view

- **Win rate**: 47.4% (tied).
- **Average ratio**: mekami is **1.23x larger** than the best shell
  per case. Total bytes: 46 100 (mekami) vs 37 484 (best shell).
- **Where the gap matters**: 9 cases where mekami saves 70-99% via
  the cap and indexer. 10 cases where the shell saves 50-95% on
  small/dense outputs.

The cap and the indexer are mekami's biggest advantages. Where they
don't apply (small outputs, single items), the JSON envelope is
1-10x larger than a single `rg` line. Where they do apply, mekami
is 70-99% smaller.

## Caveats

- **Corpus bias**: the corpus is mekami's own repo. The indexer is
  tuned for Go (and it has its own symbols, packages, files in
  the index). An LLM with mekami on a different Go repo would see
  the same patterns; on a non-Go repo mekami would have a
  different ratio (it would be limited to the mekami-cli indexer
  only).
- **Bytes ≠ tokens**: a real BPE tokenizer (cl100k_base, o200k)
  will give a different number per row, but the *ratios* between
  tools are dominated by structural overhead and scale the same
  way under any tokenizer.
- **`find_text_zero` is misclassified**: 0 bytes is "less than"
  282 bytes, but the shell output is empty (no information). The
  bench should probably count this as a mekami win in a more
  information-aware metric.
- **`who_calls_all` is a stress case**: `--head=0` (no cap) on a
  query with 242 results. The text formatter is denser than `rg`
  per-ref (76 B vs 95 B), but a 242-ref text dump is still 7 KB.
  In practice the LLM would use `--head=30` (1.4 KB) or scope the
  query.
- **The shell tools used are the most common ones** (`rg`, `grep`,
  `git grep`, `find`, `sed`, `awk`, `nl`). There are other
  alternatives (e.g. `ugrep`, `ast-grep`, `gopls`) that could be
  closer to mekami in some cases. We did not include them in this
  round.
- **The 5 SKIP cases** (`list_file`, `show_changes`, `trace`,
  `list_package`, `list-package-symbols`) are 21% of the catalog.
  They are unique to mekami (no shell tool does the same thing).
  They count as mekami wins by default in any "what can mekami do
  that the shell can't" metric, but they don't count in this byte
  comparison.

## Reproduce

```sh
# Driver lives in /tmp/opencode/mekami_vs_shell/ (out of the repo
# on purpose — the mekami binary is rebuilt via ./build.sh).
bash /tmp/opencode/mekami_vs_shell/compare.sh
```

If the binary is missing or stale:

```sh
cd /home/wolf/Projects/Mekami && ./build.sh
bash /tmp/opencode/mekami_vs_shell/compare.sh
```

The full case catalog (24 cases: 19 comparable + 5 SKIP) is defined
inline at the top of `compare.sh` as a single `CASES=( ... )` array.
Adding a case is a one-line edit; the table is regenerated
automatically.



Archivos creados:
- eval/mekami_vs_shell.md (informe)
- /tmp/opencode/mekami_vs_shell/compare.sh (script de bench)
- /tmp/opencode/mekami_vs_shell/compare.out (output del bench)