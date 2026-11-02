//go:build unix

package orchestrator

import "syscall"

// volumeFree is the bytes free, to an unprivileged writer, on the volume holding dir.
func volumeFree(dir string) (uint64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return uint64(st.Bavail) * uint64(st.Bsize), true //nolint:unconvert,gosec // the fields' types differ by platform
}
