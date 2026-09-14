# nutz-verify — component spec (scope A: Epochs)

Status: grilled 2026-09-14 (Q1–Q20), ready to build
Source: engineering spec §4 and §7, ADR-0001 through ADR-0004, and the grilling of 2026-09-14. **Where this file and the engineering spec disagree, this file wins**; the engineering spec gets patched when the component ships.
Spec location: the engineering spec is read from **`nutz-contracts/docs/spec/engineering-spec.md`**, not forked here. The stale v0.4 copy that used to sit in this repo's `docs/spec/` was deleted on 2026-09-14; do not reintroduce one.
Glossary: `CONTEXT.md` here for Verifier-local terms; `nutz-contracts/CONTEXT.md` for everything shared.

## 1. Purpose

Recompute any Epoch from public chain data and report a Verdict against the on-chain Root. Two audiences, one binary: a hostile stranger inside the 30-minute Dispute window, and the warm Signer of engineering spec §3.2 that signs hourly only on MATCH. The second executes the same published artifact the first downloads — checksum-pinned, not merely the same source — so "the code gating our signature is the code you can audit" is a checkable claim.

## 2. Decisions already made

- **ADR-0001** — Go, not the TypeScript the spec names. We implement `StandardMerkleTree` ourselves.
- **ADR-0002** — RPC-only; artifacts are comparison targets, never inputs. MATCH asserts four things. Verdicts are three-valued.
- **ADR-0003** — rules are normative here, tree shape is normative in the Distributor.
- **ADR-0004** — stdlib plus `golang.org/x/crypto/sha3`; no go-ethereum, no embedded KV store.

## 3. Layout

```
cmd/nutz-verify/              the CLI
internal/chain/               JSON-RPC, log decoding, excluded-set reconstruction
internal/cache/               append-only log, CRC32C, truncate-on-open, reorg truncate
internal/twab/                balance replay and time-weighted average balance
internal/alloc/              streaks, multipliers, weights, per-token allocation
internal/merkle/              StandardMerkleTree port
internal/report/              Verdicts, four Assertions, human and --json output
testdata/cases/               hermetic Cases (ADR-0003); consumed by the private indexer's CI too
testdata/merkle/              tree fixtures, seeded from nutz-contracts/test/fixtures/claims.json
```

Module `github.com/nutzwtf/nutz-verify`. Everything under `internal/` — the Signer execs the binary, so there is no public Go API to keep stable. Conservative `go` directive so a distro toolchain builds it. MIT.

## 4. Inputs

All from RPC. Nothing we publish is an input.

| Input | Source |
|---|---|
| NUTZ balances over time | `Transfer(from,to,value)` logs from the token's creation block |
| Block timestamps | block headers |
| Excluded set **as of the Epoch** | every `account` of an `ExcludedAppended` log with block timestamp `< 3600(e+1)` — the log stream alone, no view call |
| `funded[5]`, `carryIn[5]`, `totals[5]`, Root | `ledger()` and `RootPosted` |
| `DEV_WALLET` | pinned constant, echoed in every run's header |

`--artifacts` additionally accepts a published bundle and diffs it against the Recompute. It can never influence one.

### What the endpoints actually limit — measured 2026-09-14, chain 4663

Engineering spec §4.1 calls 2,000 blocks "the public-RPC cap". **It is not**, and this file wins:
measured against `https://rpc.mainnet.chain.robinhood.com`, a range of a *million* blocks is
accepted, and what the endpoint refuses is **10,000 results** — `logs matched by query exceeds
limit of 10000`. Patch §4.1 when this component ships.

The distinction is the difference between a working Verifier and one that fails in production
only: a range cap is a constant you can page under, while a result cap depends on how busy the
token is. USDG runs at **4.9–5.4 `Transfer` logs per block**, so a 2,000-block page is ~9,750–13,500
logs — straddling the cap. A fixed page would pass every test and then fail intermittently against
the endpoint a hostile stranger is most likely to be using. `internal/chain` therefore opens at
2,000 blocks and **halves on refusal**, which needs no constant to be right.

Two further properties of that endpoint, both relevant to §9 and §10:

- **It throttles.** Sustained querying earns `429`, and pushing harder earns a short `403`. Both
  pass. The client retries `429` and `5xx` with bounded backoff, honouring `Retry-After`; anything
  still refusing after five attempts is INDETERMINATE, because a wedged endpoint has to fail inside
  the Dispute window rather than hang in it.
- **The block-timestamp join is the real cost, not the logs.** A log carries no timestamp, so every
  block that produced one needs its header. At 5.4 logs per block essentially every block qualifies,
  and at ~100 ms blocks that is **~864,000 header requests per day of history**. Batching or the
  Cache has to absorb that; it is ticket 04's load test to say which.

Cross-checking two genuinely independent providers works: the public endpoint and a Chainstack
archive return byte-identical logs over the same range, so the canonical comparison survives real
differences in formatting and ordering. ADR-0002's mitigation is usable, not just stated.

## 5. Rules (engineering spec §4, ambiguities resolved)

Integer arithmetic throughout. These five resolutions are the difference between two implementations that agree and two that do not; each is a named Case in `testdata/cases/`.

- **Epoch window.** Epoch `e` is `[3600e, 3600(e+1))`. The end block is the last block with `timestamp < 3600(e+1)`.
- **TWAB — sum first, divide once.** `TWAB(a,e) = floor( Σ balance_i × duration_i / 3600 )`, the sum taken exactly over sub-intervals of constant balance. Not divided per term: per-term flooring loses precision in proportion to the *number of transfers*, so two wallets holding identically over the hour would score differently for having traded more often.
- **The window closes at the boundary, not at the last block.** The final sub-interval runs to `3600(e+1)`, not to the end block's timestamp. An Epoch is a time window observed through blocks; under the other reading `Σ duration ≠ 3600` and TWAB stops being comparable across Epochs.
- **Streak uses the same boundary.** `streakDays(a,e) = floor((3600(e+1) − max(lastSellAt(a), firstBuyAt(a))) / 86400)`. `lastSellAt` is the block timestamp of the most recent `Transfer(from = a)` whatever the destination. Multiplier in bps: `< 7 → 10000`, `7 ≤ d < 30 → 12500`, `≥ 30 → 15000`; `DEV_WALLET` is always `10000`.
- **Weight carries the bps; it is never divided down.** `weight(a) = TWAB(a) × multBps(a)`, `W = Σ weight(a)` over eligible `a`. The `10000` cancels between `weight` and `W`, so dividing early is pure floor loss applied per Holder.
- **Allocation spends funding plus Carry.** `base[t] = funded[t] + carryIn[t]`; `alloc(a,t) = floor(base[t] × weight(a) / W)`; `totals[t] = Σ alloc(a,t)`. The remainder stays as Carry for the next Root.
- **Excluded balances are zero** for every purpose. The Excluded set of Epoch `e` is every `account` of an `ExcludedAppended` log whose block timestamp is `< 3600(e+1)`: an entry applies to **the whole Epoch containing its block**, not from its block onward, so an append zeroes the address for the hour it lands in. The set is final when the Epoch ends, like the TWAB. The `exclusion set hash` of engineering spec §4.5 is `keccak256(abi.encodePacked(set))` over the ascending, de-duplicated addresses — recomputing it is a free cross-check against a published artifact.
- **Omission is computed last.** A Holder is omitted from the tree only when `alloc(a,t) == 0` for all five `t`.
- **`W == 0` is a legitimate state, not an error.** A funded Epoch with no eligible Holders gets no Root: engineering spec §3.3's Skipped mechanism rolls its funding into Carry. The Verifier reports MATCH (exit 0) for "funded Epoch, no Root", never INDETERMINATE.

**Resolved 2026-09-14 by `nutz-contracts`** (engineering spec §4.2, §3.5, v0.5): the whole-Epoch rule above, and the Distributor constructor now emits `ExcludedAppended` for every base entry, so the log stream is self-sufficient and no `excluded()` subtraction is needed. Verified in `src/NutzDistributor.sol`. Note this is the opposite of the execution-block rule this spec assumed before they answered.

## 6. Merkle tree

OpenZeppelin `StandardMerkleTree` v1.0.8 semantics, normative per ADR-0003. Leaf `keccak256(bytes.concat(keccak256(abi.encode(id, account, amounts[5]))))`. The encoding is fully static — 7 words, 224 bytes, no offsets — so no general ABI encoder is needed. Leaves are sorted by leaf hash (`sortLeaves: true`), the tree is a complete binary tree in a flat `2n-1` array with commutative sorted-pair hashing, root at index 0.

## 7. Verdicts and Assertions

Four Assertions, reported as four lines: Root equality; `totals` equality; the cap `totals[i] <= funded[i] + carryIn[i]`; `carryIn` equality against what the previous Epoch should have left. The full Carry chain back to deploy is `--chain` only.

Verdict is MATCH, MISMATCH or INDETERMINATE. Exit `0`, `1`, `2` respectively. **`2` is not `0`**: the Signer signs only on `0`, and conflating "could not check" with "checked and fine" degrades the 2-of-3 during exactly the RPC outage an attacker would pick.

## 8. CLI

```
nutz-verify epoch <id>     recompute and compare one Epoch
nutz-verify latest         the most recent posted Root  (dispute-window default)
nutz-verify sync           advance the Cache only
```

Flags: `--rpc` (repeatable; disagreement is INDETERMINATE), `--finality latest|safe|finalized` (default `safe`), `--fresh`, `--chain`, `--artifacts`, `--json`. Every run's header echoes chain id, Distributor address, pinned `DEV_WALLET`, finality level and endpoints — the unverifiable inputs are shown, not buried. `--json` carries a versioned schema; it is the Signer's interface.

## 9. Cache

`${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/`. Fixed 124-byte stride: `[120-byte payload][4-byte CRC32C]`, payload `(blockNumber, blockHash, timestamp, from, to, value)`. Fixed file header with magic, format version, chain id and contract address, so a file from the wrong chain is rejected loudly rather than answering confidently. No sidecar manifest — the CRC chain plus each record's block number make the log self-describing.

Truncate-to-last-valid-record on open; batched fsync on a checkpoint interval, never per record. Reorg: binary-search the fixed stride for the fork block, `Truncate(i * 124)`, fsync. Durability requirements are weak by construction — the log caches public data, so damage must be *detected*, not survived.

## 10. Testing

- **Cases** (`testdata/cases/`) for every §5 rule, hermetic, no RPC. Engineering spec §7 files these under "Indexer"; that contradicts §1 and §12 P0 and is wrong. They live here and the private indexer's CI consumes them.
- **Merkle fixtures** seeded from `nutz-contracts/test/fixtures/claims.json` (root `0x88b4…7591`), grown here with odd leaf counts, a single leaf and duplicate amounts.
- **Anvil harness**: fork 4663, run `nutz-contracts/script/Deploy.s.sol`, deploy a stand-in ERC-20, drive transfers and `postRoot`, verify end to end. The only way to test the chain-facing code before launch, and the thing that justifies hand-rolled decoding (ADR-0004).
- **Load test**: point ingestion at USDG history on 4663. At ~100 ms blocks the real risk is volume, and this is the only way to learn before launch whether a from-scratch sync takes minutes or hours.
- **Dry run**: the 48h throwaway launch of engineering spec §11 week 4.

## 11. Out of scope

- **Converter honesty** — whether the ETH→USDG rate was fair or the Slices correct. A different claim ("did you get a good price") needing historical quotes that are not deterministic. Folding it in would muddy what MATCH means. A separate tool, if ever.
- **Acorn Draws (§4.6)** — scope B, its own effort under `.scratch/verifier-draw/`, after its own grilling round. Its arithmetic has an unresolved cluster of its own: when `TW(a,d)` floors across the 168 hourly TWABs, whether the 1%-of-supply cap applies to Tickets or to balance, the order of the 30-day doubling against that cap, how Tiers shrink, and which Chainlink round fixes `P`. **Do not start it until `nutz-contracts` patches §4.6's Seed** — it still reads `keccak256(signature)` where the built contract stores `sha256(signature)`.
