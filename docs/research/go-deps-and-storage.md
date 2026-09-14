# Dependency policy and embedded storage for `nutz-verify`

Research note. Audience: maintainers of `nutz-verify`, a third-party verifier CLI for an
Arbitrum Orbit chain (chain id 4663). Two constraints are treated as fixed and load-bearing:

- **(a)** single static binary, cross-compiled to `linux/darwin` × `amd64/arm64`, no cgo;
- **(b)** minimal auditable dependency tree — "you can read all of it" *is* the product.

Every number in the "measured here" tables was produced on this machine against the real
modules; the commands are reproducible and the method is described in
[Appendix A](#appendix-a--how-the-numbers-were-measured). Everything else carries an inline
source URL. Claims I could **not** confirm from a primary source are called out in
[Unverified / open questions](#unverified--open-questions) rather than stated as fact.

Toolchain used for all measurements: `go1.26.5 linux/amd64`.

---

## Bottom line

### Question 1 — dependency policy

**Take the `golang.org/x/crypto/sha3` dependency for Keccak-256. Hand-roll ABI decoding and
JSON-RPC. Do not link go-ethereum.**

1. The standard library's `crypto/sha3` (Go 1.24+) implements **FIPS-202 only** — SHA3-224/256/384/512,
   SHAKE, cSHAKE. It exposes **no Keccak-256**. Verified by reading the package source: the only
   exported constructors are `New224/256/384/512`, `NewSHAKE128/256`, `NewCSHAKE128/256`, and the
   `Sum*` helpers. Ethereum's legacy Keccak padding (`0x01`) exists in the tree but only in the
   *internal* package `crypto/internal/fips140/sha3`, which you cannot import.
2. `golang.org/x/crypto/sha3.NewLegacyKeccak256` **remains the maintained answer**, and the
   suspected change is real — and is *good news*: as of **x/crypto v0.44.0** (2025-10-08) the package
   was rewritten so that everything *except* legacy Keccak is a thin wrapper over stdlib
   `crypto/sha3`. The legacy Keccak implementation is retained locally precisely because stdlib
   declines to ship it. The package got smaller, not larger.
3. **The cost is trivially small, and I measured it:** **+16 KB** of binary, **2 modules**
   (`golang.org/x/crypto` + `golang.org/x/sys`), and **679 lines** of Go you would actually need to
   audit. It cross-compiles cgo-free to all four targets. This is the cheapest dependency in the
   whole analysis and it is squarely on-mission.
4. **Correction to the question's premise, and it cuts against the recommendation: pulling
   go-ethereum *is* the norm, even for tiny tools.** I went looking for small Go verifier tools that
   hand-roll JSON-RPC and **found none**. `0xKiwi/go-merkle-distributor` — a merkle-airdrop
   generator — has a two-line `go.mod`, one line of which is geth. `Bananapus/juicerkle` likewise.
   `flashbots/mev-boost` imports exactly two geth packages (`common`, `common/hexutil`) yet compiles
   24 of them. The Go tools that *don't* pull geth (drand, `wealdtech/ethdo`) avoid it because they
   never touch the EVM, not because they found a lean path. **The hand-rolled approach is not an
   observed convention; it is a road not taken.** Recommending it here is a first-principles call
   about *this* product, not an appeal to what everyone else does — and it should be made with that
   understood. See [§1.8](#18-what-small-auditable-go-tools-actually-do--the-honest-answer).
5. **The cost is nonetheless real and asymmetric, and it lands almost entirely on the RPC side.**
   Measured against geth v1.17.5: `ethclient` pulls **103 modules** / **8,440 KB** — including 17
   OpenTelemetry packages, `gopsutil`, 12 `gnark-crypto` and 9 `go-eth-kzg` packages, none of which
   `eth_getLogs` needs. But `accounts/abi` *standalone* is far cheaper than folklore suggests:
   **11 modules, 3,876 KB**, cgo-free on all four targets. Go's module-graph pruning collapses that
   scary 79-line `go.mod`. **So the honest middle option is: geth `accounts/abi` + hand-rolled
   JSON-RPC.** If you would rather not hand-roll the decoding, take that. If the ABI surface stays
   as small as one `Transfer` event, hand-roll both and take zero modules.
6. **Licensing is an independent reason to avoid geth**, and it may matter more than the byte
   count: go-ethereum is **LGPL-3.0** (library) / GPL-3.0 (binaries). `nutz-verify` is MIT and ships
   a *statically linked* binary. See [§1.5](#15-licensing-lgpl-30-vs-a-static-mit-binary).
7. **Do not use `umbracle/ethgo`** — it is dead (last commit 2024-11-01). **Do not use
   `lmittmann/w3` for leanness** — it *depends on* geth and measures larger than raw `ethclient`.
   The only maintained ABI+RPC library that is genuinely geth-free is **`defiweb/go-eth`**
   (20 modules, actively maintained), but it forces a `go 1.26.0` toolchain. See
   [§1.7](#17-lean-alternatives-to-go-ethereum--surveyed).

### Question 2 — storage

**Use a custom append-only file with fixed-width records. Do not add a KV store.**

For this workload — write once in block order, read by full sequential replay, no random access,
no range queries, no secondary indexes, no concurrent writers, truncate-only mutation — an
embedded KV store is not merely overkill; on the write path it is **measurably worse**.

Measured here, 2,000,000 records of the exact 120-byte record shape:

| | write | replay | file size | binary | modules (`go.sum`) |
|---|---:|---:|---:|---:|---:|
| **flat file** (+CRC32C per record) | **11.9M rec/s** | **32.4M rec/s** | **0.25 GB** | **1,660 KB** | **0** |
| bbolt | 1.25M rec/s | 47.0M rec/s | 0.30 GB | 1,996 KB | 7 |

The flat file writes **9.5× faster** and uses **20 % less disk**. bbolt's replay edge is real but
irrelevant: the flat file's replay figure *includes verifying a CRC32C on every record*, bbolt's
does not, and both are far faster than the network fetch that produces the data.

**The decisive fact is not the throughput, though.** bbolt's own README states:

> Because of the way pages are laid out on disk, Bolt cannot truncate data files and return free
> pages back to the disk.

The single mutation this workload has is **truncation at a reorg** — and bbolt structurally cannot
give the space back. A flat file does it in one `Truncate` syscall. (bbolt files are also
endian-specific, an awkward property for a tool shipping to four platforms.)

The honest case *for* a KV store is crash consistency, not speed — and a correct flat-file design
gets that in about 60 lines. See [§2.3](#23-the-real-argument-crash-consistency-not-speed) for the
minimal correct design (record framing, CRC32C, fsync policy, and why this workload needs **no**
separate manifest).

**Precedent, for completeness:** go-ethereum defaults to **Pebble** (since **v1.12.0**, 2023-05-25;
Pebble v2 for new nodes since v1.17.5) and erigon uses **MDBX via cgo** — which cannot produce a
`CGO_ENABLED=0` static binary and cannot cross-compile without a per-target C toolchain. Neither
precedent transfers: both are archival full nodes with random-access, continuously-mutated,
concurrently-read state. The transferable lesson is erigon's negative one — **a C-backed store is a
one-way door on constraint (a)**. See [§2.4](#24-what-ethereum-adjacent-go-projects-actually-use).

**All five candidates do build cgo-free and cross-compile to all four targets** — including
`modernc.org/sqlite`, which I verified rather than assumed. cgo is therefore *not* the
discriminator. Dependency weight is:

| option | cgo-free, 4/4 targets | binary (linux/amd64, stripped) | Δ over empty binary | `go.sum` modules |
|---|:--:|---:|---:|---:|
| **flat file (stdlib only)** | **yes** | **1,660 KB** | **+112 KB** | **0** |
| `go.etcd.io/bbolt` | yes | 1,996 KB | +448 KB | 7 |
| `modernc.org/sqlite` | yes | 5,720 KB | +4,172 KB | 25 |
| `github.com/dgraph-io/badger/v4` | yes | 8,236 KB | +6,688 KB | 20 |
| `github.com/cockroachdb/pebble` | yes | 13,320 KB | +11,772 KB | 46 |

(Empty Go binary floor on this toolchain: 1,548 KB.)

Pebble would grow the binary **~8×** and add 46 modules to deliver a log-structured merge tree to a
workload that never does a point lookup. For a tool whose value proposition is that a hostile
stranger reads the source, that trade is backwards.

---

## Question 1 — dependency policy for small auditable Go crypto tooling

### 1.1 Does stdlib `crypto/sha3` provide legacy Keccak-256? **No.**

`crypto/sha3` landed in Go 1.24, imported from x/crypto
([golang/go#69982](https://github.com/golang/go/issues/69982)). Its package doc states the scope
precisely — FIPS 202 and nothing else:

> Package sha3 implements the SHA-3 hash algorithms and the SHAKE extendable output functions
> defined in FIPS 202.

— `$GOROOT/src/crypto/sha3/sha3.go`, mirrored at
[pkg.go.dev/crypto/sha3](https://pkg.go.dev/crypto/sha3).

Reading the source, the complete exported constructor set is `Sum224`, `Sum256`, `Sum384`,
`Sum512`, `SumSHAKE128`, `SumSHAKE256`, `New224`, `New256`, `New384`, `New512`, `NewSHAKE128`,
`NewSHAKE256`, `NewCSHAKE128`, `NewCSHAKE256`. **There is no `NewLegacyKeccak*` and no way to
select the padding byte.**

This is a padding difference, not a parameter difference, so it cannot be worked around from
outside the package. Go's internal FIPS module spells out the four domain-separation bytes:

```go
dsbyteSHA3   = 0b00000110  // 0x06  FIPS-202 SHA-3
dsbyteKeccak = 0b00000001  // 0x01  legacy Keccak (Ethereum)
dsbyteShake  = 0b00011111  // 0x1f
dsbyteCShake = 0b00000100  // 0x04
```

— `$GOROOT/src/crypto/internal/fips140/sha3/hashes.go`, mirrored at
[go.googlesource.com/go/+/refs/heads/master/src/crypto/internal/fips140/sha3/hashes.go](https://go.googlesource.com/go/+/refs/heads/master/src/crypto/internal/fips140/sha3/hashes.go).

The frustrating part: that same internal file **does** define working `NewLegacyKeccak256` and
`NewLegacyKeccak512`. They are simply not re-exported through `crypto/sha3`, and
`crypto/internal/...` is unimportable by external modules.

There is an **open, undecided proposal** to export them:
[golang/go#75486 — *proposal: crypto/sha3: provide NewLegacyKeccak256 and NewLegacyKeccak512*](https://github.com/golang/go/issues/75486).
As of this writing it carries the labels `LibraryProposal`, `Proposal`, `Proposal-Crypto` and sits
in the `Proposal` milestone with **no acceptance decision and no target release**. The earlier
requests are [golang/go#19709](https://github.com/golang/go/issues/19709) and
[golang/go#29533](https://github.com/golang/go/issues/29533). **Do not plan around this landing.**

### 1.2 `x/crypto/sha3` is now a wrapper — and that helps you

The change is real and recent. The upstream commit is
[`cf29fa96f8b6` — *"sha3: make it mostly a wrapper around crypto/sha3"*, 2025-10-08](https://github.com/golang/crypto/commit/cf29fa96f8b6),
reviewed as [CL 681755](https://go-review.googlesource.com/c/crypto/+/681755).

Its package doc now says ([golang/crypto master `sha3/hashes.go`](https://github.com/golang/crypto/blob/master/sha3/hashes.go)):

> Most of this package is a wrapper around the crypto/sha3 package in the standard library.
> The only exception is the legacy Keccak hash functions.

And [`sha3/legacy_hash.go`](https://github.com/golang/crypto/blob/master/sha3/legacy_hash.go):

> This implementation is only used for NewLegacyKeccak256 and NewLegacyKeccak512, which are not
> implemented by crypto/sha3. All other functions in this package are wrappers around crypto/sha3.

I bisected the module cache to pin exactly when this happened:

| x/crypto | `go` directive | `sha3` is a stdlib wrapper? | non-test files in `sha3/` |
|---|---|:--:|---|
| v0.43.0 | go 1.24.0 | **no** | `doc.go hashes.go hashes_noasm.go keccakf_amd64.go keccakf.go sha3.go sha3_s390x.go shake.go shake_noasm.go` |
| **v0.44.0** | go 1.24.0 | **yes** | `hashes.go legacy_hash.go legacy_keccakf.go shake.go` |
| v0.57.0 (latest) | go 1.26.0 | yes | `hashes.go legacy_hash.go legacy_keccakf.go shake.go` |

**The transition is v0.43.0 → v0.44.0.** Note the collapse: the hand-written SHA-3 core and the
amd64/s390x assembly paths are gone, replaced by stdlib delegation. What remains is a
self-contained pure-Go Keccak.

### 1.3 What the Keccak dependency actually costs — measured

This is the whole bill for `golang.org/x/crypto/sha3.NewLegacyKeccak256`:

**Modules: 2.** After `go mod tidy` on a program that calls only `NewLegacyKeccak256`, `go.sum`
contains exactly `golang.org/x/crypto` and `golang.org/x/sys`. (`go list -m all` additionally
*lists* `golang.org/x/net`, `x/term`, `x/text` because they appear in x/crypto's own `go.mod`
for other packages, but Go 1.17+ module-graph pruning means they are never downloaded, built, or
linked — they have no `go.sum` entry.)

**Linked non-stdlib packages: 2.** `go list -deps` reports only `golang.org/x/crypto/sha3` and
`golang.org/x/sys/cpu`. And `x/sys/cpu` is used for exactly one thing — two references to
`cpu.IsBigEndian` in `legacy_hash.go`.

**Binary: +16 KB.** Identical build flags (`CGO_ENABLED=0 -trimpath -ldflags="-s -w"`),
linux/amd64:

| program | binary |
|---|---:|
| stdlib `crypto/sha3` only | 1,650,850 B |
| `+ x/crypto/sha3.NewLegacyKeccak256` | 1,667,234 B |
| **difference** | **16,384 B (16 KB)** |

**Audit surface: 679 lines.** The `sha3` package is 893 non-test lines, but `hashes.go` (95) and
`shake.go` (119) are trivial stdlib pass-throughs. The code a reviewer must actually read is
`legacy_hash.go` (263) + `legacy_keccakf.go` (416) = **679 lines** of dependency-free Keccak
(it imports only `crypto/subtle`, `encoding/binary`, `errors`, `hash`, `math/bits`, `unsafe`,
and `x/sys/cpu`).

**Cross-compilation: clean.** All four targets, `CGO_ENABLED=0`:

```
linux/amd64  OK 1,667,234 B      darwin/amd64  OK 1,784,960 B
linux/arm64  OK 1,704,098 B      darwin/arm64  OK 1,708,162 B
```

**Correctness spot-check.** The built binary produces
`c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470` for `keccak256("")`, which is
the well-known Ethereum empty-input Keccak hash.

**One caveat worth planning around:** x/crypto v0.57.0 declares `go 1.26.0`, so pinning latest
forces a Go ≥ 1.26 toolchain. The `go` directive has escalated quickly (v0.43 `1.24.0` →
v0.49 `1.25.0` → v0.57 `1.26.0`). If you want a wider build-toolchain window, pin an older
x/crypto — **v0.44.0 is the sweet spot**: it is already the stdlib-wrapper form yet only requires
Go 1.24. There is precedent for this friction biting people:
[golang/go#68147](https://github.com/golang/go/issues/68147).

> **The alternative — vendoring ~150 lines of Keccak yourself — is defensible but I do not
> recommend it.** You would be hand-maintaining a cryptographic primitive to save 16 KB and one
> module, and you would lose the Go security team's maintenance. The x/crypto dependency is
> `golang.org/x/*`, i.e. the same trust root as the toolchain itself. If you want to shrink the
> trust story further, vendor x/crypto's two Keccak files *verbatim with attribution* rather than
> writing your own.

### 1.4 The cost of go-ethereum — measured

geth v1.17.5 `go.mod` declares **79 direct requires**; current master
([ethereum/go-ethereum `go.mod`](https://github.com/ethereum/go-ethereum/blob/master/go.mod))
has **166 `require` lines — 82 direct, 84 indirect** — plus 3 `tool` directives. The direct list
includes `Azure/azure-sdk-for-go`, four `aws-sdk-go-v2` modules, `cloudflare/cloudflare-go`,
`dop251/goja` (a JavaScript interpreter), `graph-gophers/graphql-go`, `influxdata/influxdb-client-go`,
six OpenTelemetry modules, `huin/goupnp` and `pion/stun`. Several are cgo-flavoured or heavyweight:
`github.com/ethereum/c-kzg-4844/v2`, `github.com/ethereum/hid`, `github.com/shirou/gopsutil`,
`github.com/DataDog/zstd`. Notably its closure also includes
`github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime` — a zkVM runtime that ends up in the
dependency graph of anything importing `accounts/abi`. For a project whose pitch is
"read all of it", that is a meaningful supply-chain surface.

Measured, same flags, geth v1.17.5:

| import | `go.sum` modules | `go list -m all` | binary (linux/amd64) | CGO=0 cross-compile |
|---|---:|---:|---:|:--:|
| `accounts/abi` alone | 11 | 165 | 3,876 KB | 4/4 OK |
| `ethclient` (JSON-RPC) | **103** | 175 | **8,440 KB** | 4/4 OK |

**Can you import `accounts/abi` standalone? Yes, and it is cheaper than folklore suggests** — but
it is not isolated. `go list -deps` shows it drags in the geth packages
`common`, `common/hexutil`, `common/math`, `crypto`, `crypto/keccak`, `crypto/secp256k1`,
`rlp`, and `rlp/internal/rlpstruct`. The `crypto`/`secp256k1` pull-in happens because ABI needs
Keccak for function/event selectors, and geth's `crypto` package is the one that provides it.

**It does not, however, require cgo.** geth uses the standard build-tag split:

```go
// crypto/signature_cgo.go
//go:build !nacl && !js && !wasip1 && cgo && !gofuzz && !tinygo

// crypto/signature_nocgo.go
//go:build nacl || js || wasip1 || !cgo || gofuzz || tinygo
```

Under `CGO_ENABLED=0` the `nocgo` path selects the pure-Go
`github.com/decred/dcrd/dcrec/secp256k1/v4`, which is why all four targets built cleanly. So the
"geth needs cgo" concern is **outdated** — it is *not* a valid reason to reject geth today.

Three corrections to commonly-held beliefs here, all verified:

- **The pure-Go fallback is decred's secp256k1, not btcec.** There is no `btcsuite/btcd/btcec`
  dependency in go-ethereum's `go.mod` at all. (`btcec` is what *ethgo* and *ethkit* use.)
- **`github.com/karalabe/hid` and `karalabe/usb` are gone.** `karalabe/hid` is archived upstream;
  geth now uses `github.com/ethereum/hid`, a fork. It is still cgo with vendored C
  (`#cgo CFLAGS: -I./hidapi/hidapi`, `-framework IOKit` on darwin) but it is reached **only** via
  `accounts/usbwallet`, with a `!cgo` fallback file. `cloudflare/cbrotli` is not present either.
- **The cgo KZG path is opt-in.** `crypto/kzg4844/kzg4844_ckzg_cgo.go` is gated on
  `//go:build ckzg && ... && cgo`; by default geth uses the pure-Go `crate-crypto/go-eth-kzg`.
  `supranational/blst` and `gballet/go-libpcsclite` are cgo but are similarly out of the path.

Under `CGO_ENABLED=1`, however, importing `accounts/abi` **does** compile geth's vendored
libsecp256k1 (`crypto/secp256k1/`, which contains `#include "./libsecp256k1/src/secp256k1.c"` and
is **5.5 MB** of the module cache on its own) — pure dead weight for a tool that never signs
anything, and it produces a *dynamically* linked binary. Since constraint (a) fixes
`CGO_ENABLED=0`, this is avoided, but it is a sharp edge for anyone building without that flag.

One more `go mod tidy` oddity worth knowing: `crypto/keccak_ziren.go` is gated `//go:build ziren`,
yet because `tidy` considers all build configurations, **`github.com/ProjectZKM/Ziren/...` is added
to your `go.mod` even though it is never compiled.** That is how a zkVM runtime ends up in the
dependency manifest of a tool that only decodes a `Transfer` event.

The valid reasons are the 103 modules on the RPC path, the ~2.3–6.9 MB of binary, the zkVM/KZG/
pyroscope/sentry/UPnP surface area in the closure, and the licence.

### 1.5 Licensing: LGPL-3.0 vs a static MIT binary

go-ethereum ships `COPYING` (GPL-3.0) and `COPYING.LESSER` (LGPL-3.0); library files including
`accounts/abi/abi.go` carry the LGPL-3.0 header
([ethereum/go-ethereum `accounts/abi/abi.go`](https://github.com/ethereum/go-ethereum/blob/master/accounts/abi/abi.go)):

> The go-ethereum library is free software: you can redistribute it and/or modify it under the
> terms of the GNU Lesser General Public License …

`nutz-verify` is MIT and distributes a **statically linked** binary. LGPL-3.0 §4 conditions
distribution of a "Combined Work" on giving recipients the means to relink against a modified
version of the library — straightforward with dynamic linking, awkward with a static Go binary
(it generally implies shipping object files or the corresponding source). Avoiding geth entirely
sidesteps the question and keeps the distribution story as simple as the product promise.

**This is a licensing observation, not legal advice, and the practical community interpretation of
LGPL + static Go linking is genuinely contested — I could not find an authoritative primary ruling.
Flagged as unverified.**

### 1.6 What you actually need instead

The tool needs two things, both small:

**(i) ABI decoding.** For a `Transfer(address indexed from, address indexed to, uint256 value)`
log, the "decoding" is fixed-offset slicing of 32-byte words — `topics[1][12:32]` and
`topics[2][12:32]` for the addresses, `data[0:32]` for the value. No dynamic types, no tuples, no
offsets. Selector/topic computation is one Keccak-256 of a canonical signature string. This is
tens of lines, not an ABI engine. Pull in a general ABI library only if you later need dynamic
types (`string`, `bytes`, arrays, tuples), where the offset-pointer rules genuinely are
error-prone.

**(ii) JSON-RPC.** `eth_getLogs` / `eth_getBlockByNumber` / `eth_call` over HTTP POST is
`encoding/json` + `net/http`, plus hex-quantity parsing (`0x`-prefixed, no leading zeros per the
[Ethereum JSON-RPC spec](https://ethereum.org/en/developers/docs/apis/json-rpc/)). Batch requests
are a JSON array. Zero modules.

**Auditability cuts the same way.** A reviewer can read 150 lines of your JSON-RPC and slicing
code. No reviewer reads 103 modules. The hand-rolled version is not a compromise here — it is the
more faithful expression of the product.

### 1.7 Lean alternatives to go-ethereum — surveyed

All figures below were produced by building probe programs of identical shape (ABI-encode
`balanceOf(address)`, verify the selector, plus an HTTP JSON-RPC call), Go 1.26.5, linux/amd64,
`CGO_ENABLED=0`, **unstripped** (so these are directly comparable to each other, but *larger* than
the stripped numbers quoted elsewhere in this document).

| approach | `go.sum` modules | non-std pkgs compiled | binary | cgo-free | maintained? |
|---|---:|---:|---:|:--:|---|
| **hand-rolled `net/http` + `encoding/json` + `x/crypto/sha3`** | **2** | **2** | **9,132,351 B** | yes | n/a |
| geth `accounts/abi` **alone** (no RPC) | 11 | 14 | 5,531,118 B | yes | yes |
| `umbracle/ethgo` (abi + jsonrpc) | 38 | 21 | 9,737,660 B | yes | **no — dead since 2024-11** |
| `defiweb/go-eth` (abi + rpc) | 20 | 43 | 11,218,617 B | yes | **yes — 2026-09-11** |
| geth `accounts/abi` + `ethclient` | 104 | 83 | 12,502,987 B | yes (CGO=0) | yes |
| `lmittmann/w3` (wraps geth) | 51 | 85 | 12,620,747 B | yes (CGO=0) | yes (dependabot-only since 2026-03) |

**`github.com/umbracle/ethgo` — do not use. It is unmaintained.** Default branch is `main` (not
`master`). Last commit on `main` is **2024-11-01**; latest tag **v0.1.3**
([umbracle/ethgo commits](https://github.com/umbracle/ethgo/commits/main)). That is roughly 22
months of silence as of 2026-09-14. Its README still claims *"Light and with a small number of
direct dependencies"* ([README](https://github.com/umbracle/ethgo#readme)), but its `go.mod` has
**17 direct requires** including `ory/dockertest`, `jmoiron/sqlx`, `lib/pq`, `go.etcd.io/bbolt`
and `btcsuite/btcd` declared as production requires
([go.mod](https://raw.githubusercontent.com/umbracle/ethgo/main/go.mod)). It also routes RPC
through `valyala/fasthttp` rather than `net/http`, and imports `google/gofuzz` in production code.
It does work and is cgo-free, but an unmaintained crypto-adjacent dependency is the opposite of
what this project needs.

**`github.com/lmittmann/w3` — the question's premise was wrong. It is not a geth alternative; it
depends on geth.** Its `go.mod` lists `github.com/ethereum/go-ethereum v1.17.1` as one of six
direct requires ([go.mod](https://raw.githubusercontent.com/lmittmann/w3/main/go.mod)), and its
README describes it as *"Closely linked to `go-ethereum`"* and *"an ergonomic wrapper"*
([README](https://github.com/lmittmann/w3#readme)). Its headline claim is batch-RPC speed (*"up to
80x faster requests"*), not leanness. Measured, it compiles **85** non-stdlib packages — 20 of them
geth — and produces a binary **~1 % larger than raw geth `ethclient`**. It buys ergonomics, not
dependency reduction. Maintenance is real but thin: the last commit on `main` (2026-03-11) was a
dependabot bump.

**`github.com/defiweb/go-eth` — the only maintained, genuinely geth-free ABI + RPC library I
found.** Last commit **2026-09-11** (three days before this writing)
([commits](https://github.com/defiweb/go-eth/commits/master)). Its `go.mod` has **9 direct
requires** and **no `ethereum/go-ethereum`**
([go.mod](https://raw.githubusercontent.com/defiweb/go-eth/master/go.mod)). Covers ABI
encode/decode (methods, events, errors), JSON-RPC including `eth_call`/`eth_getLogs`, HTTP/WS/IPC
transports, and wallets. cgo-free. Two caveats, both material:

- its `go.mod` declares **`go 1.26.0`**, forcing a bleeding-edge toolchain on every consumer;
- it has **16 stars**, and it shares geth's structural flaw — its `abi` package imports `types`,
  which pulls `crypto/kzg4844`, which pulls **12 `consensys/gnark-crypto` + 4 `go-kzg-4844`
  packages** for blob-transaction support you will never use. Even an ABI-only probe pulls them.

**Also checked and rejected:** `0xsequence/ethkit` (41 direct requires; a hard fork of geth's
internals, with `blst` and `c-kzg-4844` present — heaviest of the lot); `onrik/ethrpc` (tiny and
correctly shaped — 1 production dependency, `tidwall/gjson` — but **RPC only, no ABI, and
unmaintained since 2024-01-05**); `getamis/eth-client` (dead since 2017); `blocky/abi` (genuinely
zero production dependencies and ABI-only, but **archived by its owner** in 2025-11);
`cryptoddev/fastabi` (created and last pushed in the same minute, 0 stars — do not depend on it);
`omnes-tech/go-eth-abi` (7 stars, low signal).

**Reading of this table:** if you want a library rather than hand-rolled code, `defiweb/go-eth` is
the only defensible choice, and geth's `accounts/abi` alone is the only *cheaper* one. Neither beats
hand-rolling for this specific, tiny ABI surface.

### 1.8 What small auditable Go tools actually do — the honest answer

This is the part of the research that did **not** confirm the hypothesis, and it should be weighed
rather than filed away.

| tool | geth in `go.mod`? | detail |
|---|:--:|---|
| [`0xKiwi/go-merkle-distributor`](https://github.com/0xKiwi/go-merkle-distributor) | **yes** | A merkle-airdrop generator with **2 direct requires**, one of which is `geth v1.10.18`. |
| [`Bananapus/juicerkle`](https://github.com/Bananapus/juicerkle) | **yes** | 2 direct requires: `geth v1.13.13` + `mattn/go-sqlite3` (which *is* cgo). |
| [`Galactica-corp/merkle-proof-service`](https://github.com/Galactica-corp/merkle-proof-service) | **yes** | 19 direct / 150 total, `geth v1.13.15`. |
| [`flashbots/mev-boost`](https://github.com/flashbots/mev-boost) | **yes** | Imports exactly two geth packages (`common`, `common/hexutil`) — yet **24 geth packages compile in**, dragged by `attestantio/go-eth2-client` and `go-boost-utils`. |
| [`wealdtech/ethdo`](https://github.com/wealdtech/ethdo) | **no** | A serious Ethereum CLI with no geth — but it is consensus-layer only, so it never needs ABI. |
| [`drand/drand`](https://github.com/drand/drand) | **no** | 34 direct requires, **zero** Ethereum dependencies; its own `kyber`/`kyber-bls12381`. Its weight is gRPC/OTel service plumbing. |
| [`celestiaorg/celestia-node`](https://github.com/celestiaorg/celestia-node) | indirect only | `geth v1.17.0` appears once, marked `// indirect`. A source grep finds **zero** geth imports in celestia-node's own code; `go mod why` traces it through a Cosmos Hyperlane bridge module that wants **`common.Address`** — one type. |

Two patterns, both instructive:

1. **Tiny EL-touching tools pull all of geth, usually for one or two types.** `common.Address` and
   `hexutil` are the gravitational centre. This is convention, not engineering necessity.
2. **The tools that stay lean stay lean by not touching the EVM**, not by hand-rolling a lean EVM
   path. drand and ethdo are not counter-examples to the convention; they are simply outside it.

**So: I could not find primary-source precedent for the hand-rolled approach.** The recommendation
in the Bottom Line stands on the merits — 2 modules vs 103, an auditable-by-a-stranger product
thesis, and the LGPL question — but it is a deliberate departure from prevailing practice, and
should be adopted with that stated plainly rather than as "what serious projects do."

The mitigating fact is that `nutz-verify`'s Ethereum surface is unusually small: **one event
signature, three topics, one `uint256`, and a handful of `eth_getLogs`/`eth_getBlockByNumber`
calls.** The tools above that pull geth generally handle transactions, signing, RLP, or full block
types. If this tool's scope grows to those, revisit — and at that point take geth's
`accounts/abi` (11 modules), not `ethclient` (103).

---

## Question 2 — embedded storage for an append-only, sequentially-replayed event log

### 2.0 The workload, and what it rules out

```
(uint64 blockNumber, [32]byte blockHash, uint64 timestamp,
 [20]byte from, [20]byte to, [32]byte value)
```

= 8 + 32 + 8 + 20 + 20 + 32 = **120 bytes, fixed width, naturally 8-byte aligned.**

| records | raw (120 B) | with 4-byte CRC32C (124 B) |
|---:|---:|---:|
| 1 M | 0.12 GB | 0.12 GB |
| 10 M | 1.20 GB | 1.24 GB |
| 50 M | 6.00 GB | 6.20 GB |
| 100 M | 12.00 GB | 12.40 GB |

Properties that matter, and what each eliminates:

- **Fixed width.** Record *i* lives at offset `i × 124`. You get O(1) random access and binary
  search over block numbers *for free*, from arithmetic. A B-tree or LSM index would be
  reconstructing information the file layout already encodes.
- **Written once in block order.** The key is monotonically increasing. This is the single worst
  case for a B+tree's page-splitting behaviour and the case where an LSM's compaction is pure
  overhead — you never overwrite, so there is nothing to compact.
- **Full sequential replay only.** No point lookups, no range scans, no secondary indexes. Every
  read is `read(2)` straight down the file. This is the access pattern the OS readahead is built
  for.
- **Single writer, no concurrency.** Eliminates the entire reason MVCC, transactions, and lock
  managers exist.
- **Truncation at a fork point is the only mutation.** On a flat file this is literally
  `f.Truncate(n * 124)` — one syscall, atomic with respect to the file length. In a KV store it is
  a range delete followed by compaction, which is strictly more work and, in an LSM, leaves
  tombstones behind.

The last point is worth dwelling on: **the one mutation this workload has is the one operation a
flat file does better than any KV store.**

### 2.1 The candidates — measured, not assumed

All builds: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w"`, go1.26.5.

| option | pure Go / cgo-free | 4/4 cross-compile | binary (linux/amd64) | Δ over floor | `go.sum` mods | full graph |
|---|:--:|:--:|---:|---:|---:|---:|
| *(empty Go binary floor)* | — | — | 1,548 KB | — | 0 | 0 |
| **flat file** (`bufio`+`hash/crc32`+`encoding/binary`) | **yes** | **yes** | **1,660 KB** | **+112 KB** | **0** | **0** |
| `go.etcd.io/bbolt` v1.5.0 | yes | yes | 1,996 KB | +448 KB | 7 | 15 |
| `modernc.org/sqlite` | **yes** (verified) | **yes** (verified) | 5,720 KB | +4,172 KB | 25 | 25 |
| `github.com/dgraph-io/badger/v4` | yes | yes | 8,236 KB | +6,688 KB | 20 | 32 |
| `github.com/cockroachdb/pebble` | yes | yes | 13,320 KB | +11,772 KB | 46 | 126 |

Per-target sizes (KB, stripped):

| | linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64 |
|---|---:|---:|---:|---:|
| bbolt | 1,996 | 1,984 | 2,037 | 1,980 |
| modernc.org/sqlite | 5,720 | 5,696 | 5,922 | 5,835 |
| badger/v4 | 8,236 | 7,744 | 8,407 | 7,928 |
| pebble | 13,320 | 12,480 | 13,583 | 12,734 |

**The headline negative result: cgo is not a discriminator.** I expected `modernc.org/sqlite` to be
the problem child and it was not — it built cgo-free for all four targets. This is consistent with
its design: it is a pure-Go translation of the SQLite C source executed on `modernc.org/libc`
([modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)). **So reject these engines on weight
and fit, not on portability.** The earlier instinct that "sqlite means cgo" applies to
`github.com/mattn/go-sqlite3`, not to `modernc.org/sqlite`.

Caveats on that row, flagged honestly: `modernc.org/sqlite` supports a *specific enumerated* set of
GOOS/GOARCH pairs because its C-to-Go translation is generated per platform. All four targets you
care about are in that set — I verified by compiling, which is the strongest possible evidence —
but a *fifth* target would need checking, not assuming.

#### Notes per engine

**bbolt** — the only genuinely lightweight option: 7 modules in `go.sum`, and at link time only
`go.etcd.io/bbolt` (+ its `errors`, `internal/common`, `internal/freelist`) and
`golang.org/x/sys/unix`; the rest (`testify`, `go-spew`, `go-difflib`, `yaml.v3`, plus the `cobra`
CLI) are test/tooling-only. It is a single-file mmap'd B+tree with copy-on-write pages.

Its own README's *Caveats & Limitations* section
([etcd-io/bbolt](https://github.com/etcd-io/bbolt/blob/main/README.md#caveats--limitations))
contains **three disqualifying facts for this specific workload**, in bbolt's own words:

> Because of the way pages are laid out on disk, Bolt cannot truncate data files and return free
> pages back to the disk.

That is the killer. **The one mutation this workload has is truncation at a reorg, and bbolt
structurally cannot give the space back.** A flat file does it in one syscall.

> Bulk loading a lot of random writes into a new bucket can be slow as the page will not split
> until the transaction is committed. … Bolt is good for read intensive workloads. Sequential write
> performance is also fast but random writes can be slow.

> The data structures in the Bolt database are memory mapped so the data file will be endian
> specific.

The endianness note is a real portability wart for a tool shipping to four platforms: a bbolt file
written on one architecture is not guaranteed portable. A flat file with explicit
`encoding/binary.LittleEndian` framing is byte-identical everywhere — which also means a user can
diff two logs, or hand you one to reproduce a bug.

Note also: "Bolt uses an exclusive write lock on the database file so it cannot be shared by
multiple processes."

**pebble** — 46 modules in `go.sum` and **126** in the full graph, for an LSM tree; ~27–31 modules
are actually *linked*. Its closure includes `cockroachdb/errors`, `redact`, `swiss`, `crlib`,
`fifo`, `tokenbucket`, `getsentry/sentry-go`, `gogo/protobuf`, `google.golang.org/protobuf`, and
`prometheus/client_golang` (most dragged in by `cockroachdb/errors`). Its `go.mod` declares **34
direct requires**. cgo is **optional, not required**: `internal/compression/zstd_cgo.go`
(`//go:build cgo && !pebblegozstd`) uses `DataDog/zstd`, while `zstd_nocgo.go`
(`//go:build !cgo || pebblegozstd`) uses pure-Go `klauspost/compress/zstd`; `internal/manual`
splits the same way. There is even a `pebblegozstd` tag to force pure Go *with* cgo enabled.
Excellent engine; absurd fit for a log you replay start-to-finish.

**badger/v4** — 20 modules in `go.sum` (~15 linked), 8.2 MB. Value-log + LSM. `go.mod` has 11
direct requires; it pulls `google.golang.org/protobuf` **and OpenTelemetry** (`otel`, `otel/metric`,
`otel/trace`, `contrib/zpages`), though **not** gRPC. **The optional cgo zstd path is gone in v4**:
`y/zstd.go` imports `klauspost/compress/zstd` unconditionally with no build tags, and there is no
`import "C"` anywhere in the module. (The `DataDog/zstd`-behind-build-tags arrangement was real in
badger **v2/v3** — it no longer applies.)

**modernc.org/sqlite** — 25 modules in `go.sum`, ~9 actually linked (`libc`, `mathutil`, `memory`,
`dustin/go-humanize`, `google/uuid`, `remyoudompheng/bigfft`, `x/sys`). The alarming-looking
`modernc.org/cc`, `ccgo`, `gc` entries are build-list-only — they are the transpiler toolchain and
are never linked. The real cost is source bulk: the generated `lib/` directory is **56 MB across
277 files and 1,423,869 lines** of machine-translated C. For a project whose thesis is "you can
read all of it", that is the disqualifying number — not the dependency count.

### 2.2 The benchmark — flat file vs bbolt

I implemented both against the exact 120-byte record shape and measured write and full replay of
2,000,000 records on this machine (NVMe, warm page cache for replay). bbolt writes were batched
10,000 records per `Update` transaction with `FillPercent = 1.0`, which is bbolt's own documented
optimisation for sequential-key insertion — i.e. **bbolt was tuned in its favour.**

```
FLAT   write  2,000,000 recs:  0.17s   11,900,331 rec/s   file = 0.25 GB
FLAT   replay 2,000,000 recs:  0.06s   32,389,621 rec/s   (CRC32C verified on every record)

BBOLT  write  2,000,000 recs:  1.60s    1,249,125 rec/s   file = 0.30 GB
BBOLT  replay 2,000,000 recs:  0.04s   47,033,496 rec/s   (no per-record checksum)
```

**Write: the flat file is 9.5× faster.** This is not a tuning artifact; it is structural. The flat
file does one `write(2)` per 1 MB buffer. bbolt must allocate pages, split B+tree nodes as the
monotonic key stream pushes the rightmost path, and — because it is copy-on-write — rewrite every
interior page on the root-to-leaf path on each commit, then fsync twice per transaction (data
pages, then meta page).

**Disk: the flat file is 20 % smaller** (0.25 GB vs 0.30 GB) even at `FillPercent = 1.0`, because
of B+tree page overhead and bbolt's freelist. Extrapolated to 50 M records that is roughly
6.2 GB vs 7.4 GB.

**Replay: bbolt is nominally 1.45× faster, and this does not matter.** The flat file number
*includes* verifying a CRC32C over all 120 bytes of every record; bbolt's includes no integrity
check at all. Both numbers are 1–2 orders of magnitude faster than the JSON-RPC fetch that
produces the data. At 50 M records the flat replay extrapolates to ~1.5 s. Replay speed is not a
decision input.

Checksumming is free because Go's `hash/crc32` dispatches Castagnoli to hardware on both
architectures you ship: `crc32_amd64.go` uses the SSE 4.2 `CRC32` instruction
(`archAvailableCastagnoli() { return cpu.X86.HasSSE42 }`) and `crc32_arm64.go` uses the ARM64
`CRC32` instruction (`$GOROOT/src/hash/crc32/`, mirrored at
[pkg.go.dev/hash/crc32](https://pkg.go.dev/hash/crc32)). Use `crc32.Castagnoli` (`0x82f63b78`),
**not** `crc32.IEEE` — the stdlib docs note Castagnoli "has better error detection characteristics
than IEEE", and IEEE is the one without an arm64 hardware path.

> **A correctness hazard I hit while benchmarking, worth recording.** My first bbolt run produced a
> silently wrong checksum. Cause: I reused one `[]byte` buffer across `Put` calls within a
> transaction. bbolt's `Bucket.Put` documents — `bucket.go:450` in v1.5.0,
> [etcd-io/bbolt](https://github.com/etcd-io/bbolt/blob/main/bucket.go) — that
> **"Supplied value must remain valid for the life of the transaction."** It stores the slice by
> reference until commit, so every record in a batch silently took the *last* value written. No
> error, no panic; just wrong data. This is a fair illustration of the thesis: each dependency adds
> not only bytes but semantics you must know to use it correctly. The flat-file version has no such
> rule — `Write` copies.

### 2.3 The real argument: crash consistency, not speed

A flat file does not lose on throughput, footprint, or dependency count. The only place it can
lose is **durability and integrity**, and this is a legitimate concern rather than a hypothetical
one. Three specific failure modes:

1. **Torn / partial final record.** A crash mid-append leaves a fraction of a record. Worse, the
   file may be extended *before* the bytes land. SQLite's design documentation states the
   pessimistic assumption explicitly
   ([sqlite.org/atomiccommit.html](https://www.sqlite.org/atomiccommit.html)):

   > SQLite normally makes the pessimistic assumption that the file is first extended with invalid
   > "garbage" data and that afterwards the correct data replaces the garbage.

   So you cannot infer "record present" from file length alone.

2. **Torn sector.** SQLite also does not assume sector writes are atomic:

   > SQLite has traditionally assumed that a sector write is not atomic. However, SQLite does
   > always assume that a sector write is linear. … If a power failure occurs in the middle of a
   > sector write it might be that part of the sector was modified and another part was left
   > unchanged.

   Since a 124-byte record does not align to a 512 B or 4096 B sector, a single record can span a
   sector boundary and be half-updated.

3. **Silent bit rot** in a multi-gigabyte file that is read end-to-end and fed into a Merkle root.
   For this tool that is the scariest one: a flipped bit yields a MISMATCH with no explanation,
   which is *precisely* the failure that destroys trust in a verifier.

#### The minimal correct flat-file design

This is about 60 lines and closes all three:

- **Record framing.** Fixed width, so no length prefix is needed — that alone removes the classic
  framing bug. Layout: `[120-byte payload][4-byte CRC32C]` = 124 bytes. Constant stride keeps
  offset arithmetic exact.
- **Per-record CRC32C.** Covers the payload. Detects torn records, torn sectors, and bit rot in one
  mechanism. Verified on every replay at ~32 M rec/s, i.e. free. Prefer per-record over per-block
  checksums: it localises damage and lets you say exactly which block number is corrupt.
- **Truncate-to-last-valid-record on open.** Replay from the start (you do that anyway); on the
  first CRC failure or short read, `Truncate` to the end of the last good record. A partial tail
  write is self-healing, because the data is re-fetchable from the chain. **This is the key
  insight: your durability requirement is unusually weak, because the log is a cache of public
  data.** Anything lost is refetched. You need to detect damage, not survive it.
- **fsync policy.** You do *not* need `fsync` per record. I measured the policy curve (500,000
  records):

  | fsync policy | throughput |
  |---|---:|
  | at close only | 9.19 M rec/s |
  | every 1,000 records | 11.76 M rec/s |
  | every 10,000 records | 11.66 M rec/s |
  | every 100,000 records | 11.46 M rec/s |
  | **every record** | **1.77 M rec/s** |

  **Batched `fsync` is free; per-record `fsync` costs ~6.7×.** Sync on a checkpoint interval — every
  few thousand records or every few seconds — and on clean shutdown. On crash you lose the unsynced
  tail and refetch it. Go's `os.File.Sync` maps to `fsync(2)`, and to `F_FULLFSYNC` on macOS.
  *(Absolute numbers are device- and filesystem-dependent; the shape of the curve is the actionable
  part.)*
- **Manifest: not needed, and this is a real simplification.** A manifest normally records "how
  many records are valid". Here the CRC chain plus truncate-on-open *derives* that, and the record
  itself carries `blockNumber`, so the log is self-describing: the last valid record tells you
  where to resume. Optionally prepend a small fixed header (magic bytes, format version, chain id
  4663, contract address) so a file from the wrong chain or an incompatible version is rejected
  loudly instead of producing a confident wrong answer. That header is worth having — it is a
  correctness guard, not bookkeeping.
- **Reorg truncation.** Binary-search the fixed-stride file for the fork block (O(log n), no index
  needed), then `Truncate(i * 124)`. One syscall. `fsync` after.

#### Where a KV store would genuinely have helped

To be fair to the alternatives — a KV store would be the right answer if any of these became true:

- you needed **point lookups or range queries** by something other than position (e.g. "all
  transfers for address X") — that is a secondary index, and hand-rolling one is real work;
- you had **concurrent readers during writes** with transactional isolation;
- you needed **atomic multi-record commits** with rollback;
- the data were **updated in place** rather than append-only;
- the dataset **exceeded memory and required compaction/compression** of overwritten values.

**None of these hold.** If a future feature introduces the first one, revisit — bbolt would be the
proportionate answer at 7 modules, not pebble at 46.

### 2.4 What Ethereum-adjacent Go projects actually use

**go-ethereum: Pebble, and it has been the default longer than folklore suggests.**

The default is chosen in `node/database.go`, not `node/node.go`
([ethereum/go-ethereum `node/database.go`](https://github.com/ethereum/go-ethereum/blob/master/node/database.go)),
in `openKeyValueDatabase`:

```go
// No pre-existing database, no user-requested one either. Default to Pebble.
log.Info("Defaulting to pebble as the backing database")
return newPebbleDBDatabase(o.directory, o.Cache, o.Handles, o.MetricsNamespace, o.ReadOnly)
```

LevelDB is selected only via an explicit `--db.engine=leveldb` or when an existing LevelDB
directory is detected; `core/rawdb/database.go` distinguishes the three on-disk layouts by marker
files (`CURRENT` for leveldb, `marker.manifest.*` for pebble v2, `OPTIONS*` for both pebble majors).
All three modules are still direct requires in geth's `go.mod` — `cockroachdb/pebble v1.1.5`,
`cockroachdb/pebble/v2 v2.1.4`, and `syndtr/goleveldb`.

The timeline, from the release notes themselves:

| release | date | change |
|---|---|---|
| [v1.11.0](https://github.com/ethereum/go-ethereum/releases/tag/v1.11.0) | 2023-02-15 | Pebble **added**, opt-in via `--db.engine=pebble`. Stated rationale: goleveldb "is a one-person project where the maintainer has signaled that the project is not a priority." |
| **[v1.12.0](https://github.com/ethereum/go-ethereum/releases/tag/v1.12.0)** | **2023-05-25** | **Pebble becomes the default** for fresh databases (#27136) |
| v1.12.1 | 2023-08-10 | fsync enabled for pebble writes |
| v1.13.4 | 2023-10-17 | Pebble enabled on 32-bit platforms and OpenBSD |
| v1.16.0 | 2025-06-26 | Pebble synced to disk at explicit safepoints (after the v1.15.x no-fsync experiment was rolled back in v1.15.8) |
| [v1.17.5](https://github.com/ethereum/go-ethereum/releases/tag/v1.17.5) | 2026-07-27 | Pebble **v2** used for newly bootstrapped nodes; v1 retained for pre-existing DBs (#34009) |

Quoting v1.12.0 directly:

> Regarding our move from `leveldb` to `pebble`, Geth now defaults to use Pebble as a backend if no
> existing database is found (#27136). If a previous LevelDB database exists Geth will keep using
> that…

**The common "v1.13.x" recollection is off by one minor release: it was v1.12.0.**

**erigon: MDBX via cgo — and it breaks exactly the constraint this project has fixed.**

Erigon's `go.mod` ([erigontech/erigon](https://raw.githubusercontent.com/erigontech/erigon/main/go.mod))
has `github.com/erigontech/mdbx-go v0.43.0` as a direct require (the modern fork; `torquem-ch/mdbx-go`
no longer appears) and contains **no pebble and no goleveldb**. The architecture is documented in
erigon's own [`db/kv/Readme.md`](https://github.com/erigontech/erigon/blob/main/db/kv/Readme.md):
`erigontech/mdbx-go` → `ethdb/kv_mdbx.go` → the common `kv_interface.go`.

`mdbx-go` vendors ~3.2 MB of libmdbx C and compiles it with cgo (`#cgo !windows CFLAGS: …`,
`#cgo windows LDFLAGS: -lntdll`, `#cgo !android,linux LDFLAGS: -lrt`); 15 files do `import "C"`.
Verified behaviour: under `CGO_ENABLED=0` the `mdbx` package degrades to a single file and any real
use fails to compile (`undefined: mdbx.NewEnv`). Cross-compiling `CGO_ENABLED=1 GOOS=darwin
GOARCH=arm64` from linux/amd64 fails at `# runtime/cgo` without a real cross toolchain. Erigon's
[README](https://github.com/erigontech/erigon/blob/main/README.md) states the requirement plainly:
**"Toolchain: Go >= 1.26, GCC 10+ or Clang, 64-bit architecture."**

**This is the one candidate in the entire survey that would break constraint (a).** It is listed
here as a cautionary data point, not an option: if `nutz-verify` had followed erigon's lead it could
not ship a single static cross-compiled binary at all.

**What this says for `nutz-verify`: less than it appears.** geth and erigon are archival full nodes
storing the world state — hundreds of GB, random-access by hash, continuously mutated, concurrently
read. Their KV choice is driven by requirements this tool does not have. **Their precedent does not
transfer.** The relevant lesson is the negative one from erigon: a C-backed store is a one-way door
on static cross-compilation.

---

## Unverified / open questions

Flagged explicitly rather than presented as established:

1. **Whether legacy Keccak will ever land in stdlib.**
   [golang/go#75486](https://github.com/golang/go/issues/75486) is **open with no decision and no
   target release**. Do not plan around it. If it is accepted, the `x/crypto` dependency could later
   be dropped — a nice-to-have, not a plan.
2. **The LGPL-3.0 / static-Go-linking question is genuinely contested.** I found the licence facts
   (geth ships `COPYING.LESSER`; library files carry LGPL-3.0 headers) but **no authoritative ruling
   or FSF guidance specific to statically linked Go binaries**. Treat §1.5 as a reason to ask a
   lawyer if you ever reconsider geth, not as a settled legal conclusion. I am not qualified to give
   legal advice here.
3. **No primary-source precedent for the hand-rolled JSON-RPC approach.** Covered in §1.8. The
   recommendation is a first-principles argument, not an appeal to convention, and the survey found
   the convention runs the other way.
4. **`modernc.org/sqlite` has no maintainer-published supported-platforms table** in its current
   README. The platform list was derived from the generated-file set in `lib/` and confirmed by
   actually cross-compiling. That is stronger evidence than a table, but it is not a maintainer
   guarantee, and a *fifth* target would need to be tested rather than assumed.
5. **Benchmark numbers are single-machine, single-run.** One NVMe SSD, one filesystem, warm page
   cache on replay, no repetitions and no error bars. The ~9.5× write gap between flat file and
   bbolt is large enough that I am confident in its direction and rough magnitude; **the precise
   ratio should not be quoted as a benchmark result.** The fsync curve's absolute numbers are
   likewise device-dependent — only its shape is portable. I did not benchmark pebble, badger, or
   sqlite at all; they were rejected on dependency weight and workload fit, not measured
   throughput. bbolt's README gives only qualitative write guidance, no absolute figures.
6. **Extrapolations to 50 M records** (~4.2 s write, ~1.5 s replay, ~6.2 GB) are linear projections
   from the 2 M-record run. The flat-file case should extrapolate cleanly since it is pure
   sequential I/O; **the bbolt case probably degrades worse than linearly** as the B+tree deepens
   beyond page cache, meaning the measured gap is, if anything, a *lower* bound at scale. Not
   verified.
7. **Why pebble's `master` `go.mod` declares the v1 module path** while v2.x tags declare `/v2` —
   both facts confirmed, the branching scheme behind them was not.
8. **The badger ownership chain** (dgraph-io → hypermodeinc → redirecting back to dgraph-io, with an
   `Istari Digital, Inc.` copyright header in `y/zstd.go`) — the redirect and the header were
   confirmed; no primary announcement explaining the sequence was found. The module path
   `github.com/dgraph-io/badger/v4` has never changed.
9. **Erigon was not built end-to-end.** The cgo conclusion rests on `mdbx-go`'s verified build
   behaviour plus erigon's stated GCC/Clang requirement.
10. **geth direct-require counts differ by version** (79 in v1.17.5's first require block; 82 direct
    of 166 total on master). Both were counted mechanically; the discrepancy is version drift, not
    disagreement.

---

## Appendix A — how the numbers were measured

Every "measured here" figure is reproducible. Environment: `go1.26.5 linux/amd64`, NVMe SSD.

**Binary sizes.** Unless a table says otherwise, all binaries were built with identical flags:

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin .
```

Each probe is a fresh module containing a `main.go` that imports exactly one library and
references one symbol, followed by `go mod tidy`. The "empty Go binary floor" (1,548 KB) is a
program whose entire body is `fmt.Println("x")` — subtract it to get a library's true contribution.
The tables in [§1.7](#17-lean-alternatives-to-go-ethereum--surveyed) are the exception: those are
**unstripped** builds (no `-ldflags`), internally consistent but not comparable to the stripped
figures elsewhere.

**Cross-compilation.** Each probe was rebuilt with `GOOS`/`GOARCH` set for `linux/amd64`,
`linux/arm64`, `darwin/amd64`, `darwin/arm64`, all at `CGO_ENABLED=0`. "4/4 OK" means all four
linked successfully. Compiling is stronger evidence of portability than any support matrix.

**Dependency counts.** Two distinct numbers, which is why they differ so much:

- **`go.sum` modules** — `awk '{print $1}' go.sum | sort -u | wc -l`. These are the modules that are
  actually fetched, hashed, and available to the build. **This is the number that matters for
  auditability and supply-chain risk.**
- **full graph** — `go list -m all`. Includes modules named in dependencies' `go.mod` files that
  Go 1.17+ module-graph pruning never downloads or links. Larger, and mostly noise. Reported only
  to show the gap.

`go list -deps .` was used to enumerate actually-linked packages.

**Storage benchmark.** A single program writing and replaying the exact 120-byte record shape
(`uint64` block number, 32-byte block hash, `uint64` timestamp, two 20-byte addresses, 32-byte
value). Flat file: `bufio.Writer` with a 1 MB buffer, `[120-byte payload][4-byte CRC32C]` frames,
CRC verified on every record during replay. bbolt: 8-byte big-endian keys (so cursor order is
insertion order), 10,000 records per `Update` transaction, `FillPercent = 1.0` — bbolt's own
documented tuning for sequential-key insertion, i.e. **the comparison is tuned in bbolt's favour**.
Write timings are cold (fresh file); replay timings are warm (page cache), which flatters both
equally.

**A correctness note on the benchmark itself.** The first bbolt run returned a wrong checksum
because a single value buffer was reused across `Put` calls. This was a bug in the benchmark, not
in bbolt — `Bucket.Put` documents that the value must remain valid for the life of the
transaction — and it was fixed by allocating per record before the numbers above were taken. Both
implementations produce the identical expected checksum (`1999999000000` for 2 M records).

**Keccak correctness.** The x/crypto probe was checked against the known Ethereum value
`keccak256("") = c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470`.

**Version archaeology.** The x/crypto wrapper transition was pinned by downloading each release
into the module cache and inspecting the file list and imports of its `sha3/` directory directly,
rather than relying on changelogs.

