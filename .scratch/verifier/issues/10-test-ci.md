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
| `harness (fork of 4663)` | foundry, a built `nutz-contracts` — **no secret** | every push and PR |
| `live (real 4663, two providers)` | the public endpoint; `RPC_4663` for the cross-check | nightly and `workflow_dispatch` |

**The fork needs no secret**, which was not obvious until it was tried. Chain 4663's public
endpoint (`https://rpc.mainnet.chain.robinhood.com`) needs no key, and the harness deploys its own
contracts and reads only block *headers* from before the fork — which a full node serves without
archive state. So the forked harness runs on every push with a URL pinned in the workflow as a
plain value. That is the same fact ADR-0002 rests on: a hostile stranger runs the Verifier against
that endpoint with no credential at all, and a CI that needed one would contradict the claim.

Enforcement is still two variables — `NUTZ_VERIFY_REQUIRE_ANVIL` (tools present) and
`NUTZ_VERIFY_REQUIRE_FORK` (really a fork) — because a contributor without foundry wants neither,
and a contributor offline wants only the first. CI sets both. Unset, both skip.

A bare node exercises every decoder, which is what ADR-0004 bought. What it cannot answer is
anything about chain 4663 itself, so the subtest that asks whether `safe` and `finalized` are
served there skips unless the node is actually a fork — on a bare node it was asserting anvil's
tags and implying they were 4663's.

The `live` tier is new and is the only test ADR-0002's cross-check has that is not one anvil
compared with itself: it reads real USDG history through the public endpoint and a Chainstack
archive and requires the two to agree byte for byte. It also proves the paging against the public
endpoint's real 10,000-result cap. Nightly rather than per-push because it queries an endpoint
someone else pays for.

## Decisions worth knowing

- **`RPC_4663` is optional everywhere.** Without it the `live` job still proves the paging; with it
  the two-provider cross-check runs too. Nothing green-skips: the live test skips only when the
  public endpoint is throttling us, and says so, because a third party's quota is not something a
  test can assert about.
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

- ~~`CONTRACTS_TOKEN` is referenced with a `github.token` fallback, so this works whether or not
  `nutz-contracts` is private.~~ Verified 2026-09-15: both repos are public, so the `token:` line
  is gone and the harness job references no secret at all.
- `.env.example` documents every variable above, and opens by saying the Verifier itself needs none
  of them.
- Nothing publishes coverage. `nutz-contracts` uploads an lcov artifact; worth mirroring if anyone
  wants the trend.

## Comments

**2026-09-15.** Checked as `nutz-dev` through `gh`: `nutzwtf/nutz-contracts` is public, so the
`CONTRACTS_TOKEN` fallback was dead and the `token:` line is deleted. `RPC_4663` was already a
secret on `nutz-contracts`; the same value is set here by `scripts/wizard-first-release.sh`,
which also pushes `main`, watches the first CI run, and cuts `v0.1.0`. Nothing on GitHub had run
before that: `main` was 18 commits ahead of `origin` and no workflow run or tag existed.

**2026-09-15, first run on GitHub (35017880822), red twice.** Both go the ticket's way, neither
was visible from a machine where go is already 1.26.8. (1) `setup-go`'s `go-version-file`
reads only the `go` line, not `toolchain` — ticket 06's "setup-go installs the toolchain line"
was an assumption, and the log says `Setup go version spec 1.24.0`. A 1.24 go on PATH builds
`go run staticcheck@…` in module-less mode with 1.24, then the repo's `go list` switches to
1.26.8, whose stdlib staticcheck cannot parse. Fixed: every setup-go now takes the version read
from the toolchain line by a one-line step, in both workflows. (2) The harness checks out
nutz-contracts' *GitHub* main, which was at 410b143 while the local sibling that made the
harness pass was 40 commits ahead (and 6 behind) — the base-list-at-construction change
(1d29036) had never been pushed, so the fork saw one `ExcludedAppended` instead of three. Not a
bug here; nutz-contracts has to be pushed before this harness can be green, and the checkout
comment now says so.
