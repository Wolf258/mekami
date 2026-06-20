# Audit: edge_cases.md vs código actual de Mekami
**Fecha**: 2026-06-20
**Alcance**: auditar los 7 edge cases del doc `eval/edge_cases.md`
contra el código actual del repo (`Mekami`), sin re-ejecutar el
benchmark con el LLM real.
**Método**: reproducir cada caso en `tmp/proj-test/` (mini repo Go
sintético, 8 archivos, 2 packages `internal/mcp` y `cmd/mcp` para
forzar ambigüedad), correr la tool correspondiente, capturar el output,
y compararlo con el "fix" propuesto en el doc original.
## Tabla resumen
| #   | Caso                       | Tipo             | Fix Mekami actual                                | Veredicto |
|-----|----------------------------|------------------|--------------------------------------------------|-----------|
| E1  | `trace_calls` call-site    | Grader FN        | specs.go:188-191 + mensaje error                 | RESUELTO  |
| E2  | `list_package` path corto  | UX / doc         | handlers/read.go:269-300 + specs.go:223-226      | PARCIAL (1) |
| E3  | `show_changes` formato     | Grader FN        | model.FileDiff JSON con `added/modified/removed` | RESUELTO Mekami-side (2) |
| E4  | `show_body` vs `grep+read` | UX / descripción | specs.go:113-115 ("PREFERRED over `grep`+`read`") | RESUELTO Mekami-side (3) |
| E5  | `who_calls` off-by-one     | Inherente diseño | AST indexado, no regex                           | RESUELTO  |
| E6  | `list_package` speedup     | Funcional        | 1 call vs 4 de shell pipeline                    | RESUELTO  |
| E7  | `find_symbol` línea exacta | Inherente diseño | JSON con `StartLine: <int>`                      | RESUELTO  |
(1) El resolver acepta `internal/mcp` y el canonical, pero NO el bare
    name `mcp` aunque la docstring lo sugiera. Caso del edge_cases.md
    (input `internal/mcp`) sí funciona. Queda gap menor.
(2) La tool devuelve JSON con las 4 keys esperadas. El "fallo" en el
    eval fue que el LLM reformateó el JSON a markdown narrativo.
    Fix real: flexibilizar el grader, no Mekami.
(3) La descripción de la tool ya destaca el ahorro vs `grep+read`.
    Que el LLM no la prefiera es un comportamiento del modelo,
    solo auditable re-corriendo el benchmark.
## Detalle por caso
Ver archivos:
- `audit/E1_trace_calls.json`        — output completo de `mekami trace`
- `audit/E2a-c_listpkg_*.txt`        — list_package con 3 inputs
- `audit/E2d_listpkg_ambiguous.txt`  — input bare `mcp` con 2 packages
- `audit/E2_notes.md`                — notas del gap de E2
- `audit/E3a-e_showchanges_*.txt`    — 5 estados del filesystem
- `audit/E4a_show_body.txt`          — show_body con header canónico
- `audit/E4_notes.md`                — speedup show_body vs grep+read
- `audit/E5a_who_calls.txt`          — línea exacta confirmada con grep
- `audit/E5_notes.md`
- `audit/E6a_listpkg.txt`            — list_package completo
- `audit/E6_notes.md`                — comparación 1 call vs 4 calls
- `audit/E7a_find_symbol_server.txt` — find_symbol con StartLine exacto
- `audit/E7_notes.md`
## Conclusiones
### Lo que YA está resuelto (5/7)
- **E1, E5, E7** son issues del lado **grader/benchmark**, no de
  Mekami. El código actual de las tools maneja correctamente:
  call sites vs definiciones, líneas exactas del AST, y números
  discretos en JSON.
- **E6** es una confirmación funcional: la tool hace exactamente lo
  que el doc predice (1 call vs 4).
- **E3** el fix Mekami-side está; la tool devuelve JSON estructurado
  con las 4 keys (`added`/`modified`/`removed`/`inaccessible`).
### Lo que está PARCIAL (1/7)
- **E2** `list_package` con path corto: el caso del doc (input
  `internal/mcp`) funciona. El gap es que el resolver no busca por
  **último segmento** del package_id, así que input bare `mcp` con
  dos packages `internal/mcp` y `cmd/mcp` no dispara la ambigüedad
  — devuelve "no symbols" en vez de listar candidatos. Si se quiere
  cerrar esto al 100%, hay que ampliar `resolvePackageID` en
  `handlers/read.go:274-300` para probar también
  `package_id endsWith /<input>` contra el set de packages indexados.
### Lo que depende del modelo (1/7)
- **E4** la descripción de `show_body` ya está bien y la tool
  cumple. El "soft fail" del eval (acertó pero con `grep+read`) es
  preferencia del modelo, no de la tool. Medible solo re-corriendo
  el benchmark.
## Recomendaciones
1. **Cerrar el gap de E2**: ampliar `resolvePackageID` para que
   también pruebe coincidencia por último segmento
   (`package_id endsWith /<input>`). 10-15 líneas en
   `handlers/read.go`. No urgente pero queda inconsistente con la
   docstring.
2. **No tocar Mekami para E1, E3, E5, E6, E7** — los fixes son del
   lado benchmark/grader.
3. **Re-correr el benchmark** para confirmar que las descripciones
   de tools actualizadas (E1, E2, E4) producen mejor `mekami_uptake`
   en el modelo actual. Eso requiere un harness externo, no
   accionable desde este repo.
4. **Limpiar `tmp/proj-test/`** cuando ya no se necesite: el árbol
   tiene 9 archivos Go + `.mekami/` con DB. Está en
   `.gitignore` (verificar) o ignorar manualmente.