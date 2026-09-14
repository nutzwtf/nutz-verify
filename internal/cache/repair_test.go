package cache_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/cache"
)

// fill writes n records at blocks start, start+1, ... and closes the cache, returning what
// it wrote.
func fill(t *testing.T, dir string, n int) []cache.Record {
	t.Helper()

	records := make([]cache.Record, 0, n)
	for i := range n {
		records = append(records, transfer(start+uint64(i), alice, bob, int64(i)))
	}

	c := open(t, dir, identity())
	if err := c.Append(records); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	return records
}

func TestOpen_HealsATornTail(t *testing.T) {
	t.Parallel()

	// A crash mid-append leaves a partial record at the end. That is the failure batched
	// fsync makes likely, and it is meant to be free: the tail is dropped and the next sync
	// resumes from the last record that verified.
	dir := t.TempDir()
	want := fill(t, dir, 5)

	path := filepath.Join(dir, cache.FileName)
	data := readFile(t, path)
	writeFile(t, path, data[:len(data)-37])

	c := open(t, dir, identity())

	repair := c.Repaired()
	if repair == nil {
		t.Fatal("Repaired = nil, want a repair of the torn tail")
	}
	// Record 4 was torn, and nothing says record 4 was not the rest of record 3's block,
	// so block 1003 goes too and the history is whole through 1002.
	if repair.At != 4 || repair.Kept != 3 || repair.LastGoodBlock != want[2].BlockNumber || repair.Dropped != 1 {
		t.Errorf("Repaired = %+v, want 3 kept through block %d and 1 dropped", repair, want[2].BlockNumber)
	}

	sameRecords(t, all(t, c), want[:3])

	// And the file is actually shorter: a repair that only pretended would leave the next
	// append landing after the garbage.
	if err := c.Append(want[3:]); err != nil {
		t.Fatal(err)
	}
	c.Close()

	sameRecords(t, all(t, open(t, dir, identity())), want)
}

func TestOpen_AFlippedBitNamesTheBlock(t *testing.T) {
	t.Parallel()

	// Silent bit rot in a file fed into a Merkle root is the scariest failure the cache
	// has: a MISMATCH with no explanation. The CRC turns it into a repair that says where
	// the history is intact up to, so the user can tell disk damage from a wrong Root.
	dir := t.TempDir()
	want := fill(t, dir, 10)

	path := filepath.Join(dir, cache.FileName)
	data := readFile(t, path)
	third := len(data) - 7*124 + 90 // inside record 3's value word
	data[third] ^= 0x01
	writeFile(t, path, data)

	c := open(t, dir, identity())

	repair := c.Repaired()
	if repair == nil {
		t.Fatal("Repaired = nil, want a checksum repair")
	}
	if repair.At != 3 || repair.Kept != 2 || repair.LastGoodBlock != want[1].BlockNumber || repair.Dropped != 8 {
		t.Errorf("Repaired = %+v, want 2 kept through block %d and 8 dropped", repair, want[1].BlockNumber)
	}
	if !strings.Contains(repair.String(), "checksum") || !strings.Contains(repair.String(), "1001") {
		t.Errorf("Repaired.String() = %q: does not say it was a checksum, or does not name block 1001", repair)
	}

	sameRecords(t, all(t, c), want[:2])
}

func TestOpen_ARecordOutOfOrderIsDamage(t *testing.T) {
	t.Parallel()

	// A record from behind the previous one cannot come from an append — Append refuses
	// it — so it is a torn or interleaved write whose CRC happens to pass. The scan holds
	// the same rule the writer does.
	dir := t.TempDir()
	want := fill(t, dir, 6)

	path := filepath.Join(dir, cache.FileName)
	data := readFile(t, path)
	rec := func(i int) []byte { return data[len(data)-(6-i)*124:][:124] }
	copy(rec(4), rec(1))
	writeFile(t, path, data)

	c := open(t, dir, identity())

	if repair := c.Repaired(); repair == nil || repair.At != 4 || repair.Kept != 3 {
		t.Fatalf("Repaired = %+v, want a repair at record 4 keeping 3", repair)
	}

	sameRecords(t, all(t, c), want[:3])
}

func TestOpen_AHeaderAloneIsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	open(t, dir, identity()).Close()

	c := open(t, dir, identity())
	if c.Len() != 0 || c.Repaired() != nil {
		t.Errorf("a header-only file opened with %d records and repair %+v", c.Len(), c.Repaired())
	}
}

func TestOpen_ADamagedHeaderRefuses(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fill(t, dir, 2)

	path := filepath.Join(dir, cache.FileName)
	data := readFile(t, path)
	data[15] ^= 0x80 // in the chain id
	writeFile(t, path, data)

	_, err := cache.Open(dir, identity())
	if err == nil {
		t.Fatal("Open = nil error on a damaged header")
	}
	// A flipped bit in the chain id must not read as "wrong chain": that would send the
	// user looking for a configuration mistake they did not make.
	if !strings.Contains(err.Error(), "damaged") {
		t.Errorf("Open = %v, want a damaged-header error", err)
	}
}

func TestOpen_ARepairDropsTheWholeLastBlock(t *testing.T) {
	t.Parallel()

	// A checkpoint falls wherever 10,000 records fall, so a torn tail can land inside a
	// block. Keeping that block's first records and resuming after it would lose the rest
	// of the block's transfers for good — Sync reads from the last held block plus one —
	// and every later TWAB would be quietly wrong. So a repair cuts back to a block
	// boundary: the last held block is always a whole one.
	dir := t.TempDir()
	c := open(t, dir, identity())
	want := []cache.Record{
		transfer(1000, alice, bob, 1),
		transfer(1001, alice, bob, 2),
		transfer(1002, alice, bob, 3),
		transfer(1002, bob, alice, 4),
		transfer(1002, alice, bob, 5), // the tail tears inside block 1002
	}
	if err := c.Append(want); err != nil {
		t.Fatal(err)
	}
	c.Close()

	path := filepath.Join(dir, cache.FileName)
	data := readFile(t, path)
	writeFile(t, path, data[:len(data)-20])

	c = open(t, dir, identity())

	repair := c.Repaired()
	if repair == nil || repair.Kept != 2 || repair.LastGoodBlock != 1001 {
		t.Fatalf("Repaired = %+v, want block 1002 dropped whole, keeping 2 records through block 1001", repair)
	}
	sameRecords(t, all(t, c), want[:2])

	// And the next sync refetches block 1002 in full.
	f := newFakeChain(1002)
	for _, r := range want {
		f.add(r.BlockNumber, r.From, r.To, r.Value.Int64())
	}
	syncTo(t, c, f, cache.SyncOptions{})
	sameRecords(t, all(t, c), want)
}

func TestOpen_APartialHeaderIsAnEmptyCache(t *testing.T) {
	t.Parallel()

	// A crash during the very first write leaves less than a header. Nothing of ours can be
	// in it, so it opens empty rather than sending the user to --fresh for a file that
	// never held anything.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, cache.FileName), []byte("NUTZCACH\x00\x00"))

	c := open(t, dir, identity())
	if c.Len() != 0 || c.Repaired() == nil {
		t.Fatalf("opened with %d records and repair %+v; want empty with a note", c.Len(), c.Repaired())
	}
	if err := c.Append([]cache.Record{transfer(1000, alice, bob, 1)}); err != nil {
		t.Fatal(err)
	}
	c.Close()

	if again := open(t, dir, identity()); again.Len() != 1 || again.Repaired() != nil {
		t.Errorf("reopened with %d records and repair %+v; want 1 and no repair", again.Len(), again.Repaired())
	}
}
