# 04 — Append-only Cache with CRC, truncate-on-open and reorg truncation

Status: resolved
Type: task
Spec: ../spec.md §9; ADR-0004
Blocked by: 03

Implement `internal/cache`: fixed 124-byte stride, `[120-byte payload][4-byte CRC32C]`, payload `(blockNumber, blockHash, timestamp, from, to, value)`, under `${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/`. Fixed file header with magic, format version, chain id and contract address; a mismatch refuses to open and tells the user to `--fresh` rather than mixing caches. No sidecar manifest — the CRC chain plus each record's block number make the log self-describing.

Truncate-to-last-valid-record on open. Batched fsync on a checkpoint interval, never per record (measured ~6.7× penalty). Reorg: binary-search the fixed stride for the fork block, `Truncate(i * 124)`, fsync. The durability requirement is deliberately weak — the log caches public data, so anything lost is refetched; we must detect damage, not survive it.

Tests: a truncated tail mid-record self-heals on open and resumes from the last good record; a flipped bit is caught by CRC and names the block number rather than failing opaquely; a header from a different chain id refuses to open; reorg truncation lands on the right offset and replay afterwards is byte-identical to a `--fresh` sync over the same range.

Then the load test, which is the real point of this ticket: point ingestion at USDG history on chain 4663 and record how long a from-scratch sync actually takes. At ~100 ms blocks (~864k blocks/day) the risk here is volume, not logic, and a Verifier that cannot finish inside a 30-minute Dispute window is decorative. Write the measured numbers into ../spec.md §9.

## What ticket 03 already measured (2026-09-14), so this starts from facts

Recorded in ../spec.md §4 under "What the endpoints actually limit". The short version:

- **There is no 2,000-block range cap on the public endpoint.** The limit is **10,000 results**, and
  `internal/chain` now halves its page on refusal. Do not reintroduce a range constant.
- **USDG runs at 4.9–5.4 `Transfer` logs per block.** Essentially every block has one.
- **The block-timestamp join is the cost, not the logs.** `Reader.Transfers` fetches one header per
  block that produced a log, over a pool of eight. For a dense token that is one request per block:
  **~864,000 header requests per day of history**. The logs themselves are ~10 requests per day.
  The load test will almost certainly find that this, not `eth_getLogs`, is what decides minutes
  versus hours — and that the answer is JSON-RPC batching of `eth_getBlockByNumber`, which was left
  out of ticket 03 because an endpoint that does not support batching would fail loudly. Measure
  before building it, but expect to build it.
- **The public endpoint throttles**: `429` under sustained querying, a short `403` if pushed harder.
  `internal/chain` retries `429` and `5xx` five times with jittered backoff and honours
  `Retry-After`, then reports INDETERMINATE. That budget is sized for a Dispute-window run, not a
  from-scratch sync; the Cache is what makes a from-scratch sync a one-time cost, and the load
  test should say whether eight header workers is already too many for that endpoint.
- **NUTZ does not exist yet**, so its density is unknown. USDG is the pessimistic proxy spec §10
  names; the numbers above are an upper bound on the real workload, not an estimate of it.

## Comments

**2026-09-14 — implemented.**

`internal/cache` is the flat file spec §9 describes: a 52-byte header (magic, format version,
chain id, token, start block, CRC32C) and then 124-byte records, `[120-byte payload][4-byte
CRC32C]`, payload `(blockNumber, blockHash, timestamp, from, to, value)`. Every record is verified
on open and again on read; the file is truncated to the last good record on open; fsync is every
10,000 records and on close, never per record; a reorg is a binary search over the stride and one
`Truncate`. `Sync` verifies the newest held record at or below the caller's tip by block hash,
finds the fork by binary search if it is gone, and then appends a chunk at a time so an
interrupted sync resumes after its last whole chunk. `go.mod` is untouched.

All four of the ticket's tests are there and pass: a torn tail self-heals and the next append lands
after the last good record; a flipped bit is caught by the CRC and the repair names the last good
block and how many records were dropped; a header from another chain (or token, or start block,
or format version) refuses with an error that says `--fresh`; and a reorg found by `Sync` leaves a
file byte-identical to a `--fresh` sync of the same history.

**The load test is the finding, and it is not the one the ticket expected.** Numbers are in
../spec.md §9; the shape:

- **The public endpoint budgets JSON-RPC calls, batched or not, at ~15–20 a second** from a bucket
  of ~100. Batching cuts HTTP overhead, not the budget. The first load test run tripped the bucket
  71 calls in and the five retries did not outlast it, so the client now *spends* the budget on
  purpose: `internal/chain` batches headers 20 at a time and paces every endpoint with a token
  bucket at `DefaultCallsPerSecond = 15`, counting each request inside a batch. The 5,000-block run
  then completed with nothing refused, at 17 blocks/s.
- **At that pace a from-scratch sync of a USDG-dense token is ~5–6 weeks**, ~7.5 hours per day of
  history, ~19 minutes per hour. The public endpoint cannot be made to do better; a keyed archive
  provider with `--rate` raised is how the warm Signer should build its Cache, after which the
  hourly catch-up fits inside a window. NUTZ's density decides whether any of this matters; USDG is
  the pessimistic proxy and the number to remeasure at launch is the density.
- **The chain is not at 100 ms blocks.** 63.18M blocks in 137 days is ~460,000 a day, ~190 ms
  average; an Orbit chain seals on demand. `blocksPerDay` in the load test is the measured figure.
- **Two dead ends recorded so nobody repeats them.** Nitro's `eth_getLogs` carries a
  `blockTimestamp` field and it is `0x0` at every height, so the header join cannot be skipped. And
  the endpoint answers 403 to `Python-urllib`'s User-Agent before any limit applies — the "short
  403" ticket 03 attributed to pushing harder may have been this.

### Decisions worth knowing

- **Start block is in the header.** The ticket lists magic, version, chain id and address; the
  header also carries the block the history was read from. Two runs disagreeing about it have
  different histories and the later one is missing balances, which is a confident wrong answer of
  the exact kind the header exists to refuse. It is the reason `cache.Options.Start` exists and
  why ticket 05 has to pin it as a constant rather than a flag anyone can vary.
- **One writer, enforced.** The file is `flock`'d while open. The Signer's hourly run overlapping a
  manual one would interleave records that each verify and together run backwards; the scan
  refuses a record behind its predecessor for the same reason, as a second line.
- **A mid-file CRC failure heals like a torn tail** rather than refusing to open. Truncating there
  throws away everything after it, all refetchable; refusing would make the user do the same thing
  by hand with `--fresh`. `Repaired()` says what happened and names the last good block, so ticket
  05 can print it and disk damage does not read as a wrong Root.
- **No watermark, as specified, and it has a cost.** The file says nothing about blocks that were
  read and carried no transfer, so every `Sync` re-reads from the last held record's block. Bounded
  by how long the token has been quiet, and empty pages are cheap; noted in `Sync`'s doc comment
  in case NUTZ turns out to be quiet enough for it to matter.
- **The endpoint refusing batches degrades rather than fails.** A batch answered with a single
  error object or a non-retryable HTTP status marks the endpoint `noBatch` and it is asked one
  header at a time from then on; a 429 on a batch is retried as a batch, since that is not "no
  batches". Replies are matched by id, never position, and every header is checked to be the block
  asked for.
- **Test readers are unpaced.** The pacer is for a public endpoint's budget; fake-node and anvil
  readers pass `CallsPerSecond: unpaced` so the chain suite stays at ~13 s. The live tests keep the
  default, which is the point of them.

### Fixed in review

Both axes independently flagged the first, which is usually the sign it is real.

- **A repair cut inside a block lost the rest of that block for good.** Truncate-to-last-valid-
  *record* is what the ticket says, and it is wrong by one block: a checkpoint falls wherever
  10,000 records fall, so a torn tail lands mid-block at USDG's density almost every time, and
  `Sync` resumes after the last held block. The good records of the damaged block were kept and
  its remaining transfers never refetched — a confident wrong answer of exactly the kind the CRC
  exists to prevent, and the one-record-per-block test could not see it. A repair now cuts back to
  the start of the block the damage fell in, `Repair` says so, and `Append` is all-or-nothing so a
  refused chunk cannot leave a block half-held either. Spec §9's "truncate-to-last-valid-record"
  should be read as "to the last whole valid block"; the test that pins it tears a block in half.
- **A dropped connection permanently disabled batching.** Any non-retryable failure of a batch
  request counted as "this endpoint does not batch", including a cancelled context or a reset
  connection, after which every read paid a request per header for the rest of the run — twenty
  times the calls at fifteen a second. Only an HTTP status the endpoint chose, or a single error
  object in reply to an array, counts now.
- A file shorter than a header — a crash during a fresh cache's first write — opens empty with a
  note rather than sending the user to `--fresh` for a file that never held anything.
- The request breakdown recorded in spec §9 was written by hand and wrong: five `eth_getLogs`
  pages, not three. `Each`'s checksum error named the newest block rather than the one before the
  damage. `Sync`'s doc claimed whole chunks were on disk after a failure; they are in the Cache,
  and on disk at the next checkpoint or Close.
- **Declined:** dropping `lock_other.go` (eight lines that keep `go vet` honest on a GOOS spec §3
  does not ship); unifying `scan` and `Each`'s read loops (they differ in exactly the part that
  matters, what a bad record means).

### Follow-ups, not done here

- **`--rate` for ticket 05**, with the default printed in the run header (noted in 05).
- **Memory at scale is ticket 07's problem, now with a number.** A full USDG history is ~150M
  records; `twab.Replay` takes a slice, and 150M `Record`s is tens of GB in memory. `Each` streams
  the file so the incremental ledger can consume it without a slice; `Replay` for a single Epoch
  cannot, and at USDG's density a single `epoch` Recompute would need to.
- **Nightly CI runs the load test** on the public endpoint (`live` job). It takes ~5 minutes and
  prints the rate; a regression in the sync rate shows there.
