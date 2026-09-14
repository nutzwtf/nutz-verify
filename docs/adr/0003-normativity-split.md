---
status: accepted
date: 2026-09-14
---

# Normativity splits: the rules are ours, the tree shape is the Distributor's

Engineering spec §1 calls `nutz-verify` "the normative spec for §4", but that cannot extend to the whole Root. A Root has two halves — *which numbers* each Holder gets, and *how those numbers are hashed into a tree* — and the Distributor already fixes the second, verifying proofs in one specific shape pinned by `nutz-contracts` ADR-0001. So:

| | Authority | If they disagree |
|---|---|---|
| Allocation rules (spec §4.1–4.5) | **this repo** | the indexer is the bug |
| Leaf hashing and tree shape | **the Distributor** + OZ `StandardMerkleTree` v1.0.8 | **the Verifier** is the bug |

Getting this backwards in either direction is costly: if the tree were ours, we would ship a Root the contract cannot verify; if the rules were the indexer's, "normative" would be a slogan and the private implementation would define correctness while the public one chased it.

## Consequences

- **This repo owns the allocation fixtures**, which is what makes the left-hand row operational rather than aspirational. Each Case is hermetic — no RPC — carrying `{ epochId, transfers[], excludedAt[], funded[5], carryIn[5] }` and `expected: { allocations[], totals[5], root }`. Go runs them as table tests; the private indexer's CI consumes the same directory and fails on divergence. This is the concrete form of spec §1's "CI runs both implementations against the same fixtures", and it doubles as spec §7's "deterministic root across two machines".
- **A rule change lands here first, as a Case.** That is the only thing "normative" can mean in practice.
- Spec §7 files the golden-file tests under "Indexer", i.e. the private repo. That contradicts §1 and §12 P0 and is wrong; the tests live here.
- The tree port is pinned against `nutz-contracts/test/fixtures/claims.json`, and that fixture set grows here (odd leaf counts, single leaf, duplicate amounts) for all three implementations — Solidity/murky, TypeScript/OZ, Go.
