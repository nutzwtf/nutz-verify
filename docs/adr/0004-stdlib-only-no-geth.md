---
status: accepted
date: 2026-09-14
---

# The dependency tree is part of the product: stdlib only, no go-ethereum, no embedded KV store

The Verifier's value is that a hostile stranger reads the source and runs the binary, so `go.sum` is a user-facing artifact. We depend on the standard library plus **`golang.org/x/crypto/sha3`** and nothing else: ABI decoding and Ethereum JSON-RPC are hand-rolled, and the Cache is a flat append-only file rather than an embedded key-value store. Adding a dependency is an ADR-level decision.

**This is a deliberate deviation from prevailing practice, not an appeal to it.** The survey in [`docs/research/go-deps-and-storage.md`](../research/go-deps-and-storage.md) looked for small Go verifier tools that hand-roll JSON-RPC and **found none** — `0xKiwi/go-merkle-distributor`, a merkle-airdrop generator of almost exactly our shape, has a two-line `go.mod`, one line of which is go-ethereum. A future reader will assume we simply did not know that; we did.

## Why each piece

- **Keccak-256 needs `x/crypto/sha3`.** Stdlib `crypto/sha3` (Go 1.24+) is FIPS-202 only; Ethereum's legacy `0x01` padding exists in the tree but only in the unimportable `crypto/internal/fips140/sha3`, and the proposal to export it ([golang/go#75486](https://github.com/golang/go/issues/75486)) is open with no decision — do not plan around it. Since x/crypto **v0.44.0** the package is a thin stdlib wrapper keeping only legacy Keccak locally: measured **+16 KB, 2 modules, 679 lines to audit**. Cheapest dependency in the analysis and squarely on-mission.
- **ABI and RPC are hand-rolled** because the surface is four shapes known at compile time — `Transfer` (2 topics + 1 word), `RootPosted` (2 topics + 11 static words), `ExcludedAppended` (1 topic), and one dynamic `address[]` return. geth's `accounts/abi` is a runtime decoder for arbitrary contract ABIs; standalone it measures 11 modules / 3,876 KB (cheaper than folklore, because module-graph pruning collapses its 79-line `go.mod`), but paying that for generality used four times is backwards here. `ethclient` — the part we would actually want — is **103 modules / 8,440 KB**, including 17 OpenTelemetry packages and 12 gnark-crypto packages that `eth_getLogs` has no use for. geth is also **LGPL-3.0** against our MIT static binary; the research found no authoritative ruling on LGPL and static Go linking, so treat that as a tiebreaker, not a reason.
- **The Cache is a flat file** because the workload is write-once-in-order, replay-sequentially, truncate-at-a-reorg: no point lookups, no range queries, no secondary indexes, no concurrent writers. The decisive argument is not throughput but bbolt's own README — *"Bolt cannot truncate data files and return free pages back to the disk"* — and truncation is the only mutation this workload has. Weight confirms it: Δ over an empty binary is **+112 KB** flat file, +448 KB bbolt, +4.2 MB `modernc.org/sqlite`, +6.7 MB badger, **+11.8 MB / 46 modules** pebble.

## Rejected, with reasons worth remembering

- **cgo is not the discriminator.** All five storage candidates, `modernc.org/sqlite` included, build cgo-free to all four targets — verified by cross-compiling, not assumed. Reject them on weight, not portability.
- **`umbracle/ethgo`**: dead, last commit 2024-11-01. **`lmittmann/w3`**: not a geth alternative — it *depends* on geth and measures larger than raw `ethclient`. **`defiweb/go-eth`**: the only genuinely geth-free maintained option (20 modules), but it forces a `go 1.26.0` toolchain.
- **geth's precedent does not transfer.** It defaults to Pebble (since v1.12.0) and erigon uses MDBX via cgo; both are archival full nodes doing random-access, continuously-mutated, concurrently-read state. The transferable lesson is erigon's negative one: a C-backed store is a one-way door on the static-binary constraint.

## Consequences

- **We own the decoding bugs.** Tolerable only because the anvil harness (ticket 03) exercises every decoder against a real node before launch.
- The Cache needs its own correctness work rather than inheriting a store's: fixed 124-byte stride (`[120-byte payload][4-byte CRC32C]`), truncate-to-last-valid-record on open, batched fsync (per-record costs ~6.7×), and a fixed file header carrying magic, format version, chain id and contract address so a file from the wrong chain is rejected loudly. No sidecar manifest: the CRC chain plus the per-record block number make the log self-describing. Durability requirements are unusually weak — the log is a cache of public data, so we must *detect* damage, not survive it.
- Benchmarks behind these numbers are single-machine and single-run; directions are reliable, precise ratios are not. See the research note's "Unverified / open questions".
