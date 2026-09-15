# 05 — Assertions, Verdicts and the CLI contract

Status: resolved
Type: task
Spec: ../spec.md §7, §8; ADR-0002
Blocked by: 04, 07

Implement `internal/report` and `cmd/nutz-verify`. Four Assertions reported as four separate lines: Root equality, `totals` equality, the cap `totals[i] <= funded[i] + carryIn[i]`, and `carryIn` against what the previous Epoch should have left. Four lines, not one boolean — a MISMATCH has to say *which* invariant broke, or a 3am dispute is unactionable. Full Carry chain back to deploy behind `--chain`, which **depends on ticket 07**: over the per-Epoch `twab.Replay` of ticket 02 the full chain is quadratic and measures in hours, not minutes.

Commands `epoch <id>`, `latest`, `sync`. Flags `--rpc` (repeatable), `--finality latest|safe|finalized` (default `safe`), `--fresh`, `--chain`, `--artifacts`, `--json`.

**Added by ticket 04:** `--rate <calls per second>`, mapped to `chain.Config.CallsPerSecond`. The default (`chain.DefaultCallsPerSecond`, 15) is sized for chain 4663's public endpoint, which budgets JSON-RPC calls at ~15–20/s; a keyed provider allows far more and a from-scratch sync on the default takes weeks (../spec.md §9). Print the rate in the run header alongside the endpoints. The `sync` command should print `cache.Synced` (records appended, any reorg) and `cache.Repaired()` when non-nil, and `cache.Mismatch` is the error whose remedy is `--fresh`; `cache.Options.Start` is the token's creation block, which this ticket pins. Pass `cache.SyncOptions.Progress` something that prints — at USDG's density an hour of history is ~20 minutes of sync, and silence for 20 minutes reads as a hang.

Exit `0` MATCH, `1` MISMATCH, `2` INDETERMINATE. **The load-bearing rule of this ticket is that `2` is never `0`.** INDETERMINATE covers: RPC error, no Root posted yet, the Epoch's end block not at the requested finality, endpoints disagreeing, cache unusable. The warm Signer signs only on `0`; anything that lets a failed check exit `0` silently converts the 2-of-3 into a 1-of-3.

Every run's header echoes chain id, Distributor address, pinned `DEV_WALLET`, finality level and endpoints — ADR-0002 requires the unverifiable inputs be shown rather than buried. `--json` carries an explicit schema version; it is the Signer's interface and changing it is a breaking change.

`--artifacts` diffs a published bundle against the Recompute and reports the first differing row. It must be structurally incapable of influencing the Recompute — take it as a separate argument compared after the fact, never as a source of block ranges or funded totals.

Tests: exit code for each Verdict; a deliberately corrupted artifact bundle reports the differing row and still exits on the Recompute's own Verdict; an Epoch below the requested finality exits `2`; two endpoints disagreeing exits `2`; a funded Epoch with no Root exits `0` (../spec.md §5). Golden-file tests on both output formats.

## Comments

**2026-09-14 — implemented.** `internal/report` and `cmd/nutz-verify`, tests for every case the
ticket lists, golden files for both output formats.

- **Four lines, three Verdicts, `2` is never `0`.** `report.Assess` produces the four
  Assertions in ADR-0002's order — `root`, `totals`, `cap`, `carryIn` — each `pass`, `fail` or
  `n/a`, and the Verdict from them. Every error on the way to a Recompute (RPC, finality,
  disagreement, Cache) becomes INDETERMINATE with the error as the reason, and the exit code is
  the Verdict's. A usage error is exit 2 as well.
- **`carryIn` is asserted against a Recompute, not a ledger.** The previous rooted Epoch is
  found by a backwards, widening search of the `RootPosted` stream (a Root is normally an hour
  back; the whole stream from deploy is a header per Root and does not fit the window), it is
  recomputed with its own posted `carryIn`, and its `carryOut` plus the funding of every Skipped
  Epoch between is what `e` must have been posted with. Spelled out in ../spec.md §7.
- **`--chain` is built over `twab.Replay`, so it is quadratic today.** Ticket 07 is still open;
  this ticket does not wait on it. The walk is linear in Epochs with one `alloc.Compute` per
  rooted Epoch, so ticket 07's incremental ledger drops in behind `walker.assess` without
  touching the CLI. Until then `--chain` over a long history is measured in hours (07's numbers).
- **The Distributor's streams open at a million blocks.** `chain.SparseLogRange`: `Roots` and
  `Exclusions` at 2,000-block pages over months of chain were thousands of requests per run.
  The dense `Transfer` stream is unchanged. Noted in ../spec.md §4.
- **`--artifacts` is compared after the Verdict, from `alloc.Result` alone.** It reads
  `allocations.json` (rows as spec §4.5's tuple or as the Cases' object; integers as strings or
  numbers) and, if present, `root.txt`, and reports the first differing row. It cannot reach the
  Recompute: `compareArtifacts` takes the result and a path, nothing else.
- **The deployment is pinned, and empty.** `cmd/nutz-verify/deployment.go` carries chain id,
  token, Distributor, `DEV_WALLET`, the token's creation block and the Distributor's deploy
  block. NUTZ has not launched, so only the chain id is filled in and the binary exits 2 with a
  message saying so; tests inject their own `Deployment`. The launch runbook fills it in.
- **The Cache is synced to the Epoch's end block** for `epoch` and `latest`, to the tip for
  `sync`, in 2,000-block chunks so progress on stderr moves every couple of minutes at USDG's
  density. `Repaired`, a reorg and the records appended are in the header.
- **`--json` is `nutz-verify-report/1`.** Amounts are decimal strings, hashes and addresses
  lowercase hex, endpoints by position and host. The golden files in `internal/report/testdata`
  are the contract; a shape change moves the version.

### Fixed in review

- **An unreadable `--artifacts` bundle turned the exit code into 2.** The bundle could not
  touch the numbers, but a missing or malformed one made the run INDETERMINATE and so could
  hide a MISMATCH behind an exit 2 — the opposite of "still exits on the Recompute's own
  Verdict". A bundle that cannot be read is now reported in its own section as not compared,
  and the Verdict is the Recompute's. Tested over a MISMATCH as well as a MATCH.
- `Assess` returns an `Assessment` rather than a triple; the Distributor's book is built once
  per run rather than once for `latest` and again for the Epoch; `root.txt` is validated as hex
  rather than as a signed integer; `endpoints` is never `null` in the JSON.
- **Status note.** This ticket is `Blocked by: 07` for `--chain`'s running time, and 07 is still
  open. The `--chain` contract (walk from deploy, every link asserted, every rooted Epoch
  reported) is implemented and tested; its cost is O(E·N) until 07 lands. Resolved on the
  contract, not on the cost.

Not done here: an anvil end-to-end run of the binary. The harness lives in `internal/chain`'s
tests and is not importable; the CLI tests drive the binary against a JSON-RPC fake that
replays the `funding-plus-carry` Case (its Root is OpenZeppelin's, not ours). A harness that
exec's the binary against the fork is worth a ticket once the deployment is pinned.
