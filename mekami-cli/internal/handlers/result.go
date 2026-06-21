package handlers

// Result es el sobre que devuelve cada handler de lectura. Tiene
// dos vistas: text (default CLI y MCP) y data (--json CLI y
// MCP con wire_format=json). Los handlers nunca bifurcan por
// flag: siempre computan ambas vistas.
//
// La motivación es eliminar la inconsistencia CLI/MCP: antes
// cada handler decidía por su cuenta si devolver un formatter
// compacto (string) o un envelope JSON (struct) según si el
// cap de --head recortaba el resultado. El usuario no tenía
// control y el mismo comando podía dar formatos distintos.
//
// Con Result el handler:
//   - siempre computa text (un string listo para imprimir);
//   - siempre expone data (el struct serializable para --json);
// El wrapper de salida (runGraphRead en el CLI, makeHandler
// en el MCP) elige cuál vista usar según el flag --json del
// CLI o la config wire_format del MCP.
//
// Si el handler solo tiene una vista "text" (caso típico de
// mensajes de error o ausencia de resultados), Data puede ser
// nil; ExtractData lo deja pasar y el wrapper lo maneja.
type Result struct {
	Text string
	Data any
}

// AsResult envuelve un par (text, data) en un Result. Helper
// de construcción para los handlers. data puede ser nil
// cuando el handler solo quiere reportar texto.
func AsResult(text string, data any) Result {
	return Result{Text: text, Data: data}
}

// ExtractData desenvuelve un any a su data pura, sea un
// Result o un valor legacy (string, struct, nil). Usado por
// el wrapper CLI/MCP para serializar a JSON cuando el caller
// pide el formato estructurado.
//
// Si v es un Result, devuelve v.Data. Si v es cualquier
// otro tipo (string, struct, puntero), lo devuelve tal cual
// para mantener compatibilidad con handlers que aún no
// migraron a Result.
func ExtractData(v any) any {
	if r, ok := v.(Result); ok {
		return r.Data
	}
	return v
}

// TextView devuelve la vista de texto, sea un Result o un
// string legacy. Si v no es ni Result ni string, devuelve
// "" para forzar al wrapper a caer al fallback JSON.
func TextView(v any) string {
	if r, ok := v.(Result); ok {
		return r.Text
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
