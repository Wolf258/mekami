//go:build !linux && !darwin

package watch

// isLikelyNetworkFSImpl on unsupported platforms always returns
// false: we have no portable way to detect the FS type, so the
// `auto` policy falls back to fsnotify. The user can force polling
// by setting watch.fallback = "poll" in config.
func isLikelyNetworkFSImpl(path string) bool { return false }
