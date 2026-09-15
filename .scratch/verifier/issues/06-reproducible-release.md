# 06 — Reproducible release and published checksums

Status: resolved
Type: task
Spec: ../spec.md §1; ADR-0001
Blocked by: 05

GitHub Actions release: `-trimpath`, `CGO_ENABLED=0`, cross-compiled to linux/darwin × amd64/arm64, publishing **`SHA256SUMS`**. Verify in CI that every target builds cgo-free.

The checksums are not cosmetic. The warm Signer of engineering spec §3.2 pins the binary **by checksum, not by path** — swapping the binary on the signer host would otherwise be the cheapest attack on the whole 2-of-3 scheme, since it would let an attacker manufacture a MATCH without touching a key. Document that pinning requirement in the README next to the download link, not only here.

Also in this ticket: a README that states plainly what the Verifier does **not** prove — it trusts its RPC endpoints, and `--rpc` cross-checking reduces that to "two providers are not colluding" rather than eliminating it; a local node is the only true fix. ADR-0002 treats overselling as worse than a narrow claim, and the README is where that promise is either kept or broken.

Tests: two builds of the same commit on different machines produce identical checksums (this is what `-trimpath` buys and it should be asserted, not assumed); the published checksum verifies against the release artifact in a clean container.

## Comments

**2026-09-14 — implemented.** `scripts/build-release.sh`, `.github/workflows/release.yml`, a
`release-build` job in `ci.yml`, `TestReleaseBuildIsReproducible`, and the README's Install,
Reproducing a release and What MATCH does not prove sections.

- **One recipe, three callers.** The build script is the whole release: `CGO_ENABLED=0 go
  build -trimpath -buildvcs=false` for linux/darwin × amd64/arm64, host environment shut out
  (`GOENV=off`, `GOFLAGS` and `GOEXPERIMENT` emptied, `GOAMD64`/`GOARM64` at baseline), the
  settings read back from every binary with `go version -m`, and `SHA256SUMS` over the bare
  file names so `sha256sum -c` works from a download directory. The release workflow, the
  per-push CI gate and a stranger reproducing a checksum all run it; no goreleaser, because a
  release tool would be one more thing a reproducer has to install and trust.
- **Reproducibility is asserted twice.** Locally, the Go test builds the module from two
  different directories and requires identical `SHA256SUMS` (what `-trimpath` buys). At the
  tag, `release.yml` builds on ubuntu-latest and macos-latest and diffs their `SHA256SUMS`;
  nothing is published unless the two machines agree. `-buildvcs=false` is deliberate: with
  VCS stamping a source tarball and a git checkout give different bytes, and the checksum,
  not an embedded commit, is the artifact's identity.
- **The toolchain is pinned, and that changed go.mod.** A release built by "whatever go is
  on PATH" is not reproducible: this machine's go1.26.5 and the go.mod minimum go1.24.0 gave
  different checksums for the same commit. The script therefore exports `GOTOOLCHAIN` from
  go.mod and the Go command downloads it if needed. But pinning to the `go 1.24.0` directive
  would have shipped a binary that govulncheck says has **30 reachable standard-library
  vulnerabilities** (crypto/tls, crypto/x509, net/http — the code every RPC call goes
  through), because 1.24 is out of support. go.mod now carries **`toolchain go1.26.8`**,
  the newest patch of the older supported line; the `go 1.24.0` minimum spec §3 wants for
  distro builds is unchanged, and govulncheck under 1.26.8 finds nothing. setup-go installs
  the toolchain line, so the `go` job now tests the release toolchain, and a separate step
  builds and vets with exactly the `go` line's version so spec §3's "a distro toolchain
  builds it" stays asserted rather than assumed.
  This is a decision ticket 10's "go-version-file: go.mod" comment did not anticipate, and
  it means the toolchain line needs bumping as patches land — a Dependabot/Renovate rule
  for `go.mod` toolchain, or a nightly govulncheck in the `live` tier, is the follow-up.
- **The clean-container check is on the release, not the artifact.** After publishing, the
  workflow downloads the release's own assets with `gh`, then verifies them inside
  `alpine:3.22` with nothing but busybox: `sha256sum -c` passes and the static binary
  executes (no arguments is exit 2 with usage, which proves it runs without asserting
  anything about a chain the container cannot reach). Rehearsed locally in Docker.
- **A release that already exists is not replaced.** `gh release create` fails on a rerun;
  a checksum the Signer has pinned must not change under it. Cut a new tag instead.
- **README.** The Signer pinning requirement sits next to the download instructions, as the
  ticket asks: pin by checksum, recheck before every run, and the pin must be a published
  checksum because those are the reproducible ones. The "What MATCH does not prove" section
  states the endpoint trust plainly — `--rpc` twice reduces it to "two providers are not
  colluding", a local node is the only true fix — plus the pinned addresses, funding
  fairness and finality, each as a narrow claim.

### Fixed in review

A `workflow_dispatch` on a tag ref would have published (the gate was `ref_type == 'tag'`,
now `event_name == 'push'`); a relative output directory resolved against the repo root
rather than the caller; the settings check relied on literal tab characters; `GOFIPS140` was
not pinned; the release notes read the toolchain from go.mod instead of from the binary
being published; and nothing built with the `go` line's minimum any more.

Not done here: `workflow_dispatch` on `release.yml` is a dry run (build and compare, publish
nothing) but no tag has been cut, so the `publish` and `verify` jobs have run only as a local
Docker rehearsal. There is no `LICENSE` file in the repo, and the spec says MIT; a release
page without one is odd. A test that a release matches a *source tarball* build (no `.git`)
is not written; `-buildvcs=false` is what makes it true.
