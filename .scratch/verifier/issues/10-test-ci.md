# 10 — Test CI, and making the anvil harness impossible to skip silently

Status: resolved
Type: task
Spec: ../spec.md §10; ADR-0004
Blocked by: —

This repo had no CI. Ticket 06 owns the *release* workflow — reproducible builds and published
checksums — and is blocked by 05; nothing owned the test pipeline.

The load-bearing part is not "run the tests". It is that ADR-0004 trades *"we own the decoding
bugs"* for *"the anvil harness exercises every decoder against a real node"*, and until now every
precondition failure in that harness was a `t.Skipf`. A pipeline without foundry installed would
have been green having exercised no decoder against any node — the bargain kept in the ADR and not
in fact.

## Three tiers, and why the split matters

| Job | Needs | Runs |
|---|---|---|
| `go` | a Go toolchain | every push and PR |
| `harness (bare node)` | foundry, a built `nutz-contracts` | every push and PR |
| `harness (fork of 4663)` | the above plus `RPC_4663` | nightly and `workflow_dispatch` |

The middle tier is the point. A single `NUTZ_VERIFY_REQUIRE_ANVIL` covering both the tools and the
fork would mean the decoders could only be *required* to meet a node where the archive secret is
available — which is nightly, and a decoder regression would then sit unnoticed for a day. So
enforcement is split in two: `NUTZ_VERIFY_REQUIRE_ANVIL` (tools present; a bare node satisfies it,
so it needs no secrets) and `NUTZ_VERIFY_REQUIRE_FORK` (the node must really be a fork of 4663).
Unset, both still skip, so a contributor without foundry can run `go test ./...`.

A bare node exercises every decoder, which is what ADR-0004 bought. What it cannot answer is
anything about chain 4663 itself, so the subtest that asks whether `safe` and `finalized` are
served there skips unless the node is actually a fork — on a bare node it was asserting anvil's
tags and implying they were 4663's.

## Decisions worth knowing

- **The fork job has no `if: env.RPC_4663 != ''` guard**, unlike `nutz-contracts`' fork job. A
  nightly that green-skips because a secret is missing is the same silent non-check this ticket
  exists to remove. It is red until `RPC_4663` is configured, deliberately.
- **`go-version-file: go.mod`**, not the newest release. Spec §3 wants a conservative directive so a
  distro toolchain can build the Verifier; running CI on anything newer would leave that untested.
- **The dependency tree is a gate, not a convention.** ADR-0004 calls `go.sum` a user-facing
  artifact, so CI fails if `go list -deps` is anything but `x/crypto/sha3` and its `x/sys/cpu`
  requirement. Adding a dependency is meant to be an ADR-level decision; now it cannot be an
  accident. `go mod tidy` is checked for a no-op in the same job.
- **foundry is pinned to v1.8.1**, matching `nutz-contracts`' `.mise.toml` and its CI.
- `nutz-contracts` is checked out into the workspace because `actions/checkout` refuses paths
  outside it; `NUTZ_CONTRACTS` points the harness there, and the path is gitignored.

## Fixed while building it

The harness waited a flat 60 seconds for anvil and then reported only "never answered". Forking a
busy archive endpoint can take longer than that — it was seen failing once at exactly the deadline
— and a bad archive URL failed the same opaque way. anvil's stderr is now captured and its exit
watched, so a bad URL fails in 0.3 s quoting the node's own reason, and the fork budget is three
minutes. Four consecutive forked runs at ~19 s each.

## Not done here

- `CONTRACTS_TOKEN` is referenced with a `github.token` fallback, so this works whether or not
  `nutz-contracts` is private. If it is private, add the secret; if it is public, delete the `token:`
  lines. Unverified from this machine.
- Nothing publishes coverage. `nutz-contracts` uploads an lcov artifact; worth mirroring if anyone
  wants the trend.
