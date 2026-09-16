---
status: accepted, amended by ADR-0006 (the implementation-independence argument no longer holds; the static-binary one does)
date: 2026-09-14
---

# The verifier is written in Go, not the TypeScript the engineering spec names

Engineering spec §1 lists `nutz-verify` (C4b) as a "TypeScript CLI" and §12 P0 puts the allocation math in it. We are building it in **Go** instead, for two reasons that are specific to this artifact rather than general preferences: a **single static binary** that a hostile stranger downloads and runs inside a 30-minute Dispute window, with no Node toolchain, is the difference between a Verifier that exists and one that gets run; and **genuine implementation independence** from the TypeScript indexer, without which §1's own control ("CI runs both implementations against the same fixtures and fails on any root mismatch") and §5's warm Signer that "only signs on MATCH" are two codebases sharing a language, a bignum, a keccak and a Merkle library, and therefore correlating their bugs.

## Considered options

- **TypeScript, as specified**: rejected. It inherits `@openzeppelin/merkle-tree` for free, which is the one real advantage, but it converges on the same `viem` + OZ stack the private indexer will use. Two implementations that import the same two libraries do not independently confirm a Root.
- **Rust**: not seriously considered. Same static-binary property, worse fit — no in-house familiarity, and nothing in the workload needs it.

## Consequences

- **We implement OpenZeppelin `StandardMerkleTree` ourselves** (~200 lines). Nothing is inherited. This is the single largest correctness risk in the repo and is why ticket 01 builds it first, pinned against `nutz-contracts/test/fixtures/claims.json` (root `0x88b44d7a37b5a4d381e091853e53d8c9e7964de7e1a41037e7e41b0062877591`). The leaf shape helps: `(uint256, address, uint256[5])` is entirely static — 7 words, 224 bytes, no offsets, no dynamic tails — so there is no general ABI encoder to write. The landmine is OZ's `sortLeaves: true` default, which reorders leaves by hash before building; getting it wrong yields a valid-looking tree with a wrong Root.
- **Engineering spec §1 and §12 are wrong until patched.** They are read from `nutz-contracts`, not forked here.
- The warm Signer service (spec §3.2, §5 step 3) lives in a TypeScript monorepo and therefore **executes the published binary** rather than importing a library. See ADR-0002 for what it gates on.
