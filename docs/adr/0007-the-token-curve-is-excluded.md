---
status: accepted
date: 2026-09-27
---

# The Token's Pons bonding curve is Excluded in every Epoch, by a Verifier rule

Before graduation the NUTZ nobody has bought yet sits in a Pons bonding curve. The factory creates it at the Launch, one per token, and on day one it holds almost the whole supply of 1e9. Rewards are pro rata to TWAB and an address counts unless it is Excluded, and the Excluded set of an Epoch is rebuilt from the Distributor's `ExcludedAppended` logs: the base list at construction, then appends through a 2-of-3 proposal with a 48-hour timelock, each applying from the Epoch it lands in (engineering spec §4.2). The deploy comes two days before the Launch, so the base list cannot name the curve, and an append after the Launch executes two days later. In between, the curve is the largest Holder and most of two days of fees go to an address that can never claim.

**Decision.** The Engine treats the curve of the pinned Token as Excluded in every Epoch. It reads `factory.getLaunchedToken(Token).curve` once per run through the cross-checked call path, at the tip, and adds it to the exclusion stream as if an `ExcludedAppended` log for it had landed at timestamp 0. The Pons factory address joins the Deployment; `Check` refuses a Pin without it. A record with `exists == false`, or a zero curve, is an error and never an empty set. The exclusion set hash of §4.5 is unchanged in form; the curve is one more member from the first Epoch, and the indexer, the warm Signer and any stranger running the Verifier compute the same hash because they run the same rule.

The Dev wallet is the precedent: an indexer-side rule the contracts never see, carried by the Verifier because a Recompute must know it to agree with the indexer (ADR-0002 shows it in the header). The curve is the same kind of fact with one difference: it is derived from public chain state, not pinned, so no release names it and nothing unverifiable is added to the header.

## Alternatives

1. **Keep the on-chain list as the only source.** Propose the curve's exclusion right after the Launch and hold keeper bind until it executes 48 hours later, so no Epoch is funded while the curve counts. Nothing is wasted, but the first drop lands two days after the Launch, on a day whose whole point is the first drop.
2. **Launch first, deploy second.** Launch with the Dev wallet as fee recipient, deploy with the now-known curve in the base list, then transfer the recipient to the Converter, which the factory allows instantly. No window and no rule, but it reverses the two-day calendar and the Launch transaction no longer names the Converter.

The rule keeps the day as planned, wastes nothing, and lands in a release that has to be cut anyway for the Pin.

## Consequences

- A Verifier release with the rule disagrees with one without it about every pre-graduation Root. That is what makes this hard to reverse, and why it ships in the same release as the Pin, before the first Root is computed.
- The rule is safe after graduation: `graduate()` runs a final sweep and moves the supply into the v4 pool, so the curve holds nothing and excluding it changes no allocation. It costs one `eth_call` per run and stays forever.
- The v4 PoolManager is the opposite case, global and known today, so it belongs in the Distributor's base list and the runbook's "append the pool at graduation" step (§10 step 5) disappears. That is a `robinhood.json` change in nutz-contracts, not a Verifier one.
- The anvil harness stands in nutz-contracts' `MockPonsFactory` for the real factory, since its stand-in token was never launched through the real one. The Cases gain an optional `curve` field, applied by the hermetic runner as an exclusion at timestamp 0, so the rule is pinned where every other allocation rule is.
- Nothing else changes: the Distributor and Converter, the Bundle's four files and their fields, and nutz-platform's code, which links the release and gets the rule with it.
