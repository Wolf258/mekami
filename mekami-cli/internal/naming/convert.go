package naming

import "strings"

// Kebab converts a snake_case identifier to kebab-case. Examples:
//
//	find_symbol   -> find-symbol
//	max_depth     -> max-depth
//	path_prefix   -> path-prefix
//	get_symbol    -> get-symbol
//
// Identifiers that are already kebab-case (no underscores) are
// returned unchanged.
func Kebab(snake string) string {
	if !strings.ContainsRune(snake, '_') {
		return snake
	}
	return strings.ReplaceAll(snake, "_", "-")
}
