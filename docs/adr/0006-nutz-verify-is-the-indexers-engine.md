---
status: accepted
date: 2026-09-16
---

# nutz-verify is the indexer's engine

The private indexer is written in Go and **imports this module** — the `epoch` package, with `chain`, `cache` and `merkle` under it — rather than reimplementing the allocation rules. The Root the indexer posts and the Root the Signer's verifier recomputes come from one function, so byte-identity between them is a property of the code, not of a test. This supersedes the implementation-independence half of ADR-0001's argument for Go; the static-binary half stands, and the Signer still executes the published binary rather than importing anything.

## Considered options

- **A TypeScript indexer, as the engineering spec names it (§1, C4)**, checked against this repo by running the Cases in both CIs. Rejected: two implementations of a rule is two places for a rule to land, and the second one is the one nobody runs in a Dispute window. Every divergence found in CI is a bug fixed in one of them, after the fact.
- **A Go indexer written independently of this module**, sharing nothing. Rejected: the independence would be nominal. The same author writing both from the same spec, resolving the same ambiguities the same way, does not make two implementations that fail differently — it makes one implementation typed twice, and the second typing is where the typo goes.
- **One path.** Chosen. A rule change lands once, as a Case, and the code that honours it is the code both parties run.

## Consequences

- **This repo has a public Go API**, which spec §3 had ruled out. It is versioned by tag with one consumer: the indexer pins an exact tag in its `go.mod` and records it in every Bundle's `input.json`, and the Signer's binary is built from that same tag. A Bundle whose recorded version differs from the Signer's binary is visible before anything is signed.
- **What MATCH proves changes, and the README says so.** A MATCH from the Signer's host — a separate machine, its own RPC endpoints, its own Cache — catches a compromised keeper, an endpoint serving bad data, and a Bundle tampered with after computation. It does not catch a bug in the shared rules; the Cases do, and the per-Epoch cap (ADR-0002, Assertion 3) bounds what such a bug can cost to one Epoch's funding. A verifier that oversells what it proves is worse than a narrow one, so this is written down rather than left to be inferred.
- **The Engine defines its own `Result`**, so nothing under `internal/` reaches a public signature. `twab`, `alloc` and `report` stay internal: the rules are an interface only in the form of the Cases, and a consumer that wants a number gets it from a `Result`.
- **ADR-0003's "the private indexer's CI consumes the same directory and fails on divergence" is retired.** Two implementations could diverge; one cannot. The Cases pin the Engine in this repo's CI, at the tag the indexer pins, and re-running them in the consumer would test the same code twice. What the indexer's CI tests is its own layer — the Bundle and the store written from a `Result` — against a chain of its own.
- **`cmd/nutz-verify` computes nothing.** It reads a `Result`, compares it with the chain, and prints. The test that pins this — the CLI's `assess` consuming a `Result` from `Compute` with no field of its own — is what keeps the binary the Signer runs and the library the indexer links on one path.
