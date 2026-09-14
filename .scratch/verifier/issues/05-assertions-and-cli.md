# 05 — Assertions, Verdicts and the CLI contract

Status: ready-for-agent
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
