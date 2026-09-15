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
| `--chain` | assert the full Carry chain back to deploy, not just the previous Epoch; slow |
| `--artifacts <dir>` | a published `epochs/<id>/` bundle to diff against the Recompute; it can never influence one |
| `--json` | one JSON document, schema `nutz-verify-report/1`; the Signer's interface |

The Cache lives at `${XDG_CACHE_HOME:-~/.cache}/nutz-verify/<chainId>-<token>/`. A cold Cache
for a dense token takes weeks to build on the public endpoint and minutes to catch up once
warm; see [the spec](.scratch/verifier/spec.md#9-cache) for the measurements.

## Trust

The Verifier trusts its endpoints, and cannot not. Passing two independent providers turns
"trust your provider" into "trust that two providers are not colluding"; a local node is the
only true fix. The addresses it verifies against are pinned in
[`cmd/nutz-verify/deployment.go`](cmd/nutz-verify/deployment.go) and echoed in every header,
so what you are trusting is in front of you.

Build: `go build ./cmd/nutz-verify`. Standard library plus `golang.org/x/crypto/sha3`, nothing
else ([ADR-0004](docs/adr/0004-stdlib-only-no-geth.md)).
