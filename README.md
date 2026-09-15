# nutz-verify

Recomputes any NUTZ Epoch from public chain data and reports a Verdict against the on-chain
Root. Nothing we publish is an input: every number comes from the RPC endpoints you name.

```
nutz-verify epoch <id> --rpc https://rpc.mainnet.chain.robinhood.com
nutz-verify latest     --rpc https://rpc.mainnet.chain.robinhood.com
nutz-verify sync       --rpc https://rpc.mainnet.chain.robinhood.com
```

A run prints its header — chain id, Distributor, token, the pinned `DEV_WALLET`, finality level,
endpoints, rate — then the Epoch with four Assertions, then the Verdict:

```
  root     ok    0xfab8…b7b0
  totals   ok    [1500 0 500 0 6]
  cap      ok    totals <= funded [1000 0 500 1 0] + carryIn [500 0 0 0 7] in every token
  carryIn  ok    [500 0 0 0 7] = carryOut of epoch 999

MATCH
```

| Verdict | Exit | Meaning |
|---|---|---|
| `MATCH` | 0 | the recomputed Root, `totals`, the cap and `carryIn` all agree with the chain |
| `MISMATCH` | 1 | at least one of the four does not; the line says which |
| `INDETERMINATE` | 2 | nothing could be checked: RPC error, Epoch not closed at the requested finality, endpoints disagreeing, no Root posted yet, Cache unusable |

**`2` is never `0`.** The warm Signer signs only on `0`; "could not check" is not "checked and
fine" ([ADR-0002](docs/adr/0002-rpc-only-and-what-match-asserts.md)).

## Flags

| Flag | |
|---|---|
| `--rpc <url>` | repeatable; every read is cross-checked and any disagreement is INDETERMINATE |
| `--finality latest\|safe\|finalized` | which tip history is read at; default `safe` |
| `--rate <n>` | JSON-RPC calls per second per endpoint; default 15, the public endpoint's budget. A keyed provider allows far more |
| `--fresh` | discard the Cache and rebuild it (the remedy for a Cache built for another history) |
| `--chain` | assert the full Carry chain back to deploy, not just the previous Epoch. The history is replayed once however many Epochs that is; the cost is the `RootPosted` read from deploy and a Root per rooted Epoch |
| `--artifacts <dir>` | a published `epochs/<id>/` bundle to diff against the Recompute; it can never influence one |
| `--json` | one JSON document, schema `nutz-verify-report/1`; the Signer's interface |

The Cache lives at `${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/`. A cold Cache
for a dense token takes weeks to build on the public endpoint and minutes to catch up once
warm; see [the spec](.scratch/verifier/spec.md#9-cache) for the measurements.

## Install

Download the binary for your platform and `SHA256SUMS` from the
[releases page](https://github.com/nutzwtf/nutz-verify/releases), and check the binary before
you run it:

```
sha256sum -c SHA256SUMS --ignore-missing     # shasum -a 256 -c on macOS
chmod +x nutz-verify_linux_amd64
```

**Pin it by checksum, not by path.** The warm Signer of engineering spec §3.2 holds one of the
three keys and signs a Root every hour only when this binary exits `0`. That Signer must run a
binary whose SHA-256 it has pinned and rechecks before every run, never "whatever is at
`/usr/local/bin/nutz-verify`". Otherwise swapping the binary on the Signer host is the cheapest
attack on the whole 2-of-3: no key is touched, and the replaced binary manufactures a MATCH for
any Root. The pin must be one of the published checksums, because those are the ones anyone can
reproduce (below), which is what makes "the code gating our signature is the code you can
audit" a checkable claim rather than a promise.

Static binary, no runtime, no config file, no account. Building from source is
`go build ./cmd/nutz-verify`: standard library plus `golang.org/x/crypto/sha3`, nothing else
([ADR-0004](docs/adr/0004-stdlib-only-no-geth.md)). Any Go from 1.24 builds it; `go.mod` also
names the newer toolchain releases are built with, which the Go command fetches unless you set
`GOTOOLCHAIN=local` to use your own.

## Reproducing a release

Every release is built by [`scripts/build-release.sh`](scripts/build-release.sh) on two
machines with different host OSes, and is published only if both produced identical checksums.
The script is the whole recipe: `CGO_ENABLED=0 go build -trimpath -buildvcs=false` with the
exact toolchain the `toolchain` line of `go.mod` names, downloaded if it is not the one on
your `PATH`, and with the host's Go environment shut out. So a build on your machine matches:

```
git checkout v1.2.3
scripts/build-release.sh
diff dist/SHA256SUMS <(curl -sL https://github.com/nutzwtf/nutz-verify/releases/download/v1.2.3/SHA256SUMS)
```

The script prints the toolchain it used. A checksum that does not match is either a different
toolchain (set `GOTOOLCHAIN` and it is honoured, at the cost of matching) or a release that was
not built from that tag, and the diff tells you which.

## What MATCH does not prove

A Verifier that oversells what it proves is worse than a narrow one
([ADR-0002](docs/adr/0002-rpc-only-and-what-match-asserts.md)), so this is the list.

- **It trusts its RPC endpoints, and cannot not.** Every input comes from the endpoints you
  name, and a lying endpoint gets a confident, wrong Verdict. Block-hash chaining does not
  help: there is no independent anchor to chain back to. Passing `--rpc` twice with genuinely
  independent providers makes any disagreement INDETERMINATE, which turns "trust your provider"
  into "trust that two providers are not colluding". It reduces the trust; it does not remove
  it. **A local node is the only true fix**: run your own chain 4663 node and point `--rpc` at
  it, and the Verifier trusts nobody you do not.
- **It trusts its pinned addresses.** The Distributor, token and `DEV_WALLET` are constants in
  [`cmd/nutz-verify/deployment.go`](cmd/nutz-verify/deployment.go) and echoed in every header,
  so what you are trusting is in front of you; check them against the addresses published on
  nutz.wtf. A MATCH against the wrong Distributor is a MATCH about nothing.
- **It does not judge the funding.** The cap Assertion bounds `totals` by what the Distributor
  recorded as funded plus Carry. Whether the ETH→USDG conversion behind that funding got a fair
  price is a different claim, needing historical quotes that are not deterministic, and is
  out of scope on purpose.
- **It does not prove the Root is Final.** MATCH says the arithmetic is right at the requested
  finality level. A Root can still be voided inside its Dispute window, and an Epoch below the
  requested finality is INDETERMINATE, not MATCH.
