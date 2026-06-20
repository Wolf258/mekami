package walk

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

// Fingerprint reads path and returns its sha256 hex hash,
// modification time (unix seconds) and size. Used by both the
// build pipeline (to skip unchanged files) and DiffSinceLastBuild
// (to detect changes). The hash is the source of truth for
// content equality; mtime is recorded but does not gate
// re-ingestion.
func Fingerprint(path string) (hash string, mtime int64, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), stat.ModTime().Unix(), stat.Size(), nil
}
