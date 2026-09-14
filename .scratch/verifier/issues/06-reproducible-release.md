# 06 — Reproducible release and published checksums

Status: ready-for-agent
Type: task
Spec: ../spec.md §1; ADR-0001
Blocked by: 05

GitHub Actions release: `-trimpath`, `CGO_ENABLED=0`, cross-compiled to linux/darwin × amd64/arm64, publishing **`SHA256SUMS`**. Verify in CI that every target builds cgo-free.

The checksums are not cosmetic. The warm Signer of engineering spec §3.2 pins the binary **by checksum, not by path** — swapping the binary on the signer host would otherwise be the cheapest attack on the whole 2-of-3 scheme, since it would let an attacker manufacture a MATCH without touching a key. Document that pinning requirement in the README next to the download link, not only here.

Also in this ticket: a README that states plainly what the Verifier does **not** prove — it trusts its RPC endpoints, and `--rpc` cross-checking reduces that to "two providers are not colluding" rather than eliminating it; a local node is the only true fix. ADR-0002 treats overselling as worse than a narrow claim, and the README is where that promise is either kept or broken.

Tests: two builds of the same commit on different machines produce identical checksums (this is what `-trimpath` buys and it should be asserted, not assumed); the published checksum verifies against the release artifact in a clean container.
