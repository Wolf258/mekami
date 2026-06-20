# Edge Cases: cuando Mekami no ayudó (o no fue la mejor opción)

Casos identificados en el benchmark `eval/` (modelo `opencode-go/minimax-m3`,
7 tareas, 2 variantes, 14 corridas). El reporte completo está en
`eval/results/report.md`.

## Criterio de clasificación

Un "edge case" es cualquier corrida donde:

1. **Mekami técnicamente acertó pero el grader falló** — bug del benchmark
2. **El LLM ignoró las tools de Mekami y eligió el camino tradicional**
3. **Mekami no fue la primera opción** aunque la opción del LLM también
   funcionó
4. **Baseline no resolvió con la información disponible** — Mekami habría
   cerrado la brecha

## Resumen visual

| # | Tarea × Variante | Outcome | Tool calls | Diagnóstico |
|---|------------------|---------|:----------:|-------------|
| E1 | `02_trace_calls-mekami` | ❌ (grader) | 1 | Snippet esperado mal elegido |
| E2 | `03_list_package-mekami` | ❌ | 3 | LLM ignoró `list_package` |
| E3 | `06_show_changes-mekami` | ❌ (grader) | 1 | Formato de salida no coincide |
| E4 | `04_show_body-mekami` | ✅ (sub-óptimo) | 2 | LLM prefirió `grep+read` |
| E5 | `01_who_calls-baseline` | ❌ | 1 | `grep` solo no alcanza |
| E6 | `03_list_package-baseline` | ❌ | 4 | `bash+grep` no cubre todo |
| E7 | `05_find_symbol-baseline` | ❌ | 5 | 5 calls, no llegó al dato |

---

## E1. `02_trace_calls-mekami` — Snippet esperado incorrecto

**Outcome**: ❌ (false negative del grader)  
**Tool calls**: 1 (`mekami_trace_calls`)  
**Tools usadas**: `trace_calls`

### Output del LLM

```
La cadena es directa, un solo paso:

  mekami.openStore  (mekami-cli/cmd/mekami/dbpath.go:39)
    └─> store.Open  (mekami-cli/cmd/mekami/dbpath.go:44)

mekami.openStore llama a store.Open directamente en la línea 44, sin funciones intermedias.
```

### Por qué "falló"

El grader marcó `False` porque el `final_text` no contiene la cadena literal
`store.go` (buscaba el archivo de definición). Pero `trace_calls` devuelve el
**call site** (`dbpath.go:44` donde `openStore` invoca a `store.Open`), no la
**definición** (`store/store.go:48` donde está `func Open`).

El LLM **acertó semánticamente** — la cadena de llamadas es correcta. El bug
está en el snippet que el YAML declaraba como esperado.

### Lección

`trace_calls` reporta líneas de **call sites** (refs), no de definiciones. Si
una tarea quiere la definición del target, hay que combinar con
`lookup_symbol` después.

### Fix del benchmark

Cambiar `02_trace_calls.yaml`:

```yaml
expected_answer_contains:
  - "openStore"
  - "store.Open"
  - "dbpath.go"  # call site, no definición
  # - "store.go"  # ← incorrecto: definición, no la devuelve la tool
```

---

## E2. `03_list_package-mekami` — LLM ignoró la tool

**Outcome**: ❌  
**Tool calls**: 3 (`bash, bash, grep`)  
**Tools usadas**: ninguna de Mekami

### Output del LLM

```
Métodos del package github.com/mekami/mekami-cli/internal/mcp
(todos en server.go, receptor *Server):

Exportados:
  - Close() error (server.go:36)
  - Run(ctx context.Context) error (server.go:38)

No exportados:
  - registerTools() (server.go:112)
  - search(ctx, req, args) (...)
  - getSymbol(...)
  ...
```

### Por qué "falló"

1. **El grader** buscaba `mcp.Server.Run` (qualified name), pero el LLM
   escribió `Run(ctx context.Context) error` (forma corta, deducida del
   código). Snippet mismatch.
2. **El LLM** sí tenía disponible `list_package`, pero prefirió `bash+grep`
   para "explorar" el directorio antes. Probablemente porque:
   - El prompt decía "package `github.com/mekami/mekami-cli/internal/mcp`" y
     el LLM no estaba seguro de que ese fuera el `package_id` exacto
   - La descripción de `list_package` dice "ordered by file then line" —
     no destaca que es el camino rápido

### Lección

`list_package` requiere que el LLM sepa el `package_id` exacto. Si el
usuario da el path (`internal/mcp`) en vez del ID canónico
(`github.com/mekami/mekami-cli/internal/mcp`), el LLM duda y va por
`bash`+`ls`.

### Fix del benchmark

Dos opciones:

a) **Prompt directivo**: añadir "usá la tool `list_package` con el
   package_id `github.com/mekami/mekami-cli/internal/mcp`"
b) **Aceptar ambos formatos** en el snippet matching: `mcp.Server.Run`
   OR `Server.Run` OR `Run(ctx`

### Mejora sugerida a Mekami

`list_package` podría aceptar un path o nombre corto como input alternativo
al `package_id` canónico.

---

## E3. `06_show_changes-mekami` — Formato de salida no estructurado

**Outcome**: ❌ (false negative del grader)  
**Tool calls**: 1 (`mekami_show_changes`)  
**Tools usadas**: `show_changes`

### Output del LLM

```
Sí, el índice está desactualizado. Cambios desde el último mekami build:

Añadidos (30):
  - mekami-cli/internal/config/config_test.go
  - mekami-cli/internal/watch/testhelpers_test.go, watch_test.go
  - mekami-cli/tests/cmd/mekami/mcptest_test.go
  - mekami-cli/tests/internal/graph/grep/grep_test.go
  - mekami-cli/tests/internal/graph/ingest/ (16 archivos: ...)
  ...
```

### Por qué "falló"

El YAML declaraba `expected_answer_format: "json"` y
`expected_answer_keys: ["added", "modified", "removed", "unreadable"]`. El
grader (versión arreglada) acepta menciones textuales, pero esta corrida se
evaluó con la versión original que pedía JSON literal.

El LLM devolvió el output en formato **Markdown narrativo**, no JSON
estructurado, aunque `show_changes` por debajo sí devuelve JSON.

### Lección

Cuando opencode recibe output JSON de un tool, a menudo lo reformatea a
markdown para la respuesta final. Si el grader busca JSON literal, va a
fallar aunque el modelo sí resolvió la tarea.

### Fix del benchmark

- Cambiar `expected_answer_keys` para aceptar tanto la key JSON como su
  traducción natural (`added` ↔ "Añadidos" / "añadido")
- O pedirle al LLM explícitamente que devuelva JSON puro

---

## E4. `04_show_body-mekami` — LLM prefirió `grep+read`

**Outcome**: ✅ (acertó), pero **no usó Mekami**  
**Tool calls**: 2 (`grep, read`)  
**Tools usadas**: ninguna de Mekami (aunque `show_body` estaba disponible)

### Output del LLM

```
mekami-cli/internal/mcp/server.go:112-208

112: func (s *Server) registerTools() {
113:   // Each tool's Description follows a fixed template so the LLM can
114:   //   parse them uniformly:
...
119:   mcp.AddTool(s.server, &mcp.Tool{
120:     Name:        "find_symbol",
121:     ...
```

### Por qué el LLM eligió `grep+read`

`show_body` y `grep`+`read` ambos resuelven la tarea en 2 calls. El LLM
probablemente:

1. Está entrenado a usar `grep` + `read` como patrón universal
2. La descripción de `show_body` en el system prompt de opencode dice
   "Show the source of a symbol by qualified name" — suena equivalente a
   `read` después de un `grep`
3. No hay una indicación de que `show_body` evite el paso de buscar primero

### Lección

Para este modelo, `show_body` no es un atajo claro cuando el LLM ya tiene
un patrón mental "grep → read" consolidado. Mekami necesita:

a) **Mejor descripción en system prompt** que destaque el ahorro de 1 call
b) **El LLM tiene que estar entrenado** o tener system prompt que le diga
   "preferí las tools MCP cuando existan"

### Fix del benchmark

Marcar como **"soft fail"** (acertó pero con tools no-óptimas) y medir
`mekami_uptake = False` aparte de `correct = True`.

---

## E5. `01_who_calls-baseline` — `grep` solo no alcanza

**Outcome**: ❌  
**Tool calls**: 1 (`grep`)  
**Tools usadas**: `grep`

### Output del LLM

```
Call sites (uso del método):
  - mekami-cli/internal/mcp/server.go:162 — pasado como handler a
    mcp.AddTool(...) dentro de registerTools()
```

### Por qué "falló"

El grader pedía `mcp.Server.registerTools` y `server.go:163`. El LLM dio
`server.go:162` (off-by-one). Esto es porque el `grep` cuenta líneas
0-indexed o el LLM alinea mal el contexto de la cita.

### Lección

`grep` + razonamiento sobre el contexto es propenso a errores off-by-one
en números de línea. Mekami devuelve file:line exactos del índice, sin
ambigüedad.

### Métrica

Si Mekami devuelve `server.go:163` y la realidad es `server.go:163` (la
tool lo verifica contra el AST), la confianza es 100%. Aquí el LLM
arriesgó una línea y falló por 1.

---

## E6. `03_list_package-baseline` — `bash+grep` insuficiente

**Outcome**: ❌  
**Tool calls**: 4 (`glob, bash, bash, read`)  
**Tools usadas**: `glob, bash, read`

### Output del LLM

```
Métodos del package mcp (todos no exportados, receiver *Server,
archivo mekami-cli/internal/mcp/server.go):

  - Close() error — server.go:36
  - Run(ctx context.Context) error — server.go:38
  - registerTools() — server.go:112
  - search(ctx, _, args searchArgs) — server.go:210
  - getSymbol(ctx, _, args symbolArgs) — server.go:218
  ...
```

### Por qué "falló"

Mismo problema que E2 pero en baseline: el grader buscaba `mcp.Server.Run`
(qualified) y el LLM escribió `Run(...)`. Mismatch semántico, no falta de
datos.

Pero conceptualmente: con 4 calls (`glob`+`bash`+`bash`+`read`) el LLM
**logró** enumerar 13+ métodos. Si Mekami hubiera estado disponible, 1
call a `list_package` habría dado el mismo resultado. Speedup real: 4x.

### Lección

Para tareas de "listar todo X", `list_package` siempre gana en calls. El
problema es que el LLM no fue lo suficientemente valiente para confiar en
la tool.

---

## E7. `05_find_symbol-baseline` — 5 calls y casi

**Outcome**: ❌  
**Tool calls**: 5 (`grep×3, read×2`)  
**Tools usadas**: `grep, read`

### Output del LLM

```
- Qualified name: mcp.Server
- Archivo: mekami-cli/internal/mcp/server.go
- Líneas: 17–20
- Signature:
  type Server struct {
      server *mcp.Server
      store  *store.Store
  }
```

### Por qué "falló"

El grader pedía `server.go:17` exacto. El LLM escribió `17–20` (rango). El
match es parcial — `17–20` no contiene `17` como substring seguido de
caracteres no numéricos (porque va seguido de `–20`).

### Lección

El LLM tiene la información, pero el formato de salida no es lo
suficientemente preciso. Con Mekami (`lookup_symbol`) la respuesta es
exacta porque viene del índice.

### Fix del benchmark

Cambiar el snippet a algo más flexible, o agregar un pre-procesamiento
del grader que extraiga el primer número de línea del rango.

---

## Patrones transversales

### 1. El grader es más estricto que el modelo

3 de 7 fallos son **false negatives** del grader (E1, E3, E6). El LLM
resolvió pero el snippet matching falló. Esto sugiere que el benchmark
subestima la calidad real del modelo.

### 2. Mekami tiene ventaja cuando hay ambigüedad de path

- E5, E6, E7: el LLM con `grep` puede equivocarse de línea (off-by-one) o
  de package. Mekami lo evita porque el índice es canónico.
- E1: la definición vs call site confundió al grader, no al LLM.

### 3. El LLM no siempre descubre las tools MCP

- 5/7 corridas usaron Mekami. Las 2 que no (E2, E4) revelan:
  - **E2**: input no canónico (`internal/mcp` vs
    `github.com/mekami/mekami-cli/internal/mcp`) hizo dudar al LLM
  - **E4**: descripción de `show_body` no destacó que es atajo vs
    `grep+read`

### 4. `show_changes` y `find_text` se usan pero el output confunde

- `06_show_changes` y `07_find_text` ambos hicieron 1 call, pero el
  formato de respuesta no coincidió con lo que el grader esperaba.

## Recomendaciones

| Para | Acción |
|------|--------|
| Benchmark | Relajar snippets a palabras clave o regex, no literales |
| Benchmark | Agregar `mekami_uptake` (booleano: ¿usó ≥1 tool MCP?) |
| Benchmark | Trackear `first_tool_was_mekami` (¿la primera call fue MCP?) |
| Mekami | `list_package` debería aceptar nombres cortos / paths |
| Mekami | Descripciones de tools deberían destacar el ahorro vs `grep` |
| Prompts | Versión "directiva" que diga "preferí tools MCP si existen" |
