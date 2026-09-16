//go:build unix

package cache

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lock takes an exclusive advisory lock on the open file, released when it is closed.
//
// The layout assumes a single writer: two processes appending at once would interleave
// records that each verify and together run backwards, and the Signer's hourly run can
// overlap a manual one. An advisory lock costs one syscall and turns that into a refusal.
func lock(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return fmt.Errorf("cache: another nutz-verify is using %s; wait for it to finish", file.Name())
	}
	if err != nil {
		return fmt.Errorf("cache: locking %s: %w", file.Name(), err)
	}

	return nil
}
