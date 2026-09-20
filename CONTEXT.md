# NUTZ Verifier

The open-source program that recomputes any Epoch or Acorn Draw from public chain data and reports MATCH or MISMATCH against the on-chain Root.

**Shared vocabulary lives in [`nutz-contracts/CONTEXT.md`](https://github.com/nutzwtf/nutz-contracts/blob/main/CONTEXT.md)** — Epoch, TWAB, Streak, Multiplier, Weight, Allocation, Root, Dispute window, Final, Void, Skipped, Carry, Claim, Push, Acorn Draw, Ticket, Tier, Seed, Signer, Keeper, Excluded address and the rest. Use those terms as defined there and do not restate them here: a duplicated document drifts, and this repo has already watched the engineering spec do exactly that.

This file defines only what is local to the Verifier.

## Language

**Recompute**:
One run of the Verifier over a single Epoch or Acorn Draw: read the chain, rebuild the Allocations, rebuild the Root, compare.
_Avoid_: run, check, audit (that would be a claim about swap pricing, which the Verifier does not make)

**Verdict**:
The outcome of a Recompute, always one of three: **MATCH**, **MISMATCH**, **INDETERMINATE**. Never two — "could not check" is not "checked and fine", and the distinction is what keeps the warm Signer from degrading the 2-of-3.
_Avoid_: pass/fail, ok, valid

**Assertion**:
One of the four things a MATCH commits to: Root equality, `totals` equality, the per-token cap, and `carryIn`. Reported as four lines, so a MISMATCH names which one broke. A Recompute given an Expectation has a fifth: whether the Expectation equals the posted Root.
_Avoid_: check, test (that is a Case)

**Expectation**:
A Root someone expects for an Epoch, given to a Recompute with `--expect`. With no Root posted it is what the Root Assertion compares against, so the Recompute can reach a Verdict before the Root is up; with one posted it is checked against it. Shown in the report, never an input to the Recompute itself.
_Avoid_: proposal, hint, candidate, claim (a Claim is a Holder's, in `nutz-contracts`)

**Cache**:
The Verifier's own append-only record of NUTZ transfers and block timestamps, built from RPC and owned by whoever runs the binary. It is never shared, never published, and never an input anyone else supplies.
_Avoid_: index, database, snapshot (a snapshot would be something we hand out, which is the thing the Verifier exists to avoid needing)

**Finality level**:
Which chain tip a Recompute reads history at — `latest`, `safe` or `finalized`. Default `safe`. An Epoch whose end block has not reached the requested level is INDETERMINATE, not MATCH.
_Avoid_: confirmations, depth

**Case**:
One hermetic allocation fixture: inputs and expected Allocations, `totals` and Root, with no RPC. The unit in which a rule change lands; the private indexer picks it up by pinning the tag whose CI ran it, since it runs the same Engine.
_Avoid_: fixture (ambiguous with the Merkle fixtures inherited from `nutz-contracts`), golden file, test vector

**Cross-check**:
Running a Recompute against several independent RPC endpoints and treating disagreement as INDETERMINATE.
_Avoid_: consensus, quorum

**Engine**:
The one code path that reads the chain and rebuilds an Epoch's Allocations, `totals`, Carry and Root. A Recompute is the Engine's output compared against the chain; the private indexer's Bundle is the Engine's output written out.
_Avoid_: library, facade, core (implementation words for the same thing)
