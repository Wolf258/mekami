//go:build darwin

package watch

import (
	"path/filepath"
	"syscall"
)

// Darwin mount type constants. They live in sys/mount.h and aren't
// exposed by the Go syscall package, so we hardcode the values.
// See https://github.com/apple-oss-dist/xnu/blob/main/bsd/sys/mount.h
const (
	mntNFS  = 0x0000000A
	mntSMBFS = 0x0000000C
	mntMSDOS = 0x00000006
	mntWebDAV = 0x0000000E
	mntAFP   = 0x0000000F
)

func isLikelyNetworkFSImpl(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(abs, &stat); err != nil {
		return false
	}
	switch uint32(stat.Type) {
	case mntNFS, mntSMBFS, mntWebDAV, mntAFP:
		return true
	}
	// Generic "fuse" / network heuristics: FUSE filesystems
	// report the underlying fstype in f_fstypename. We sniff
	// for known network names.
	name := byteSliceToString(stat.Fstypename[:])
	switch name {
	case "fuse", "osxfuse", "osxfusefs", "nfs", "smbfs", "webdav", "afpfs":
		return true
	}
	return false
}

func byteSliceToString(b []int8) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		out = append(out, byte(c))
	}
	return string(out)
}
