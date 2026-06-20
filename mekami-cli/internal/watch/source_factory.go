package watch

import (
	"strings"
	"time"
)

// FallbackMode is the resolved form of config.watch.fallback.
type FallbackMode int

const (
	// FallbackAuto picks fsnotify by default; if the FS is detected
	// as unreliable (NFS, SMB, FUSE), it switches to the poller.
	FallbackAuto FallbackMode = iota
	// FallbackFsnotify forces fsnotify even on unreliable FSes.
	// Use this when the user explicitly wants the low-overhead
	// path and accepts that some events may be missed.
	FallbackFsnotify
	// FallbackPoll forces the poller. Use this when fsnotify
	// is known to be broken on the target FS.
	FallbackPoll
)

// ParseFallbackMode maps the user-facing string to a FallbackMode.
// Unknown values fall back to FallbackAuto with a flag so the
// caller can log a warning.
func ParseFallbackMode(s string) (FallbackMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return FallbackAuto, true
	case "fsnotify", "inotify":
		return FallbackFsnotify, true
	case "poll", "poller":
		return FallbackPoll, true
	}
	return FallbackAuto, false
}

// NewSource picks the Source to use for this invocation. The
// returned string is a short label suitable for logs and
// `index_status` (e.g. "fsnotify", "poller", "auto:fsnotify").
//
// Behaviour:
//   - FallbackFsnotify: always FsnotifySource.
//   - FallbackPoll: always PollerSource with the configured interval.
//   - FallbackAuto: FsnotifySource by default; PollerSource if the
//     FS is detected as unreliable.
func NewSource(root string, mode FallbackMode, pollInterval time.Duration, log Logger) Source {
	switch mode {
	case FallbackFsnotify:
		return NewFsnotifySource(root, log)
	case FallbackPoll:
		return NewPollerSource(root, pollInterval, log)
	default:
		if isLikelyNetworkFS(root) {
			if log != nil {
				log.Info("auto: detected unreliable FS, using poller")
			}
			return NewPollerSource(root, pollInterval, log)
		}
		return NewFsnotifySource(root, log)
	}
}
