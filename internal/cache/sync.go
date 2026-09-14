package cache

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/nutzwtf/nutz-verify/internal/chain"
)

// defaultChunk is how many blocks one Sync read covers. At USDG's density on chain 4663
// (about five Transfer logs per block) that is ~50,000 records, or ~6 MB of file, between
// appends; the Reader pages eth_getLogs underneath it and this only bounds memory.
const defaultChunk = 10_000

// Source is where a Sync reads from: a chain.Reader in the binary, a stand-in in tests.
type Source interface {
	// Transfers is every NUTZ Transfer in the inclusive block range, timestamped, in
	// (block, index) order.
	Transfers(ctx context.Context, from, to uint64) ([]chain.Transfer, error)

	// BlockByNumber is one header, which Sync uses only for its hash.
	BlockByNumber(ctx context.Context, number uint64) (chain.Block, error)
}

// SyncOptions tune a Sync. The zero value is fine.
type SyncOptions struct {
	// Chunk is how many blocks each read covers. Zero for the default.
	Chunk uint64

	// Progress, when set, is called after every chunk with the block the Cache now
	// reaches. The load test and the CLI's sync command both want to show something moving.
	Progress func(block uint64)
}

// Reorg is history the chain took back: the Cache held records from Block onward whose
// block hashes are no longer the chain's, and dropped them before reading the new branch.
type Reorg struct {
	Block   uint64 // the fork block: the first whose records were dropped
	Dropped int64
}

// Synced is what a Sync did.
type Synced struct {
	Reorg    *Reorg // nil when the held history still stood
	Appended int64
}

// Sync advances the Cache to tip, reading from src.
//
// tip is a block the caller resolved at the finality it wants — chain.Reader.Head — and it
// bounds two things: what is verified and what is read. The newest held record at or below
// it is checked against the chain by block hash; if the chain no longer has that block,
// the fork block is found by binary search over the held records and everything from it on
// is truncated (spec §9). Then the blocks after the newest held record are read up to tip
// and appended, a chunk at a time, so a sync that fails partway leaves every whole chunk
// before the failure on disk and resumes after it.
//
// Records held past tip are left as they are: a stricter finality than last time is not a
// reorg, and they are checked when a tip reaches them.
//
// Between the last held transfer and tip there may be blocks that carried none, and
// nothing in the file says they were read. Those blocks are read again next time; that is
// the price of the log being self-describing with no manifest, and it is bounded by how
// long the token has been quiet.
func (c *Cache) Sync(ctx context.Context, src Source, tip chain.Block, opts SyncOptions) (Synced, error) {
	if tip.Hash == (chain.Hash{}) {
		return Synced{}, errors.New("cache: sync needs a tip with a block hash; resolve one with Head first")
	}

	chunk := opts.Chunk
	if chunk == 0 {
		chunk = defaultChunk
	}

	var synced Synced

	reorg, err := c.verify(ctx, src, tip.Number)
	if err != nil {
		return synced, err
	}
	synced.Reorg = reorg

	from := c.start
	if last, ok := c.Last(); ok {
		if last.BlockNumber >= tip.Number {
			return synced, nil
		}

		from = last.BlockNumber + 1
	}

	for start := from; start <= tip.Number; {
		end := tip.Number
		if tip.Number-start >= chunk {
			end = start + chunk - 1
		}

		transfers, err := src.Transfers(ctx, start, end)
		if err != nil {
			return synced, fmt.Errorf("cache: syncing blocks %d..%d: %w", start, end, err)
		}

		records := make([]Record, 0, len(transfers))
		for _, t := range transfers {
			records = append(records, FromTransfer(t))
		}

		if err := c.Append(records); err != nil {
			return synced, err
		}
		synced.Appended += int64(len(records))

		if opts.Progress != nil {
			opts.Progress(end)
		}

		if end == tip.Number {
			break
		}

		start = end + 1
	}

	return synced, nil
}

// verify checks that the newest held record at or below tipNumber is still on the chain,
// and truncates at the fork block if it is not.
func (c *Cache) verify(ctx context.Context, src Source, tipNumber uint64) (*Reorg, error) {
	if c.count == 0 {
		return nil, nil
	}
	if err := c.writer.Flush(); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}

	// The records at or below the tip are [0, n). None means the whole cache is past the
	// tip, and there is nothing this tip can vouch for either way.
	n, err := c.firstAtOrAbove(tipNumber + 1)
	if err != nil || n == 0 {
		return nil, err
	}

	stillOnChain := func(i int64) (bool, error) {
		r, err := c.recordAt(i)
		if err != nil {
			return false, err
		}

		block, err := src.BlockByNumber(ctx, r.BlockNumber)
		if err != nil {
			return false, fmt.Errorf("cache: verifying block %d: %w", r.BlockNumber, err)
		}

		return block.Hash == r.BlockHash, nil
	}

	ok, err := stillOnChain(n - 1)
	if err != nil || ok {
		return nil, err
	}

	// The newest record is gone, so somewhere in [0, n) the chain forked, and "still on
	// chain" is true up to that point and false after it. Binary search for the first
	// false; every probe is one header read.
	fork, err := searchErr(n, func(i int64) (bool, error) {
		ok, err := stillOnChain(i)

		return !ok, err
	})
	if err != nil {
		return nil, err
	}

	r, err := c.recordAt(fork)
	if err != nil {
		return nil, err
	}

	dropped, err := c.Truncate(r.BlockNumber)
	if err != nil {
		return nil, err
	}

	return &Reorg{Block: r.BlockNumber, Dropped: dropped}, nil
}

// Truncate drops every record from block on and fsyncs, returning how many went. The
// fixed stride makes it a binary search and one syscall.
func (c *Cache) Truncate(block uint64) (int64, error) {
	if err := c.writer.Flush(); err != nil {
		return 0, fmt.Errorf("cache: %w", err)
	}

	keep, err := c.firstAtOrAbove(block)
	if err != nil {
		return 0, err
	}
	dropped := c.count - keep
	if dropped == 0 {
		return 0, nil
	}

	if err := c.file.Truncate(offsetOf(keep)); err != nil {
		return 0, fmt.Errorf("cache: truncating to record %d: %w", keep, err)
	}
	if err := c.sync(); err != nil {
		return 0, err
	}

	c.count = keep
	c.pending = 0
	if keep > 0 {
		if c.last, err = c.recordAt(keep - 1); err != nil {
			return 0, err
		}
	} else {
		c.last = Record{}
	}

	return dropped, nil
}

// firstAtOrAbove is the index of the first record from block on, or Len if there is none.
// The write buffer must be flushed.
func (c *Cache) firstAtOrAbove(block uint64) (int64, error) {
	return searchErr(c.count, func(i int64) (bool, error) {
		r, err := c.recordAt(i)

		return r.BlockNumber >= block, err
	})
}

// recordAt reads one record by index. The write buffer must be flushed.
func (c *Cache) recordAt(i int64) (Record, error) {
	var buf [stride]byte
	if _, err := c.file.ReadAt(buf[:], offsetOf(i)); err != nil {
		return Record{}, fmt.Errorf("cache: reading record %d: %w", i, err)
	}

	r, ok := decode(buf[:])
	if !ok {
		return Record{}, fmt.Errorf("cache: record %d fails its checksum; the file changed underneath this run", i)
	}

	return r, nil
}

// searchErr is sort.Search over a predicate that can fail. The predicate must be false then
// true over [0, n); the result is the first true, or n.
func searchErr(n int64, pred func(int64) (bool, error)) (int64, error) {
	var failed error
	i := sort.Search(int(n), func(i int) bool {
		if failed != nil {
			return true
		}

		ok, err := pred(int64(i))
		if err != nil {
			failed = err

			return true
		}

		return ok
	})
	if failed != nil {
		return 0, failed
	}

	return int64(i), nil
}
