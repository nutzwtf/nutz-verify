---
status: accepted
date: 2026-09-14
---

# The Verifier reads only the chain, and MATCH asserts four things

Every input to a Recompute comes from RPC: NUTZ `Transfer` logs, block timestamps, the Excluded set, `funded` and `carryIn` from the Distributor, and the posted Root. **Nothing we publish is ever an input.** Artifacts (spec §4.5) are accepted only under `--artifacts` and only as a *comparison target* — "your published `allocations.json` disagrees with what I computed at row N". A Verifier that reads our `input.json` for the block range would let a malicious indexer choose both the evidence and the test; everything in that file is derivable from chain, so there is no reason to accept it.

**MATCH** is not "the Root matched". It asserts, as four separately reported lines:

1. the recomputed Root equals the posted Root;
2. the recomputed `totals[5]` equal the posted `totals[5]`;
3. the cap held — `totals[i] <= funded[i] + carryIn[i]`, the invariant that bounds a malicious Root to one Epoch;
4. `carryIn[5]` equals what the previous Epoch should have left behind.

The full Carry chain back to deploy sits behind `--chain`, because it needs history the 30-minute Dispute window does not afford.

## Consequences

- **Verdicts are three-valued, and the third one is load-bearing.** Exit `0` MATCH, `1` MISMATCH, `2` INDETERMINATE (RPC error, no Root posted yet, Epoch not at the requested finality, endpoints disagree, cache unusable). **`2` is not `0`.** The warm Signer signs only on `0`. Conflating "could not check" with "checked and fine" would quietly turn the 2-of-3 into a 1-of-3 during exactly the RPC outage an attacker would choose.
- **The Verifier trusts its RPC endpoint, and cannot not.** Block-hash chaining does not fix this — there is no independent anchor to chain back to. Mitigation: `--rpc` is repeatable and disagreement between endpoints is INDETERMINATE, which turns "trust your provider" into "trust that two providers are not colluding". A local node is the only true fix; the README names it as the paranoid path and does not pretend otherwise. **A verifier that oversells what it proves is worse than a narrow one.**
- Two inputs are not on chain and are printed in every run's header rather than buried: the pinned `DEV_WALLET` (spec §3.5 marks it indexer-only) and the Distributor address, alongside chain id, finality level and endpoints.
- The Excluded set must be taken **as of the Epoch**, not read live: `excluded()` returns today's list. It is every `account` of an `ExcludedAppended` log with block timestamp `< 3600(e+1)`, an entry applying to the whole Epoch containing its block. The log stream alone suffices because the Distributor's constructor emits `ExcludedAppended` for every base entry — a change `nutz-contracts` made on 2026-09-14 in response to this repo flagging that the base list was otherwise unrecoverable from logs.
