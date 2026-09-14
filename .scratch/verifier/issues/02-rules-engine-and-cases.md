# 02 — Rules engine and the Case fixture format

Status: resolved
Type: task
Spec: ../spec.md §5; ADR-0003
Blocked by: 01

Implement `internal/twab` and `internal/alloc`: balance replay, TWAB, streak, multiplier, weight, per-token allocation, tree assembly. Pure functions over an in-memory transfer list — no RPC, no cache, no chain types. That separation is what lets ticket 03 be tested independently and what makes the Cases hermetic.

Encode the five §5 resolutions exactly: sum-then-divide-once for TWAB; the window closing at `3600(e+1)` rather than the end block; the same boundary in `streakDays`; weight carrying bps undivided; allocation over `funded + carryIn`; omission computed after all five tokens; and `W == 0` as a legitimate no-Root state.

Define the Case format and commit it, since the private indexer's CI consumes this directory (ADR-0003): `{ epochId, transfers[], excludedAt[], funded[5], carryIn[5] }` with `expected: { allocations[], totals[5], root }`. Allocations sorted by address ascending for stable diffs. Document the format in `testdata/cases/README.md` — it is an interface with an external consumer, not internal scaffolding.

Tests: one named Case per §5 resolution, each constructed so the wrong reading gives a visibly different answer — buy at minute 59; sell at minute 1; several transfers within one hour (this is the one that catches per-term flooring); a streak landing exactly on the 7-day and 30-day boundaries; `DEV_WALLET` held at 1.0x while its streak would otherwise promote it; an Excluded address with a large balance contributing zero; a Holder whose allocation floors to zero in four tokens but not the fifth (must stay in the tree); a funded Epoch with no eligible Holders. Plus engineering spec §7's determinism requirement: the same Case computed twice yields identical bytes.

## Comments

**2026-09-14 — implemented.**

`internal/twab` replays balances and reduces an Epoch to its eligible Holders; `internal/alloc`
turns those into Allocations, `totals`, Carry and the Root. Both are pure functions over an
in-memory transfer list: `twab` imports only the standard library, `alloc` only `twab` and
`merkle`, so `go.mod` and `go.sum` are untouched and the dependency tree stays at the two
modules ADR-0004 allows.

All seven §5 resolutions are encoded, each with a named Case built so the wrong reading gives a
visibly different answer, and each Case's `note` records the arithmetic and the competing
answer. Eleven Cases in `testdata/cases/`, documented in its README as an interface with an
external consumer. Expected Allocations, `totals`, `carryOut` and `totalWeight` are
hand-authored; only `expected.root` is generated, by OpenZeppelin through a new
`tooling/ pnpm gen:case-roots` (ADR-0005, extended below).

**Validation.** Every rule was mutation-tested against the Cases *alone*, since the Cases are
what the private indexer's CI runs: twelve mutations — per-term flooring, window closing at the
last transfer, streak measured to the last transfer, week and month thresholds off by one,
exclusion applying only from its own block, exclusions ignored, allocation over `funded` alone,
bps divided out of the weight early, no `DEV_WALLET` override, omission on the first zero token,
`W` recomputed over the survivors, `W == 0` as an error — and each is caught, by the Case
written for it. Per-term flooring is caught by `transfers-within-one-hour` and nothing else,
which is what that Case exists for. 100% statement coverage, `-race` clean, `go vet` and
`staticcheck` clean, root regeneration a no-op.

### Decisions worth knowing

- **The Case format is a documented superset of ADR-0003's shape.** `devWallet` is necessary —
  `DEV_WALLET` is an input to the rules and a hermetic Case cannot read a constant the spec has
  not pinned. `name` and `note` are enforced by the runner, because an unreviewable Case pins
  nothing. `expected.totalWeight` and `expected.carryOut` are diagnostics a consumer may ignore;
  a wrong `W` is the likeliest divergence between two implementations and is invisible in
  `totals` whenever it scales every Holder equally.
- **`excludedAt` carries the raw `ExcludedAppended` stream**, `{account, timestamp}`, not a
  resolved set — otherwise §5's whole-Epoch rule would sit outside what the Cases pin on either
  side.
- **ADR-0005 extended**, and the ADR amended to say so: `tooling/` now fills Case roots as well
  as the Merkle fixtures. It goes no further — generating the expected *rules* output would make
  the Cases a second rules implementation, free to drift from the normative one.
- **A zero-value `Transfer` resets a streak.** The literal reading of engineering spec §4.3
  ("whatever the destination") and the ungameable one. It is a reading rather than a §5
  resolution, so it is pinned by a Go test rather than a committed Case — see the follow-up
  below.
- **Validation paths no spec asks for** are kept deliberately: nil and negative `funded`,
  `carryIn` and transfer values, and a history that drives a balance negative. ADR-0004 leaves
  the decoding to us, and spec §7 turns on not conflating "could not check" with "checked and
  fine"; refusing beats computing confidently from an unusable input. `internal/merkle` sets the
  precedent.

### Fixed in review

- **`Replay` judged the balance sign after every log, not once per block.** A wallet that
  receives and forwards inside one block passes through a negative balance in one of the two log
  orders, so a valid Epoch became an error — against spec §7's "`2` is not `0`" — while the
  README promised the indexer that log order within a block is not part of the input. The sign is
  now judged once every transfer sharing a timestamp has been applied, which still catches an
  incomplete history at each block boundary. `buy-at-minute-59` now routes Alice's buy through a
  same-block relay that nets to zero and never reaches the tree, so a Case exercises the property
  rather than only a unit test; the expected values and the root are unchanged.
- **`window-closes-at-the-boundary` overclaimed.** A Case carries timestamps, never blocks, so it
  pins "the last transfer", not "the last block". Its note and the README now say so, and name
  the other half of the resolution as the chain reader's.
- The README claimed adding a field was safe, which `DisallowUnknownFields` contradicts; it now
  says a new field must land in the Go runner in the same change, and why that check is worth
  keeping.

### Follow-ups, not done here

- **`Replay` re-walks the whole transfer history for every Epoch.** Correct and fine for the
  Cases, but a from-scratch sync over USDG-scale history will feel it. The seam takes pre-seeded
  opening balances without any change to the Case format — for tickets 03 and 04, alongside the
  load test of spec §10.
- **The end-block half of §5's window resolution is unpinned here.** No hermetic Case can express
  which blocks a Recompute reads; `internal/chain` needs its own test that the end block is the
  last with `timestamp < 3600(e+1)`.
- **Ask `nutz-contracts` to confirm the zero-value-transfer reading.** If they agree, it earns a
  committed Case and becomes normative for the indexer; until then it is only a Go test.
- **The exclusion set hash** of engineering spec §4.5, `keccak256(abi.encodePacked(set))`, is not
  implemented: it is a cross-check against a published artifact, so it belongs with `--artifacts`
  in ticket 05.
- Carried over from ticket 01 and still open: no `LICENSE` at the repo root though spec §3 says
  MIT; `go.mod` says `go 1.24.0` rather than spec §3's conservative directive; and nothing
  exports the fixture set to the Solidity and TypeScript implementations ADR-0003 names.
