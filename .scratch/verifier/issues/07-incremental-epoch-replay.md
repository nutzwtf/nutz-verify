# 07 — Incremental Epoch replay, so `--chain` is linear rather than quadratic

Status: resolved
Type: task
Spec: ../spec.md §5, §7, §9
Blocked by: 04

`twab.Replay` takes the whole transfer history and reduces **one** Epoch from it. That is the
right shape for a single Recompute and the reason the Cases are hermetic (ticket 02), but it
makes spec §7's `--chain` — "the full Carry chain back to deploy" — quadratic: every Epoch
re-walks history from the token's creation block.

## Measured, not assumed

`Replay` over one Epoch, 100k holders, on an i9-9900K:

| History | Wall time | Allocated |
|---|---|---|
| 100k transfers | 0.14 s | 53 MB |
| 1M transfers | 0.53 s | 103 MB |
| 10M transfers | 3.8 s | 607 MB |

Linear in the history above a fixed ~0.13 s holder-map cost, so:

- **A single Epoch is fine and needs nothing.** `epoch <id>` and `latest` — the Dispute-window
  default — cost about 4 seconds against a 10M-transfer history, inside a 30-minute window by
  three orders of magnitude.
- **`--chain` is not.** Walking E Epochs each over a history growing to N costs about
  `E × cost(N) / 2`. At 10M transfers over 10,000 Epochs that is **≈ 5.3 hours** (extrapolated
  from the table, not measured end to end). Ticket 04's load test against USDG history on 4663
  will give the real N and E; treat the shape as the finding, not the precise figure.

## What to build

An incremental ledger advanced Epoch by Epoch, emitting Holders at each boundary — O(N) for the
entire chain instead of O(E·N), plus O(A) per boundary. Every piece of state carries forward
cleanly, which is why this is a refactor and not a redesign:

- **balances** — carried forward by construction.
- **`firstBuyAt`** — set once and never changes.
- **`lastSellAt`** — monotone increasing.
- **the Excluded set** — monotone; engineering spec §3.5 makes entries "Never removable".

The 900k allocations per Replay are mostly `standing` structs and `big.Int` churn that an
incremental ledger reuses, so expect the memory win to be larger than the time win.

**Do not change the Case format.** The Cases are single-Epoch by construction and are an
interface with the private indexer's CI (ADR-0003). `Replay` stays as it is; the incremental
path is an addition beside it.

Tests: the incremental ledger, advanced from Epoch 0 through each Case's Epoch, must produce
**byte-identical** Holders to `Replay` over the same history — run it across every Case in
`testdata/cases/`, since agreeing with `Replay` is the entire correctness claim. Plus a
benchmark showing `--chain` over the load test's real history completes in minutes.

## Comments

### Resolved 2026-09-14

**What was built.** `twab.Ledger`: `NewLedger(transfers, exclusions)` sorts the history once,
and `Advance(epochID)` closes that Epoch and returns its Holders — what `Replay(epochID, …)`
returns over the same history, in the same order. Epochs may be skipped, never revisited. Each
`Advance` restarts every active standing's accrual at the window's opening, applies the
transfers up to the boundary with Replay's own `apply`/`settle`, and reduces the active list
with Replay's own `holders`. Balances, `firstBuyAt`, `lastSellAt` carry forward in the map;
the Excluded set is `ExcludedAsOf` per Epoch, the same call Replay makes. A wallet that holds
nothing at a boundary leaves the active list and rejoins, at the current window's opening, the
next time a transfer touches it — so a boundary costs the Holders it emits, not every wallet
the token ever had.

`alloc.Allocate(params, holders)` is `Compute` over Holders already replayed; `Compute` is now
`Replay` followed by it. The CLI's `walker` builds one Ledger per run and every Recompute on
every path — `epoch`, `latest`, with and without `--chain`, with and without a previous Root —
advances it in ascending order, so a run reads the history once however many links it asserts.

**`Replay` is unchanged in behaviour, not in text.** It shares `standing`, `at`, `accrueTo`,
`settle` and `holders` with the Ledger; the Cases pin that nothing moved. Two internals did
change: `holders` reduces an explicit active list rather than the map (in Replay every address
is listed, so the set is the same), and `accrueTo` multiplies into one reused `big.Int` rather
than allocating per transfer — the churn this ticket named.

**Tests.** `TestCases_LedgerAgreesWithReplay` advances the Ledger from Epoch 0 through each
Case's Epoch, comparing Holders with `Replay` at every Epoch on the way and the full
`Allocate` result with `Compute` at the end, across every Case in `testdata/cases/`. In
`internal/twab`, a hand-written twelve-Epoch history exercises idle stretches, a mid-way
Exclusion, a burn, a zero-value transfer and a same-block forward; 24 seeded random histories
of 400 transfers over 40 Epochs compare at every Epoch; skipping ahead, an idle wallet
rejoining before the target window, going backwards and an overspent history each have a
named test. Breaking either half of `open` — the accrual reset, or the unlisting — fails
four tests.

**Measured** (`BenchmarkChain`, i9-9900K, `-benchtime 1x`). "Replay × E" is one Replay of the
last Epoch times E, the walk's cost before this ticket, give or take a half; where the walk
was affordable it was run in full and is in brackets.

| History | Holders | Epochs | Ledger, whole chain | Replay × E |
|---|---|---|---|---|
| 100k transfers | 10k | 100 | 0.26 s, 75 MB | 2.3 s [1.4 s measured] |
| 1M | 100k | 1,000 | 31 s, 4.1 GB | 380 s |
| 10M | 100k | 10,000 | 708 s (11.8 min), 84 GB allocated over the run | 26,800 s (7.4 h) |
| one day at USDG density: 24 × 133k | 100k | 24 | 3.9 s, 0.44 GB | 48 s [27 s measured] |

Above 1M transfers the Ledger's time is almost all the boundaries — ~70 ms to emit 100k
Holders, each a fresh `big.Int` the caller keeps, under a GC marking a gigabyte of live
history — not the transfers; that O(E·A) term is the output itself, and `alloc.Allocate` plus
a 100k-leaf tree per rooted Epoch costs more again. The shape the ticket predicted holds: the
walk is linear in the history, and at the ticket's 10M-transfer, 10,000-Epoch case — a year
of hourly Epochs with 100k Holders in every one — it completes in twelve minutes rather than
hours. The single-Replay figure came out at 5.4 s against the ticket's 3.8 s because the
large shapes ran while the day-at-USDG-density shape and the race suite were also running;
the ratio, not the absolute, is the finding.

**On "the load test's real history".** Ticket 04's load test syncs 5,000 blocks of USDG — about
16 minutes of chain, less than one Epoch — into a throwaway Cache, so no walk over it spans a
boundary. The last row is the benchmark's stand-in: a day of Epochs at the density that test
measured (7 logs a block, ~19,000 blocks an hour, spec §9), which is the real N per Epoch. The
real E at launch is NUTZ's, not USDG's, and the measurement to redo then is the density.

### Fixed in review

- The CLI's `assess` had a `chain.Ledger` local (the Distributor's funding book) beside a
  `twab.Ledger` field; the walker's field is now `history`.
- `NewLedger` repeated Replay's clone-and-stable-sort; both call `sortedByTimestamp`.
- The Ledger tracked "last closed, if any" as a `uint64` plus a `bool`; it is one `floor`.
- **Declined:** renaming `twab.Ledger` (the ticket's own word, and the collision was local);
  sharing the Holder fingerprint helper between `twab_test` and `alloc_test` (an exported
  test-support package for eight lines); an `Inputs` type so `Allocate` need not ignore
  `Params.Transfers` (it would touch every `alloc.Params` literal in the tree, and the doc
  says what is read); moving `open` into `replay.go` (it is the Ledger's boundary rule, and
  Replay never crosses one).

### Follow-ups, not done here

- **Streaming.** `NewLedger` takes the whole history as a slice, like `Replay`, and clones it
  to sort. Ticket 04 noted `cache.Each` streams the file so a ledger could consume it without
  a slice; at 150M records that is the difference between tens of GB and a map of Holders.
  The Cache is already in block order, so a push-style `Feed`/`Advance` that refuses
  out-of-order timestamps is the natural next shape. Not needed for NUTZ's density, and not
  this ticket.
- Ticket 05's status note said `--chain` was O(E·N) "until 07 lands". It has.
