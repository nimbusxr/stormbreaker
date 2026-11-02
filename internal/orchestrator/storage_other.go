//go:build !unix

package orchestrator

// volumeFree is unknown here: the orchestrator keeps no reserve.
func volumeFree(string) (uint64, bool) { return 0, false }
