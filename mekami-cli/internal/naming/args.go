package naming

// ArgMap is the wire shape of an MCP tool call: snake_case field
// name → decoded value. Implementations of the MCP layer pass an
// ArgMap (decoded from the JSON-RPC params) to the handler.
//
// Lookup helpers (GetString, GetInt, etc.) return the default when
// the key is absent, matching the SDK's `omitempty` convention.
type ArgMap map[string]any

// GetString returns args[k] as a string, or def if absent.
func (a ArgMap) GetString(k, def string) string {
	if a == nil {
		return def
	}
	v, ok := a[k]
	if !ok || v == nil {
		return def
	}
	s, ok := v.(string)
	if !ok {
		return def
	}
	return s
}

// GetInt returns args[k] as an int, or def if absent or wrong type.
func (a ArgMap) GetInt(k string, def int) int {
	if a == nil {
		return def
	}
	v, ok := a[k]
	if !ok || v == nil {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return def
}

// GetBool returns args[k] as a bool, or def if absent or wrong type.
func (a ArgMap) GetBool(k string, def bool) bool {
	if a == nil {
		return def
	}
	v, ok := a[k]
	if !ok || v == nil {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

// GetStringSlice returns args[k] as []string, or def if absent.
func (a ArgMap) GetStringSlice(k string, def []string) []string {
	if a == nil {
		return def
	}
	v, ok := a[k]
	if !ok || v == nil {
		return def
	}
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, e := range xs {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return def
}
