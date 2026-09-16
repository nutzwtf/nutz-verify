package cache_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nutzwtf/nutz-verify/cache"
	"github.com/nutzwtf/nutz-verify/chain"
)

// fakeChain is a Source with one block a second and a few transfers, which can be reorged
// from a block onward: every block from there gets a new hash, and its transfers change.
type fakeChain struct {
	mu        sync.Mutex
	tip       uint64
	forkAt    uint64 // blocks >= forkAt carry the salted hash; 0 for no fork
	transfers map[uint64][]chain.Transfer
	failAt    uint64 // Transfers over a range containing this block fails; 0 for never

	requests int
}

func newFakeChain(tip uint64) *fakeChain {
	return &fakeChain{tip: tip, transfers: map[uint64][]chain.Transfer{}}
}

func (f *fakeChain) hash(n uint64) chain.Hash {
	h := hashOf(n)
	if f.forkAt != 0 && n >= f.forkAt {
		h[30] = 0xf0
	}

	return h
}

func (f *fakeChain) add(block uint64, from, to chain.Address, value int64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.transfers[block] = append(f.transfers[block], chain.Transfer{
		Site:  chain.Site{BlockNumber: block, BlockHash: f.hash(block), LogIndex: uint64(len(f.transfers[block])), Timestamp: int64(block)},
		From:  from,
		To:    to,
		Value: big.NewInt(value),
	})
}

// reorg rewrites history from block on: new hashes, and the transfers there replaced.
func (f *fakeChain) reorg(from uint64) {
	f.mu.Lock()
	f.forkAt = from
	for b := range f.transfers {
		if b >= from {
			delete(f.transfers, b)
		}
	}
	f.mu.Unlock()
}

func (f *fakeChain) Transfers(_ context.Context, from, to uint64) ([]chain.Transfer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests++
	if f.failAt != 0 && from <= f.failAt && f.failAt <= to {
		return nil, errors.New("the endpoint went away")
	}

	var out []chain.Transfer
	for b := from; b <= to; b++ {
		for _, t := range f.transfers[b] {
			t.BlockHash = f.hash(b)
			out = append(out, t)
		}
	}

	return out, nil
}

func (f *fakeChain) BlockByNumber(_ context.Context, n uint64) (chain.Block, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests++
	if n > f.tip {
		return chain.Block{}, fmt.Errorf("block %d is past the tip", n)
	}

	return chain.Block{Number: n, Hash: f.hash(n), ParentHash: f.hash(n - 1), Timestamp: int64(n)}, nil
}

// head is the tip as a caller would have resolved it with Head. Not counted as a request:
// the tests count what Sync itself asks for.
func (f *fakeChain) head() chain.Block {
	b, _ := f.BlockByNumber(context.Background(), f.tip)
	f.mu.Lock()
	f.requests--
	f.mu.Unlock()

	return b
}

func expected(t *testing.T, f *fakeChain, from, to uint64) []cache.Record {
	t.Helper()

	transfers, err := f.Transfers(context.Background(), from, to)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]cache.Record, 0, len(transfers))
	for _, tr := range transfers {
		out = append(out, cache.FromTransfer(tr))
	}

	return out
}

// busy is a chain with two transfers in most blocks from start onward, and a gap.
func busy(tip uint64) *fakeChain {
	f := newFakeChain(tip)
	for b := uint64(start); b <= tip; b++ {
		if b%7 == 3 {
			continue // a block with no transfers, so records and blocks are not one to one
		}

		f.add(b, alice, bob, int64(b))
		f.add(b, bob, alice, 1)
	}

	return f
}

func syncTo(t *testing.T, c *cache.Cache, f *fakeChain, opts cache.SyncOptions) cache.Synced {
	t.Helper()

	synced, err := c.Sync(t.Context(), f, f.head(), opts)
	if err != nil {
		t.Fatalf("Sync = %v", err)
	}

	return synced
}

func TestSync_FromScratchAndThenIncrementally(t *testing.T) {
	t.Parallel()

	f := busy(1050)
	c := open(t, t.TempDir(), identity())

	var reached []uint64
	synced := syncTo(t, c, f, cache.SyncOptions{Chunk: 20, Progress: func(block uint64) { reached = append(reached, block) }})

	if synced.Appended != c.Len() || synced.Reorg != nil {
		t.Errorf("Synced = %+v over an empty cache with %d records", synced, c.Len())
	}
	sameRecords(t, all(t, c), expected(t, f, start, 1050))

	// Progress reports the block the Cache reaches after each chunk, ending at the tip.
	if len(reached) == 0 || reached[len(reached)-1] != 1050 {
		t.Errorf("Progress reported %v, want a series ending at the tip", reached)
	}

	// The chain moves on. Only the new blocks are read: the fake counts requests, and a
	// re-read of the whole history would be many chunks rather than one.
	for b := uint64(1051); b <= 1060; b++ {
		f.add(b, alice, bob, 9)
	}
	f.tip = 1060

	before := f.requests
	synced = syncTo(t, c, f, cache.SyncOptions{Chunk: 20})

	if synced.Appended != 10 || synced.Reorg != nil {
		t.Errorf("Synced = %+v, want 10 appended and no reorg", synced)
	}
	if f.requests-before > 3 {
		t.Errorf("an incremental sync of 10 blocks made %d requests", f.requests-before)
	}
	sameRecords(t, all(t, c), expected(t, f, start, 1060))
}

func TestSync_NothingToDoIsNothing(t *testing.T) {
	t.Parallel()

	f := busy(1020)
	c := open(t, t.TempDir(), identity())
	syncTo(t, c, f, cache.SyncOptions{})

	// Already at the tip: one header to confirm the last record still stands, nothing else.
	before := f.requests
	synced := syncTo(t, c, f, cache.SyncOptions{})
	if synced.Appended != 0 || synced.Reorg != nil || f.requests-before != 1 {
		t.Errorf("Synced = %+v with %d requests; want nothing appended and one header read",
			synced, f.requests-before)
	}

	// A tip behind the cache — the same history read at a stricter finality — is not a
	// reorg and not an error; the records past it simply are not vouched for yet.
	f.tip = 1010
	synced = syncTo(t, c, f, cache.SyncOptions{})
	if synced.Appended != 0 || synced.Reorg != nil || c.Len() != int64(len(expected(t, f, start, 1020))) {
		t.Errorf("Synced = %+v against an earlier tip; want the cache untouched", synced)
	}
}

func TestSync_AReorgTruncatesAtTheForkAndMatchesAFreshSync(t *testing.T) {
	t.Parallel()

	f := busy(1100)

	dir := t.TempDir()
	c := open(t, dir, identity())
	syncTo(t, c, f, cache.SyncOptions{Chunk: 30})

	// History rewrites from block 1042: those blocks get new hashes and different
	// transfers, and the chain advances a little on the new branch.
	f.reorg(1042)
	for b := uint64(1042); b <= 1110; b++ {
		f.add(b, bob, alice, int64(b)*3)
	}
	f.tip = 1110

	wasHeld := c.Len()
	synced := syncTo(t, c, f, cache.SyncOptions{Chunk: 30})

	if synced.Reorg == nil {
		t.Fatalf("Synced = %+v, want a reorg", synced)
	}
	// The first dropped block is the fork block, and what was dropped is exactly every
	// record from there on — two per block over the 58 blocks 1042..1099 minus the gaps.
	dropped := int64(len(expected(t, busy(1100), 1042, 1100)))
	if synced.Reorg.Block != 1042 || synced.Reorg.Dropped != dropped {
		t.Errorf("Reorg = %+v, want block 1042 and %d dropped", synced.Reorg, dropped)
	}
	if synced.Appended != c.Len()-(wasHeld-dropped) {
		t.Errorf("Appended = %d, want the records of the new branch", synced.Appended)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// Byte-identical to a --fresh sync over the same range: the truncation landed on the
	// right offset and nothing of the old branch leaked into the file.
	freshDir := t.TempDir()
	fresh := open(t, freshDir, identity())
	syncTo(t, fresh, f, cache.SyncOptions{Chunk: 30})
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(readFile(t, filepath.Join(dir, cache.FileName)), readFile(t, filepath.Join(freshDir, cache.FileName))) {
		t.Error("the repaired cache differs from a fresh sync of the same history")
	}
	sameRecords(t, all(t, open(t, dir, identity())), expected(t, f, start, 1110))
}

func TestSync_AReorgBehindTheTipIsStillFound(t *testing.T) {
	t.Parallel()

	// The fork is well below the tip and the records past it are the only evidence. The
	// search has to find the first record whose block hash is no longer the chain's, not
	// just compare the newest one.
	f := busy(1100)
	c := open(t, t.TempDir(), identity())
	syncTo(t, c, f, cache.SyncOptions{})

	f.reorg(1005)
	for b := uint64(1005); b <= 1100; b++ {
		f.add(b, alice, bob, 4)
	}

	synced := syncTo(t, c, f, cache.SyncOptions{})
	if synced.Reorg == nil || synced.Reorg.Block != 1005 {
		t.Fatalf("Synced = %+v, want a reorg at block 1005", synced)
	}
	sameRecords(t, all(t, c), expected(t, f, start, 1100))
}

func TestSync_AFailedChunkKeepsWhatWasAppended(t *testing.T) {
	t.Parallel()

	// A sync that dies partway is the normal case against a throttling endpoint. The chunks
	// before the failure are on disk and the next sync resumes after them, so a
	// from-scratch sync is a one-time cost even when it takes several attempts.
	f := busy(1100)
	f.failAt = 1050

	c := open(t, t.TempDir(), identity())
	if _, err := c.Sync(t.Context(), f, f.head(), cache.SyncOptions{Chunk: 10}); err == nil {
		t.Fatal("Sync = nil error through a failing chunk")
	}

	last, ok := c.Last()
	if !ok || last.BlockNumber < 1030 || last.BlockNumber >= 1050 {
		t.Fatalf("after the failure the cache reaches block %d (%v), want the last whole chunk before 1050", last.BlockNumber, ok)
	}

	f.failAt = 0
	syncTo(t, c, f, cache.SyncOptions{Chunk: 10})
	sameRecords(t, all(t, c), expected(t, f, start, 1100))
}

func TestSync_RefusesABlockWithoutAHash(t *testing.T) {
	t.Parallel()

	// The tip is what the cache is verified against. A zero tip is a caller that never
	// resolved Head, and syncing "to block 0" would silently do nothing forever.
	c := open(t, t.TempDir(), identity())
	if _, err := c.Sync(t.Context(), busy(1010), chain.Block{}, cache.SyncOptions{}); err == nil {
		t.Error("Sync = nil error against an unset tip")
	}
}

func TestTruncate_DropsFromTheBlockOn(t *testing.T) {
	t.Parallel()

	c := open(t, t.TempDir(), identity())
	want := []cache.Record{
		transfer(1000, alice, bob, 1),
		transfer(1002, alice, bob, 2),
		transfer(1002, bob, alice, 3),
		transfer(1005, alice, bob, 4),
	}
	if err := c.Append(want); err != nil {
		t.Fatal(err)
	}

	// A block with no record of its own: everything at or after it goes, which is the
	// record at 1005.
	dropped, err := c.Truncate(1003)
	if err != nil || dropped != 1 {
		t.Fatalf("Truncate(1003) = %d, %v; want 1 dropped", dropped, err)
	}
	sameRecords(t, all(t, c), want[:3])

	if dropped, err := c.Truncate(1002); err != nil || dropped != 2 {
		t.Fatalf("Truncate(1002) = %d, %v; want both records of the block dropped", dropped, err)
	}
	sameRecords(t, all(t, c), want[:1])

	last, ok := c.Last()
	if !ok || last.BlockNumber != 1000 {
		t.Errorf("Last = %+v, %v after truncating", last, ok)
	}

	if dropped, err := c.Truncate(1000); err != nil || dropped != 1 || c.Len() != 0 {
		t.Fatalf("Truncate(1000) = %d, %v with %d left; want an empty cache", dropped, err, c.Len())
	}
	if _, ok := c.Last(); ok {
		t.Error("an emptied cache still has a last record")
	}

	// Appending after a truncation continues from the new tail, not from wherever the
	// write buffer thought it was.
	if err := c.Append(want[:2]); err != nil {
		t.Fatal(err)
	}
	sameRecords(t, all(t, c), want[:2])
}
