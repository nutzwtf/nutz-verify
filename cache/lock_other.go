//go:build !unix

package cache

import "os"

// lock is a no-op where flock is not available. The Verifier ships for linux and darwin
// (spec §3), so this exists to keep the package building elsewhere, not to protect anyone.
func lock(*os.File) error { return nil }
