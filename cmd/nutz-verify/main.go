// Command nutz-verify recomputes an Epoch from public chain data and reports a Verdict
// against the on-chain Root: MATCH, MISMATCH or INDETERMINATE, exit 0, 1 or 2.
//
// Two audiences, one binary (spec §1): a hostile stranger inside the 30-minute Dispute
// window, and the warm Signer that signs hourly only on exit 0. Everything a Recompute
// reads comes from the --rpc endpoints; nothing published is ever an input (ADR-0002).
//
//	nutz-verify epoch <id>   recompute and compare one Epoch
//	nutz-verify latest       the most recent posted Root
//	nutz-verify sync         advance the Cache only
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Ctrl-C ends the run rather than killing it mid-write: the Cache checkpoints on
	// Close, and an interrupted sync resumes after its last whole chunk.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr, pinned))
}
