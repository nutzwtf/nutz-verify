// Package cache is the Verifier's own append-only record of NUTZ transfers and block
// timestamps, built from RPC and owned by whoever runs the binary (CONTEXT.md: Cache).
//
// It is a flat file rather than an embedded store (ADR-0004): the workload is write once in
// block order, replay sequentially, truncate at a reorg, and that last mutation is the one a
// key-value store does worst. The layout is a fixed header followed by fixed-stride records,
// [120-byte payload][4-byte CRC32C], so record i lives at a computable offset and a binary
// search over block numbers needs no index.
//
// # Detect damage, do not survive it
//
// The durability requirement is unusually weak, and the design leans on that. Everything in
// the file is public chain data, so anything lost is refetched; what must never happen is a
// damaged record being fed into a Merkle root, because that yields a MISMATCH with no
// explanation, which is the failure that destroys trust in a verifier. So every record is
// checksummed, every record is verified on open and again on read, the file is truncated to
// the last good record rather than trusted past it, and a header from another chain or
// token refuses to open instead of answering confidently.
//
// fsync is batched on a checkpoint interval and never per record: per-record costs about
// 6.7x (docs/research/go-deps-and-storage.md) and buys nothing here, since a torn tail is
// exactly what truncate-on-open repairs.
package cache

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math/big"
	"os"
	"path/filepath"

	"github.com/nutzwtf/nutz-verify/chain"
)

// FileName is the log inside the Cache directory. One file, because the Cache holds one
// stream: the Distributor's own logs are a handful of requests and are read live.
const FileName = "transfers.log"

// Layout. Every offset below is fixed so a record's position is arithmetic.
const (
	// magic marks a file as ours. A file that does not start with it is not a damaged cache;
	// it is not a cache, and is refused rather than repaired.
	magic = "NUTZCACH"

	// formatVersion changes when the record layout does. A file at another version refuses
	// to open: decoding it under the wrong layout would be quiet and wrong.
	formatVersion = 1

	// headerSize is magic, version, chain id, token, start block and a CRC32C over all of
	// them. Records begin here.
	headerSize = 8 + 4 + 8 + 20 + 8 + 4

	// payloadSize is (blockNumber, blockHash, timestamp, from, to, value): 8+32+8+20+20+32.
	payloadSize = 120

	// stride is a payload and its CRC32C; the unit the file is measured in.
	stride = payloadSize + 4

	// checkpointRecords is how many appended records may sit unsynced before the next
	// Append fsyncs. Every 10,000 is on the flat part of the measured curve, and since a
	// batch is written whole, a crash loses at most a batch past that of refetchable history.
	checkpointRecords = 10_000

	// bufferSize is the read and write buffer. Sequential I/O in 1 MiB steps is what the
	// kernel's readahead is built for.
	bufferSize = 1 << 20
)

// crcTable is Castagnoli, which Go dispatches to a hardware instruction on both amd64 and
// arm64. Not IEEE: the stdlib docs note Castagnoli has better error detection.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// Record is one NUTZ Transfer as the Cache stores it: where it sat on chain, when that block
// was, and the transfer itself. It is chain.Transfer minus the log index, which the rules do
// not need and the payload does not carry.
type Record struct {
	BlockNumber uint64
	BlockHash   chain.Hash
	Timestamp   int64
	From        chain.Address
	To          chain.Address
	Value       *big.Int // a uint256; nil or negative is refused on Append
}

// FromTransfer is the Record of a decoded, timestamped Transfer log.
func FromTransfer(t chain.Transfer) Record {
	return Record{
		BlockNumber: t.BlockNumber,
		BlockHash:   t.BlockHash,
		Timestamp:   t.Timestamp,
		From:        t.From,
		To:          t.To,
		Value:       t.Value,
	}
}

// Equal reports whether two Records carry the same payload. Values compare numerically, so
// a nil Value equals nothing.
func (r Record) Equal(o Record) bool {
	return r.BlockNumber == o.BlockNumber &&
		r.BlockHash == o.BlockHash &&
		r.Timestamp == o.Timestamp &&
		r.From == o.From &&
		r.To == o.To &&
		r.Value != nil && o.Value != nil && r.Value.Cmp(o.Value) == 0
}

// encode writes the payload and its CRC into a stride-sized buffer.
func (r Record) encode(buf []byte) error {
	if r.Value == nil {
		return errors.New("value is nil, which is not the same as zero")
	}
	if r.Value.Sign() < 0 {
		return errors.New("value is negative, and uint256 has no sign")
	}
	if r.Value.BitLen() > 256 {
		return errors.New("value does not fit a uint256")
	}

	binary.BigEndian.PutUint64(buf[0:8], r.BlockNumber)
	copy(buf[8:40], r.BlockHash[:])
	binary.BigEndian.PutUint64(buf[40:48], uint64(r.Timestamp))
	copy(buf[48:68], r.From[:])
	copy(buf[68:88], r.To[:])
	r.Value.FillBytes(buf[88:120])
	binary.BigEndian.PutUint32(buf[payloadSize:stride], crc32.Checksum(buf[:payloadSize], crcTable))

	return nil
}

// decode reads a stride-sized buffer, verifying its CRC first.
func decode(buf []byte) (Record, bool) {
	if crc32.Checksum(buf[:payloadSize], crcTable) != binary.BigEndian.Uint32(buf[payloadSize:stride]) {
		return Record{}, false
	}

	var r Record
	r.BlockNumber = binary.BigEndian.Uint64(buf[0:8])
	copy(r.BlockHash[:], buf[8:40])
	r.Timestamp = int64(binary.BigEndian.Uint64(buf[40:48]))
	copy(r.From[:], buf[48:68])
	copy(r.To[:], buf[68:88])
	r.Value = new(big.Int).SetBytes(buf[88:120])

	return r, true
}

// Options identify the history a Cache holds. All three are written into the header on
// creation and checked against it on every open, because a cache that silently served
// another chain's, another token's or a later-starting history would be a confident wrong
// answer rather than an error.
type Options struct {
	ChainID uint64
	Token   chain.Address

	// Start is the first block the history is read from: the token's creation block. It is
	// part of the identity because two runs that disagree about it have different
	// histories, and the one that started later is missing balances.
	Start uint64

	// Fresh discards whatever is on disk and starts over. It is the answer to a Mismatch.
	Fresh bool
}

// Mismatch is a cache built for a different history than this run's: another chain, token,
// start block, or format version. It is never resolved by opening anyway.
type Mismatch struct {
	What string // "chain id", "token", "start block", "format version"
	File string // what the file's header says
	Run  string // what this run asked for
}

func (m *Mismatch) Error() string {
	return fmt.Sprintf("cache: this cache was built for %s %s and this run is for %s %s; "+
		"pass --fresh to rebuild it rather than mixing the two", m.What, m.File, m.What, m.Run)
}

// Dir is where the Cache for one chain and token lives:
// ${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/ (spec §9).
func Dir(chainID uint64, token chain.Address) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cache: no XDG_CACHE_HOME and %w", err)
		}

		base = filepath.Join(home, ".cache")
	}

	return filepath.Join(base, "nutz-verify", fmt.Sprintf("%d-%s", chainID, token)), nil
}

// Cache is one open transfer log. A single process writes it at a time; Open takes a lock.
type Cache struct {
	file   *os.File
	writer *bufio.Writer

	count   int64  // records on disk and in the write buffer
	last    Record // the newest record, meaningful when count > 0
	start   uint64
	pending int // records appended since the last checkpoint

	repaired *Repair
}

// Open opens or creates the Cache in dir, verifies every record on the way in, and repairs
// a damaged tail.
//
// A file whose header names another chain, token or start block is a Mismatch. A file that
// is not a cache at all is refused. A file that is ours but damaged is truncated to its last
// good record — see Repair — and opens.
func Open(dir string, opts Options) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}

	flags := os.O_RDWR | os.O_CREATE | os.O_APPEND
	if opts.Fresh {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(filepath.Join(dir, FileName), flags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}

	if err := lock(file); err != nil {
		file.Close()

		return nil, err
	}

	c := &Cache{file: file, writer: bufio.NewWriterSize(file, bufferSize), start: opts.Start}

	if err := c.load(opts); err != nil {
		file.Close()

		return nil, err
	}

	return c, nil
}

// load reads or writes the header, then scans every record.
func (c *Cache) load(opts Options) error {
	info, err := c.file.Stat()
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	if info.Size() == 0 {
		return c.writeHeader(opts)
	}

	// Shorter than a header is a crash during the first write of a fresh cache. Nothing of
	// ours can be in it, so it is the empty case with a note, not a refusal.
	if info.Size() < headerSize {
		if err := c.file.Truncate(0); err != nil {
			return fmt.Errorf("cache: %w", err)
		}
		if err := c.writeHeader(opts); err != nil {
			return err
		}

		c.repaired = &Repair{Reason: fmt.Sprintf("a partial header of %d bytes", info.Size())}

		return nil
	}

	if err := c.checkHeader(opts); err != nil {
		return err
	}

	return c.scan(info.Size())
}

func (c *Cache) writeHeader(opts Options) error {
	var buf [headerSize]byte
	copy(buf[0:8], magic)
	binary.BigEndian.PutUint32(buf[8:12], formatVersion)
	binary.BigEndian.PutUint64(buf[12:20], opts.ChainID)
	copy(buf[20:40], opts.Token[:])
	binary.BigEndian.PutUint64(buf[40:48], opts.Start)
	binary.BigEndian.PutUint32(buf[48:52], crc32.Checksum(buf[:48], crcTable))

	if _, err := c.file.Write(buf[:]); err != nil {
		return fmt.Errorf("cache: writing the header: %w", err)
	}

	return c.sync()
}

func (c *Cache) checkHeader(opts Options) error {
	var buf [headerSize]byte
	if _, err := io.ReadFull(io.NewSectionReader(c.file, 0, headerSize), buf[:]); err != nil {
		return fmt.Errorf("cache: reading the header of %s: %w", FileName, err)
	}

	if string(buf[0:8]) != magic {
		return fmt.Errorf("cache: %s is not a nutz-verify cache; pass --fresh to replace it", FileName)
	}
	if crc32.Checksum(buf[:48], crcTable) != binary.BigEndian.Uint32(buf[48:52]) {
		return fmt.Errorf("cache: the header of %s is damaged; pass --fresh to rebuild it", FileName)
	}

	if v := binary.BigEndian.Uint32(buf[8:12]); v != formatVersion {
		return &Mismatch{What: "format version", File: fmt.Sprint(v), Run: fmt.Sprint(formatVersion)}
	}
	if id := binary.BigEndian.Uint64(buf[12:20]); id != opts.ChainID {
		return &Mismatch{What: "chain id", File: fmt.Sprint(id), Run: fmt.Sprint(opts.ChainID)}
	}

	var token chain.Address
	copy(token[:], buf[20:40])
	if token != opts.Token {
		return &Mismatch{What: "token", File: token.String(), Run: opts.Token.String()}
	}

	if s := binary.BigEndian.Uint64(buf[40:48]); s != opts.Start {
		return &Mismatch{What: "start block", File: fmt.Sprint(s), Run: fmt.Sprint(opts.Start)}
	}

	return nil
}

// Repair is what Open found wrong with the file and did about it: it truncated to the last
// whole block that verified. Everything dropped is refetched by the next Sync.
//
// A whole block, not the last good record. A checkpoint falls wherever 10,000 records fall,
// so damage can land inside a block, and the good records before it in that block are not
// the whole block. Sync resumes after the last held block, so keeping half of one would lose
// the other half for good and every later TWAB would be quietly wrong.
type Repair struct {
	// Reason says what was wrong with record At: a short tail, a CRC failure, or a block
	// number out of order.
	Reason string
	At     int64

	// Kept is how many records survived, and LastGoodBlock the block the kept history runs
	// through — every record of it is held. Zero when nothing survived.
	Kept          int64
	LastGoodBlock uint64

	// Dropped is how many complete records were discarded, not counting a partial tail:
	// the damaged ones, and the good ones of the block the damage fell in.
	Dropped int64
}

func (r *Repair) String() string {
	return fmt.Sprintf("cache: %s at record %d; kept %d records through block %d and dropped %d, "+
		"which the next sync refetches", r.Reason, r.At, r.Kept, r.LastGoodBlock, r.Dropped)
}

// Repaired is what Open had to do to the file, or nil when it verified end to end.
func (c *Cache) Repaired() *Repair { return c.repaired }

// scan verifies every record from the header to the end, and truncates at the start of
// the block the first bad record falls in.
//
// The whole file, on every open. Replaying it is what a Recompute does anyway, and a check
// that stopped at the tail would leave a flipped bit in the middle to surface as a MISMATCH
// with no explanation.
func (c *Cache) scan(size int64) error {
	reader := bufio.NewReaderSize(io.NewSectionReader(c.file, headerSize, size-headerSize), bufferSize)

	var buf [stride]byte

	// blockStart is the index of the first record of the block c.last is in, and
	// beforeBlock the block of the record before that: what survives if this block has to
	// go.
	var blockStart int64
	var beforeBlock uint64

	for {
		n, err := io.ReadFull(reader, buf[:])
		if err == io.EOF {
			return nil
		}
		if err == io.ErrUnexpectedEOF {
			return c.repair(fmt.Sprintf("a partial record of %d bytes", n), size, blockStart, beforeBlock)
		}
		if err != nil {
			return fmt.Errorf("cache: reading record %d: %w", c.count, err)
		}

		r, ok := decode(buf[:])
		switch {
		case !ok:
			return c.repair("a record that fails its checksum", size, blockStart, beforeBlock)
		case r.BlockNumber < c.start:
			return c.repair(fmt.Sprintf("a record from block %d, before the start block %d",
				r.BlockNumber, c.start), size, blockStart, beforeBlock)
		case c.count > 0 && r.BlockNumber < c.last.BlockNumber:
			return c.repair(fmt.Sprintf("a record from block %d, behind the previous %d",
				r.BlockNumber, c.last.BlockNumber), size, blockStart, beforeBlock)
		}

		if c.count > 0 && r.BlockNumber != c.last.BlockNumber {
			blockStart, beforeBlock = c.count, c.last.BlockNumber
		}

		c.count++
		c.last = r
	}
}

// repair cuts the file back to record index keep during scan — the start of the block the
// damage at c.count fell in — and records why. size is the file's length before the cut.
func (c *Cache) repair(reason string, size, keep int64, lastGoodBlock uint64) error {
	complete := (size - headerSize) / stride

	if err := c.file.Truncate(offsetOf(keep)); err != nil {
		return fmt.Errorf("cache: truncating to record %d: %w", keep, err)
	}
	if err := c.sync(); err != nil {
		return err
	}

	c.repaired = &Repair{Reason: reason, At: c.count, Kept: keep, LastGoodBlock: lastGoodBlock, Dropped: complete - keep}
	c.count = keep
	if keep == 0 {
		c.last = Record{}
	} else {
		var err error
		if c.last, err = c.recordAt(keep - 1); err != nil {
			return err
		}
	}

	return nil
}

func offsetOf(i int64) int64 {
	return headerSize + i*stride
}

// Len is how many records the Cache holds.
func (c *Cache) Len() int64 { return c.count }

// Last is the newest record, and false when the Cache is empty.
func (c *Cache) Last() (Record, bool) {
	return c.last, c.count > 0
}

// Append adds records in order, all of them or none. A batch runs forward: its block numbers
// are non-decreasing and none is behind the last record already held, because the Cache is
// a prefix of the chain's history and a record out of place would break every replay's
// ordering. It is also whole blocks, which is the caller's to keep — Sync's chunks are — and
// which all-or-nothing preserves: a batch that is refused leaves the file as it was.
//
// Not durable until the next checkpoint or Close. A crash loses the tail, and Open refetches
// it; that is the bargain the package comment makes.
func (c *Cache) Append(records []Record) error {
	floor := c.start
	if c.count > 0 {
		floor = c.last.BlockNumber
	}

	encoded := make([]byte, len(records)*stride)
	for i, r := range records {
		if r.BlockNumber < floor {
			return fmt.Errorf("cache: record %d of the batch is from block %d, behind block %d",
				i, r.BlockNumber, floor)
		}
		if err := r.encode(encoded[i*stride : (i+1)*stride]); err != nil {
			return fmt.Errorf("cache: record %d of the batch, at block %d: %w", i, r.BlockNumber, err)
		}

		floor = r.BlockNumber
	}

	if len(records) == 0 {
		return nil
	}
	if _, err := c.writer.Write(encoded); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	c.count += int64(len(records))
	c.last = records[len(records)-1]
	c.pending += len(records)

	if c.pending >= checkpointRecords {
		return c.checkpoint()
	}

	return nil
}

// checkpoint flushes and fsyncs what has been appended so far.
func (c *Cache) checkpoint() error {
	if err := c.writer.Flush(); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	if err := c.sync(); err != nil {
		return err
	}

	c.pending = 0

	return nil
}

func (c *Cache) sync() error {
	if err := c.file.Sync(); err != nil {
		return fmt.Errorf("cache: fsync: %w", err)
	}

	return nil
}

// Each calls fn for every record in append order, which is block order, stopping at the
// first error. Every record's CRC is verified again on the way out.
func (c *Cache) Each(fn func(Record) error) error {
	if err := c.writer.Flush(); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	reader := bufio.NewReaderSize(io.NewSectionReader(c.file, headerSize, c.count*stride), bufferSize)

	var buf [stride]byte
	var prev uint64
	for i := range c.count {
		if _, err := io.ReadFull(reader, buf[:]); err != nil {
			return fmt.Errorf("cache: reading record %d of %d: %w", i, c.count, err)
		}

		r, ok := decode(buf[:])
		if !ok {
			return fmt.Errorf("cache: record %d fails its checksum, after block %d; "+
				"the file changed underneath this run", i, prev)
		}

		if err := fn(r); err != nil {
			return err
		}

		prev = r.BlockNumber
	}

	return nil
}

// Close checkpoints and releases the file. The Cache is unusable afterwards.
func (c *Cache) Close() error {
	if c.file == nil {
		return nil
	}

	err := c.checkpoint()
	if closeErr := c.file.Close(); err == nil && closeErr != nil {
		err = fmt.Errorf("cache: %w", closeErr)
	}

	c.file = nil

	return err
}
