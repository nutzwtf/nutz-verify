# 09 — Point the anvil harness at `script/Deploy.s.sol`

Status: needs-info
Type: task
Spec: ../spec.md §10; ticket 03
Blocked by: —
External blocker: `nutz-contracts` must fill in `script/config/robinhood.json`

Ticket 03 asked for the harness to run `nutz-contracts/script/Deploy.s.sol`. It does not, and today
it cannot: `run()` hardcodes `script/config/robinhood.json`, whose `signers` and `keeper` are all
the zero address pending that file's own TODO, and the `Signers` constructor reverts `ZeroAddress`
on them. The harness deploys the same compiled `NutzDistributor` artifact directly instead.

That is sound for what ticket 03 decodes — the four log and call shapes come from the Distributor
alone, and neither the Converter nor the Draw appears in any of them. What it does not cover is the
deploy path we will actually use: the Converter address prediction, the `checkRoles` assertion, and
the constructor arguments coming from a config file rather than from the harness.

## Done when

The harness invokes `Deploy.s.sol` against the anvil fork with a config carrying real addresses, and
reads its logged Distributor address instead of deploying one itself. Two things to settle first:

- **Whose config.** `run()` reads one hardcoded path, so either `nutz-contracts` grows a way to
  point it elsewhere (an env var, or a second entry point taking a path), or this repo builds an
  overlay root. The first is cleaner and is a question for them.
- **Whether the fork has the EIP-2537 precompiles.** `NutzDraw`'s constructor self-test reverts
  `VerifierSelfTestFailed` without them, and it is deployed third. Chain 4663 is claimed to have
  them (ADR-0004 over there); a fork of it should, but that is unverified here.

Until then the harness says what it deploys and why, in a comment at the top of `anvil_test.go`.
