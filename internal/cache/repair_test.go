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
	if repair.Kept != 4 || repair.LastGoodBlock != want[3].BlockNumber || repair.Dropped != 0 {
		t.Errorf("Repaired = %+v, want 4 kept through block %d and nothing dropped", repair, want[3].BlockNumber)
	}

	sameRecords(t, all(t, c), want[:4])

	// And the file is actually shorter: a repair that only pretended would leave the next
	// append landing after the garbage.
	if err := c.Append(want[4:]); err != nil {
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
	if repair.Kept != 3 || repair.LastGoodBlock != want[2].BlockNumber || repair.Dropped != 7 {
		t.Errorf("Repaired = %+v, want 3 kept through block %d and 7 dropped", repair, want[2].BlockNumber)
	}
	if !strings.Contains(repair.String(), "checksum") || !strings.Contains(repair.String(), "1002") {
		t.Errorf("Repaired.String() = %q: does not say it was a checksum, or does not name block 1002", repair)
	}

	sameRecords(t, all(t, c), want[:3])
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

	if repair := c.Repaired(); repair == nil || repair.Kept != 4 {
		t.Fatalf("Repaired = %+v, want a repair keeping 4", repair)
	}

	sameRecords(t, all(t, c), want[:4])
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
