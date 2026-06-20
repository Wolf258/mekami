//go:build linux

package watch

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// isLikelyNetworkFSImpl parses /proc/mounts to determine whether
// path lives on a network or otherwise-unreliable filesystem. It
// returns true for fstypes known to misbehave with inotify (NFS,
// SMB/CIFS, 9p, FUSE) and false for local filesystems.
//
// The implementation picks the longest mountpoint prefix that
// matches path. A path is "on" a mount if it equals the mountpoint
// or has it as a directory prefix. We normalise the path with
// filepath.Clean before comparison.
func isLikelyNetworkFSImpl(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)

	f, err := os.Open("/proc/mounts")
	if err != nil {
		return false
	}
	defer f.Close()

	// Network or unreliable fstypes. inotify on FUSE is hit-or-miss
	// depending on the driver, so we err on the side of polling.
	networkTypes := map[string]bool{
		"nfs":         true,
		"nfs4":        true,
		"cifs":        true,
		"smbfs":       true,
		"smb3":        true,
		"9p":          true,
		"fuse":        true,
		"fuseblk":     true,
		"fuse.sshfs":  true,
		"overlayfs":   true,
		"virtiofs":    true,
	}

	var (
		bestMount string
		bestType  string
	)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		mount, fstype := filepath.Clean(fields[1]), fields[2]
		// Match if abs is under mount.
		if abs == mount || strings.HasPrefix(abs, mount+"/") {
			if len(mount) > len(bestMount) {
				bestMount = mount
				bestType = fstype
			}
		}
	}
	if bestType == "" {
		return false
	}
	// Trim "fuse." prefix (e.g. "fuse.sshfs" → "sshfs").
	if i := strings.Index(bestType, "."); i >= 0 {
		bestType = bestType[:i]
	}
	return networkTypes[strings.ToLower(bestType)]
}
