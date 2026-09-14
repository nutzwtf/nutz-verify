# 04 — Append-only Cache with CRC, truncate-on-open and reorg truncation

Status: ready-for-agent
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
