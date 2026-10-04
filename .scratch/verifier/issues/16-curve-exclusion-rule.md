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

**2026-10-04, released ahead of the Pin.** nutz-platform asked for the rule before launch day
(its launch ticket 04, the Pin writer), so it moves to the new `epoch.Deployment` now and
tests it nightly: v0.4.0 carries the rule and stays unpinned, `Pinned()` naming only ChainID
and the Pons factory, so like v0.3.0 every run is exit 2 "not pinned". "Ships with the Pin"
above, and in ADR-0007's Consequences, protected the first Root, and no Root is computed
before the Pin either way. The Pin is the next minor, written by nutz-platform's `pin.sh`.

Before the tag: ADR-0007 and this ticket's docs committed (e119a1d), and two tests that used
`epoch.Pinned()` to mean "no addresses" now pass `epoch.Deployment{ChainID: 4663}` (40d4ac4),
so the Pin does not turn the suite red. The harness job had been red nightly since
2026-09-30: nutz-contracts moved to Foundry 1.8.3 in 2d9412f and its lint config fails
under 1.8.1; the CI pin follows it (0592676), CI green (run 37232994249).

Tagged v0.4.0 on 0592676 (05926763d9b816301ff8bbc8bda52ae35030ef29); release run 37233336684
built it on ubuntu-latest and macos-latest with identical checksums, published, and verified
the download in a clean container. Reproduced from source on two more machines — this host
with `scripts/build-release.sh` from `git archive v0.4.0`, and a fresh `golang:1.26-bookworm`
container from the same archive — both identical to the published SHA256SUMS:

    526e4bfca4cc232dfe62f0858c17b8170ffebe750d9fd7807a4c9bbe69c5293b  nutz-verify_linux_amd64
    0128ebb0655b5324576d835cbd47a9de2e9b963526892e051416812d3e06d5a0  nutz-verify_linux_arm64
    f6b81604253825296c28096978a6a19afbfc4e094699578cfc83f8397970eeb9  nutz-verify_darwin_amd64
    552c17022b2635b84452e42f038f355e5ca61348571795dfbba4a932c847ac37  nutz-verify_darwin_arm64

The linux/amd64 binary run against chain 4663's public endpoint: zero addresses in the
header, exit 2.
