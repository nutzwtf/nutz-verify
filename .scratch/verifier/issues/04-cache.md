# 04 — Append-only Cache with CRC, truncate-on-open and reorg truncation

Status: ready-for-agent
Type: task
Spec: ../spec.md §9; ADR-0004
Blocked by: 03

Implement `internal/cache`: fixed 124-byte stride, `[120-byte payload][4-byte CRC32C]`, payload `(blockNumber, blockHash, timestamp, from, to, value)`, under `${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/`. Fixed file header with magic, format version, chain id and contract address; a mismatch refuses to open and tells the user to `--fresh` rather than mixing caches. No sidecar manifest — the CRC chain plus each record's block number make the log self-describing.

Truncate-to-last-valid-record on open. Batched fsync on a checkpoint interval, never per record (measured ~6.7× penalty). Reorg: binary-search the fixed stride for the fork block, `Truncate(i * 124)`, fsync. The durability requirement is deliberately weak — the log caches public data, so anything lost is refetched; we must detect damage, not survive it.

Tests: a truncated tail mid-record self-heals on open and resumes from the last good record; a flipped bit is caught by CRC and names the block number rather than failing opaquely; a header from a different chain id refuses to open; reorg truncation lands on the right offset and replay afterwards is byte-identical to a `--fresh` sync over the same range.

Then the load test, which is the real point of this ticket: point ingestion at USDG history on chain 4663 and record how long a from-scratch sync actually takes. At ~100 ms blocks (~864k blocks/day) the risk here is volume, not logic, and a Verifier that cannot finish inside a 30-minute Dispute window is decorative. Write the measured numbers into ../spec.md §9.
