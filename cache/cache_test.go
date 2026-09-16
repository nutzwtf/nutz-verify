package cache_test

import (
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/cache"
	"github.com/nutzwtf/nutz-verify/chain"
)

var (
	usdg  = repeatAddr(0x5f)
	alice = repeatAddr(0xa1)
	bob   = repeatAddr(0xb2)
)

const (
	chain4663 = 4663
	start     = 1000
)

func repeatAddr(b byte) chain.Address {
	var a chain.Address
	for i := range a {
		a[i] = b
	}

	return a
}

func hashOf(n uint64) chain.Hash {
	var h chain.Hash
	h[0], h[1], h[2], h[3] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
	h[31] = 0xff

	return h
}

// transfer is one Record at block n, timestamped as if the chain ran at one block a second.
func transfer(n uint64, from, to chain.Address, value int64) cache.Record {
	return cache.Record{
		BlockNumber: n,
		BlockHash:   hashOf(n),
		Timestamp:   int64(n) * 1,
		From:        from,
		To:          to,
		Value:       big.NewInt(value),
	}
}

func identity() cache.Options {
	return cache.Options{ChainID: chain4663, Token: usdg, Start: start}
}

func open(t *testing.T, dir string, opts cache.Options) *cache.Cache {
	t.Helper()

	c, err := cache.Open(dir, opts)
	if err != nil {
		t.Fatalf("Open = %v", err)
	}
	t.Cleanup(func() { c.Close() })

	return c
}

func all(t *testing.T, c *cache.Cache) []cache.Record {
	t.Helper()

	var out []cache.Record
	if err := c.Each(func(r cache.Record) error {
		out = append(out, r)

		return nil
	}); err != nil {
		t.Fatalf("Each = %v", err)
	}

	return out
}

func sameRecords(t *testing.T, got, want []cache.Record) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOpen_CreatesAndRoundTrips(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// A value that needs all 32 bytes, so the payload's uint256 word is exercised end to end
	// rather than through the low eight bytes a small literal would touch.
	huge, _ := new(big.Int).SetString("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", 16)

	want := []cache.Record{
		transfer(1000, alice, bob, 5),
		transfer(1000, bob, alice, 7),
		{BlockNumber: 1001, BlockHash: hashOf(1001), Timestamp: 1001, From: alice, To: bob, Value: huge},
		transfer(1003, alice, bob, 0),
	}

	c := open(t, dir, identity())
	if c.Len() != 0 {
		t.Fatalf("a fresh cache has %d records", c.Len())
	}
	if _, ok := c.Last(); ok {
		t.Fatal("a fresh cache has a last record")
	}

	if err := c.Append(want); err != nil {
		t.Fatalf("Append = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}

	again := open(t, dir, identity())
	if again.Len() != int64(len(want)) {
		t.Fatalf("reopened with %d records, want %d", again.Len(), len(want))
	}

	last, ok := again.Last()
	if !ok || !last.Equal(want[len(want)-1]) {
		t.Errorf("Last = %+v, %v; want the final record", last, ok)
	}

	sameRecords(t, all(t, again), want)
}

func TestOpen_RefusesAnotherChainOrToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	c := open(t, dir, identity())
	if err := c.Append([]cache.Record{transfer(1000, alice, bob, 1)}); err != nil {
		t.Fatal(err)
	}
	c.Close()

	tests := []struct {
		name string
		opts cache.Options
	}{
		{"a different chain id", cache.Options{ChainID: 1, Token: usdg, Start: start}},
		{"a different token", cache.Options{ChainID: chain4663, Token: alice, Start: start}},
		{"a different start block", cache.Options{ChainID: chain4663, Token: usdg, Start: start + 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cache.Open(dir, tt.opts)

			var mismatch *cache.Mismatch
			if !errors.As(err, &mismatch) {
				t.Fatalf("Open = %v, want a Mismatch", err)
			}

			// The user's way out is --fresh, and the error has to say so: a cache that
			// refuses to open without saying why or what to do is the opaque failure the
			// ticket is about.
			if !strings.Contains(err.Error(), "--fresh") {
				t.Errorf("error %q does not tell the user about --fresh", err)
			}
		})
	}

	// Fresh throws the file away rather than refusing it.
	fresh := open(t, dir, cache.Options{ChainID: 1, Token: usdg, Start: start, Fresh: true})
	if fresh.Len() != 0 {
		t.Errorf("Fresh kept %d records", fresh.Len())
	}
}

func TestOpen_RefusesASecondWriter(t *testing.T) {
	t.Parallel()

	// The layout assumes one writer. The Signer's hourly run overlapping a manual one would
	// otherwise interleave two valid-looking streams into one that runs backwards.
	dir := t.TempDir()
	first := open(t, dir, identity())

	if _, err := cache.Open(dir, identity()); err == nil {
		t.Fatal("a second Open on a cache already in use succeeded")
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	open(t, dir, identity()) // released with the first
}

func TestOpen_RefusesAFileThatIsNotACache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, cache.FileName), []byte("this is not a cache file, but it is longer than a header"))

	if _, err := cache.Open(dir, identity()); err == nil {
		t.Fatal("Open = nil error, want refusal")
	}
}

func TestAppend_RefusesGoingBackwards(t *testing.T) {
	t.Parallel()

	c := open(t, t.TempDir(), identity())
	if err := c.Append([]cache.Record{transfer(1005, alice, bob, 1)}); err != nil {
		t.Fatal(err)
	}

	if err := c.Append([]cache.Record{transfer(1004, alice, bob, 1)}); err == nil {
		t.Error("appended a record from an earlier block than the last")
	}
	if err := c.Append([]cache.Record{transfer(1006, alice, bob, 1), transfer(1005, alice, bob, 1)}); err == nil {
		t.Error("appended a batch that runs backwards")
	}
	if err := c.Append([]cache.Record{transfer(999, alice, bob, 1)}); err == nil {
		t.Error("appended a record from before the start block")
	}

	if c.Len() != 1 {
		t.Errorf("%d records after refused appends, want 1", c.Len())
	}
}

func TestAppend_RefusesAValueThatIsNotAUint256(t *testing.T) {
	t.Parallel()

	c := open(t, t.TempDir(), identity())

	tooBig := new(big.Int).Lsh(big.NewInt(1), 256)
	negative := big.NewInt(-1)

	for name, v := range map[string]*big.Int{"too big": tooBig, "negative": negative, "nil": nil} {
		r := transfer(1000, alice, bob, 0)
		r.Value = v

		if err := c.Append([]cache.Record{r}); err == nil {
			t.Errorf("appended a %s value", name)
		}
	}
}

func TestEach_StopsAtTheFirstError(t *testing.T) {
	t.Parallel()

	c := open(t, t.TempDir(), identity())
	if err := c.Append([]cache.Record{transfer(1000, alice, bob, 1), transfer(1001, alice, bob, 2)}); err != nil {
		t.Fatal(err)
	}

	stop := errors.New("enough")
	seen := 0
	err := c.Each(func(cache.Record) error {
		seen++

		return stop
	})

	if !errors.Is(err, stop) || seen != 1 {
		t.Errorf("Each = %v after %d records, want the callback's error after one", err, seen)
	}
}

func TestDir_IsUnderTheXDGCacheHome(t *testing.T) {
	// Not parallel: it sets the environment.
	t.Setenv("XDG_CACHE_HOME", "/var/cache/someone")

	dir, err := cache.Dir(chain4663, usdg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/var/cache/someone/nutz-verify/4663-" + usdg.String(); dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}

	// Spec §9: ${XDG_CACHE_HOME:-~/.cache}, so unset means the user's ~/.cache and not the
	// platform's own idea of a cache directory.
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", "/home/someone")

	dir, err = cache.Dir(chain4663, usdg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "/home/someone/.cache/nutz-verify/4663-" + usdg.String(); dir != want {
		t.Errorf("Dir = %q, want %q", dir, want)
	}
}

func TestAppend_IsAllOrNothing(t *testing.T) {
	t.Parallel()

	// A batch with a bad record in the middle writes nothing: a chunk half-appended would
	// end inside a block, and Sync resumes after the last held block.
	c := open(t, t.TempDir(), identity())
	bad := transfer(1001, alice, bob, 0)
	bad.Value = nil

	if err := c.Append([]cache.Record{transfer(1000, alice, bob, 1), bad}); err == nil {
		t.Fatal("Append = nil error with a nil value in the batch")
	}
	if c.Len() != 0 {
		t.Errorf("%d records after a refused batch, want 0", c.Len())
	}
	if _, ok := c.Last(); ok {
		t.Error("Last is set after a refused batch")
	}
}
