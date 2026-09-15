# 09 — Point the anvil harness at `script/Deploy.s.sol`

Status: wontfix
Type: task
Spec: ../spec.md §10; ticket 03
Blocked by: —

Ticket 03 asked for the harness to run `nutz-contracts/script/Deploy.s.sol`. It does not: `run()`
hardcodes `script/config/robinhood.json`, whose `signers` and `keeper` are all the zero address
pending that file's own TODO, and the `Signers` constructor reverts `ZeroAddress` on them. The
harness deploys the same compiled `NutzDistributor` artifact directly instead.

**This ticket's original justification was wrong and it is closed unbuilt.** It claimed the deploy
path was "untested anywhere today". It is tested, in the repo that owns it:
`nutz-contracts/test/unit/Deploy.t.sol` covers the Converter address prediction
(`test_deploy_converterLandsOnTheDistributorsImmutable`), the revert when the prediction misses,
`checkRoles` for a differing keeper and for a differing Signer, and the Draw's wiring. Every
argument for running the script from here was an argument for coverage that already exists.

Running it from here would also buy no decoding coverage at all. The four log and call shapes
ticket 03 decodes come from the Distributor alone; neither the Converter nor the Draw appears in
any of them. What it would buy is a coupling from this repo's test suite to a sibling repo's
script API, for a path that repo already tests.

## The real gap, which belongs to `nutz-contracts`

`test_load_readsTheRobinhoodConfig` asserts every field of the committed config **except `signers`
and `keeper`** — exactly the two that are still zero — and nothing feeds `load()`'s result into
`deploy()`. So nobody asserts that the committed config actually deploys, and today it cannot.

That is one test over there, and it is better than anything this harness could do, because it
exercises the real config rather than harness-invented addresses:

```solidity
function test_deploy_theCommittedConfigActuallyDeploys() public {
    Deploy.Params memory p = script.load(string.concat(vm.projectRoot(), "/script/config/robinhood.json"));
    script.deploy(p, address(script));
}
```

It fails today on `ZeroAddress`, which is the point: it turns the config's TODO into a red test and
a launch gate, rather than a comment nobody is blocked by. `deploy()` is `public` and the existing
`setUp` already does `vm.warp(1_800_000_000)`, so nothing else is needed.

**Hand this to `nutz-contracts` as an issue on their tracker.** Nothing in this repo changes.

## If it is ever reopened

The blocker is mechanical, not conceptual: `run()` reads one hardcoded path, so either that repo
grows a way to point it elsewhere (an env var through `vm.envOr`, or a second entry point taking a
path — both also need a `fs_permissions` entry), or this repo builds an overlay root of symlinks so
`vm.projectRoot()` resolves to a directory carrying our config. The second needs nothing from them
and was rejected as fragile for the value.

Also unverified: whether a fork of 4663 has the EIP-2537 precompiles `NutzDraw`'s constructor
self-test requires. It is deployed third by the script, so running `Deploy.s.sol` at all depends on
it.

## Comments

2026-09-15 (agent): handed off. The test above is filed as
`nutz-contracts/.scratch/distributor/issues/11-committed-config-deploys.md` (their `.scratch` is
gitignored, so the file on disk is the tracker entry; nothing to commit there). Before filing, the
test was run once as a throwaway probe against nutz-contracts `86e281c`: `[FAIL: ZeroAddress()]`,
as this ticket predicted. The issue is `needs-triage` rather than `ready-for-agent` because their
`ci.yml` runs the whole unit suite, so the test is red in CI from the day it lands until the
signers and keeper are filled in; the maintainer picks between landing it red as the launch gate
(recommended) and landing it together with the addresses.

One claim above is loose: `test_load_readsTheRobinhoodConfig` asserts a sample of fields (four of
five tokens, the Pons and Uniswap addresses, one gas and one price bound), not "every field except
`signers` and `keeper`". The point stands, since it asserts neither of those two, and the filed
issue says it precisely. Nothing else in this repo changes; the status stays wontfix.
