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
One of the four things a MATCH commits to: Root equality, `totals` equality, the per-token cap, and `carryIn`. Reported as four lines, so a MISMATCH names which one broke.
_Avoid_: check, test (that is a Case)

**Cache**:
The Verifier's own append-only record of NUTZ transfers and block timestamps, built from RPC and owned by whoever runs the binary. It is never shared, never published, and never an input anyone else supplies.
_Avoid_: index, database, snapshot (a snapshot would be something we hand out, which is the thing the Verifier exists to avoid needing)

**Finality level**:
Which chain tip a Recompute reads history at — `latest`, `safe` or `finalized`. Default `safe`. An Epoch whose end block has not reached the requested level is INDETERMINATE, not MATCH.
_Avoid_: confirmations, depth

**Case**:
One hermetic allocation fixture: inputs and expected Allocations, `totals` and Root, with no RPC. The unit in which a rule change lands, and the artifact the private indexer's CI consumes.
_Avoid_: fixture (ambiguous with the Merkle fixtures inherited from `nutz-contracts`), golden file, test vector

**Cross-check**:
Running a Recompute against several independent RPC endpoints and treating disagreement as INDETERMINATE.
_Avoid_: consensus, quorum
