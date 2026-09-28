# 16 — The Token's Pons curve is Excluded in every Epoch

Status: resolved
Type: task
Blocked by: —
Blocks: the pin-and-release step of nutz-platform's launch effort (`.scratch/launch/brief.md` item 4)
Spec: engineering spec §2.4, §3.5, §4.2, §4.5, §10 step 5; nutz-platform `.scratch/launch/brief.md`

Written 2026-09-27 from the launch interview; confirmed by eduar the same day. Before the
release that pins the launch addresses, the Engine must treat one more address as Excluded in
every Epoch: the Pons bonding curve of the pinned Token, read from the Pons factory's launch
record. The curve is created at the Launch, one per token, so the Distributor's on-chain
Excluded list cannot name it at deploy, and an append through the 48 h timelock would leave
the curve counting as the largest holder for the first two days. A rule in nutz-verify only,
with the Dev wallet as its precedent; no contract changes, no Bundle format change.

## The problem

Before graduation, the NUTZ nobody has bought yet sits in a Pons bonding curve the factory
creates at the Launch; its address is the `curve` field of `factory.getLaunchedToken(Token)`.
On day one it holds almost the entire supply of 1e9 NUTZ. Rewards are pro rata to TWAB and
an address counts unless it is Excluded; the Excluded set is rebuilt from `ExcludedAppended`
logs, base list at construction, then appends through a 2-of-3 proposal with a 48 h timelock,
each applying from the Epoch it lands in (engineering spec §4.2). The launch runbook assumed
the Pons contracts holding curve supply were global and in the base list. They are not: the
deploy comes before the Launch, so the base list cannot name the curve, and most of two days
of fees would be allocated to an address that can never claim.

The v4 pool is the opposite case: after graduation the liquidity sits in Uniswap v4, whose
single PoolManager is global and known today, so it belongs in the base list and §10 step 5's
"append the pool at graduation" disappears. Only the curve has no fixed address.

## The rule

The curve of the pinned Token is Excluded in every Epoch. The Engine reads it once per run as
`factory.getLaunchedToken(Token).curve` through the cross-checked call path, at the tip, and
treats it as if an `ExcludedAppended` log for it had landed at timestamp 0. A zero curve, or
a record with `exists == false`, is an error and never an empty set: the pinned Token was not
launched through this factory. The rule is safe after graduation: `graduate()` runs a final
sweep and moves the supply into the v4 pool, so the curve holds nothing and excluding it
changes no allocation. One `eth_call` per run, forever.

## What changed

| Where | Change |
|---|---|
| `epoch.Deployment` | gains `PonsFactory`; `Pinned()` carries `0x7eD598BcEf8bd9Edd8C97A195C6d13f40801EC7e` today; `Check` refuses a Pin without it; `New` refuses a Reader whose factory differs |
| `chain.Config` / `Reader` | `PonsFactory`; `LaunchCurve(ctx, at)`, one cross-checked `getLaunchedToken(Token)` decoded from the fifteen-word struct; `callAt` takes a target |
| `Engine.load` | after the `ExcludedAppended` stream, one synthetic `Exclusion{Account: curve, Timestamp: 0}` in both the Ledger's and the Result's streams, so `Result.Excluded` and the exclusion set hash see it from the first Epoch |
| Cases | optional `curve` field: the Engine runner feeds it to the fake factory, the hermetic runner folds it in as an exclusion at timestamp 0; every Case's fake node answers `getLaunchedToken`; new Case `curve-holds-the-supply` |
| Fake node | `Launch(factory, token, curve)`; `eth_call` dispatched on the target |
| Anvil harness | deploys nutz-contracts' `MockPonsFactory` and records the stand-in's launch; a subtest reads the curve and refuses a token never launched |
| Docs | ADR-0007; spec §5; README; the Cases README; engineering spec §3.5 and §4.2, one sentence each, in nutz-contracts and nutz-platform |

## Decisions, 2026-09-27

Six calls from the analysis, all confirmed:
1. The anvil harness deploys `MockPonsFactory` (already in `nutz-contracts/test/mocks/pons/`) rather than asking the real factory about a token it never launched.
2. `PonsFactory` is a `chain.Config` field beside Token and Distributor, and `epoch.New` refuses a mismatch as it does for the other two.
3. The fake node dispatches `eth_call` on the target address.
4. The refusal is an Engine test, not a Case: Cases are allocation fixtures.
5. The supply Case names its curve in a new optional `curve` field.
6. The synthetic entry's timestamp is 0, so no header read is needed; the Site's block is the Token block.

Alternatives set aside, recorded in ADR-0007: keep the on-chain list as the only source and
hold keeper bind for 48 h after the Launch; or launch first and deploy second with the curve
in the base list.

## Done when

`go test ./...` green including the new Case through both runners, `TestCompute_RefusesATokenTheFactoryNeverLaunched`,
`TestCompute_TheCurveIsInEveryEpochsExcludedSet` and the anvil harness on a fork of 4663;
ADR-0007 and the spec sentences read as above. Released together with the Pin, per the day's
ordering.

## Comments

**2026-09-27, built.** All of the above; `go test ./...`, vet and staticcheck green; the anvil
harness passes on a fork of 4663 with the factory mock (17 subtests). Not released: the
release waits for the Pin, and the two ship together. Outside this repo and not done here:
robinhood.json's `excludedBase` gaining the v4 PoolManager and the global Pons contracts, and
§10 step 5 losing the graduation append.
