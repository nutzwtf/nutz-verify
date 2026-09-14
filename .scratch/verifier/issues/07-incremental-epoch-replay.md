# 07 — Incremental Epoch replay, so `--chain` is linear rather than quadratic

Status: ready-for-agent
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
