# Cases

A **Case** is one hermetic allocation fixture: an Epoch's inputs, and the Allocations,
`totals` and Root they must produce. No RPC, no Cache, no chain access of any kind — a Case
is a JSON file and an integer-arithmetic problem.

ADR-0003 makes the allocation rules of engineering spec §4.1–4.5 normative **in this repo**:
if the private indexer disagrees with what is written here, the indexer is the bug. This
directory is the operational form of that claim. Go runs it as a table test in
`internal/alloc` and again through the Engine in `epoch`, and **the private indexer links
this module at a pinned tag**, so a rule lands here as a Case and reaches the indexer as a
version bump (ADR-0006). The format below is still an interface, not internal scaffolding:
a Case is what a reviewer, and the spec's owner, reads a rule from. Renaming or
removing a field breaks that consumer. Adding one is safe for the consumer but not for the Go
runner, which rejects unknown fields on purpose — a mistyped `carryout` would otherwise pass
as a zero — so a new field must land in `testCase` in `internal/alloc/cases_test.go` in the
same change.

The format is a documented superset of the shape ADR-0003 fixes
(`{epochId, transfers[], excludedAt[], funded[5], carryIn[5]}` and
`expected:{allocations[], totals[5], root}`). `devWallet` is a necessary addition — `DEV_WALLET`
is an input to the rules, and a hermetic Case cannot read a constant the spec has not pinned.
`name` and `note` are enforced by the runner because an unreviewable Case is not a pin.
`expected.totalWeight` and `expected.carryOut` are diagnostics a consumer may ignore.

A rule change lands here first, as a Case. That is the only thing "normative" can mean in
practice.

## Format

```json
{
  "name": "buy-at-minute-59",
  "note": "…which reading this Case rules out, and what that reading would produce…",
  "epochId": 1000,
  "devWallet": "0x0000000000000000000000000000000000000000",
  "transfers":  [{ "timestamp": 3603540, "from": "0xcc…cc", "to": "0xa1…a1", "value": "3600" }],
  "excludedAt": [{ "timestamp": 0, "account": "0xcc…cc" }],
  "funded":  ["1000", "1000", "1000", "1000", "1000"],
  "carryIn": ["0", "0", "0", "0", "0"],
  "expected": {
    "totalWeight": "1200000",
    "allocations": [
      { "account": "0xa1…a1", "twab": "60", "multBps": 10000, "weight": "600000",
        "amounts": ["500", "500", "500", "500", "500"] }
    ],
    "totals":   ["1000", "1000", "1000", "1000", "1000"],
    "carryOut": ["0", "0", "0", "0", "0"],
    "root": "0x67ba1bcdd5491d157182c04d50eece1df0252c45c95bfa7178d01a9a7c20623e"
  }
}
```

**Every integer is a decimal string**, including the ones that comfortably fit a double. A
`uint256` does not survive a JSON number, and a format where some amounts are numbers and
others strings is a format that will eventually be read wrong.

| Field | Meaning |
|---|---|
| `name` | Must equal the filename without `.json`. |
| `note` | Prose, and load-bearing: it records the arithmetic and **what the wrong reading would produce**. A Case with no note cannot be reviewed, and the Go runner fails on an empty one. |
| `epochId` | Epoch `e`. The window is `[3600e, 3600(e+1))` and is derived, never stated. |
| `devWallet` | The pinned `DEV_WALLET`, flat 1.00x however long its streak. The zero address where a Case has no dev wallet — a Case must be hermetic, and the real constant is a CLI concern. |
| `transfers` | The whole NUTZ `Transfer(from, to, value)` history **from the token's creation block**, not just this Epoch's. Entries before the window establish opening balances; a history that does not reach back far enough drives a balance negative and is rejected rather than computed from. `from`/`to` of the zero address are the mint and burn counterparty, which is never a Holder. A `value` of `0` is a legitimate entry that changes nothing: neither balance nor streak. **Order is not significant, including within a single block**: balances are replayed in timestamp order and judged only once every transfer sharing a timestamp has been applied, so a wallet that receives and forwards in the same block is not mistaken for a broken history in whichever order the logs arrive. |
| `excludedAt` | The whole `ExcludedAppended` log stream as `{account, timestamp}` — **not** a resolved set. The Epoch's Excluded set is every entry with `timestamp < 3600(e+1)`, and an entry applies to the *whole* Epoch containing its block, so an append at minute 30 zeroes the address for the entire hour. Resolving the set in the Case would move that rule out of what the Cases pin. Order is not significant. |
| `funded`, `carryIn` | Five values each, in the order `[SPY, NVDA, MU, SPCX, USDG]`, in each token's own decimals. |

### `expected`

| Field | Meaning |
|---|---|
| `allocations` | Exactly the tree's claims, **sorted by `account` ascending** — so a diff against an indexer artifact is stable, and so a Holder wrongly present or absent shows as a one-line diff. A Holder omitted by the omission rule does not appear. |
| `allocations[].twab`, `multBps` | Engineering spec §4.5 publishes both in `allocations.json`, and a MISMATCH is far easier to place when the intermediate values are pinned rather than only their product. |
| `allocations[].weight`, `totalWeight` | `TWAB × multBps` and `W`, carried **undivided**. A wrong `W` is the likeliest divergence between two implementations and is invisible in `totals` whenever it scales every Holder equally. A consumer that does not track them may ignore both. |
| `totals` | `Σ alloc(a,t)`. |
| `carryOut` | `funded + carryIn − totals`: the rounding dust that stays in the Distributor for the next Root. Derivable, and pinned anyway because the Carry chain is one of the four Assertions. |
| `root` | **`null` exactly when the Epoch posts no Root** — spec §5's `W == 0`, or every allocation flooring to zero. That is a legitimate state, not an error: the Verifier reports MATCH for a funded Epoch with no Root, never INDETERMINATE. |

## What each Case pins

Every Case is built so the wrong reading gives a *visibly* different answer, and its `note`
says which answer that is. Deleting one narrows what the rules are pinned to, so
`internal/alloc` keeps the list below as literals and fails if a name goes missing.

| Case | The resolution it pins | The wrong reading it rules out |
|---|---|---|
| `buy-at-minute-59` | TWAB integrates over sub-intervals; and, through the relay that forwards Alice's buy in the same block, that log order within a block is not part of the input | crediting a late buy for the whole hour: 983/16 instead of 500/500; or reading the relay's momentary negative balance as a broken history |
| `sell-at-minute-1` | the same, at the other end of the hour | ignoring the sale: 983/16 again |
| `transfers-within-one-hour` | **sum first, divide once** | flooring per term: Alice 3498 rather than 3500, and 499/500 for having traded six times |
| `window-closes-at-the-boundary` | the final sub-interval runs to `3600(e+1)` | closing at the last transfer in the Epoch: 1000/0 instead of 750/250 |
| `streak-boundaries` | `streakDays` uses that same boundary, and the 7- and 30-day steps are inclusive | measuring to the last transfer: 222/222/277/277 instead of 200/250/250/300 |
| `weight-carries-bps` | `weight = TWAB × multBps`, never divided down | dividing the 10000 out first: 75/25 instead of 78/21, moving the floor loss off the dust and onto the Holders |
| `funding-plus-carry` | `base[t] = funded[t] + carryIn[t]` | allocating over `funded` alone, stranding the previous Epoch's Carry for good |
| `dev-wallet-flat` | `mult(DEV_WALLET) = 1.00` always | letting its 41-day streak promote it: 125/125 instead of 100/150 |
| `excluded-holder` | Excluded balances are zero, and an entry covers its whole Epoch | counting them at all, or applying a mid-Epoch entry only from its block: 99 of 100 to the Excluded address |
| `omission-last` | a Holder leaves the tree only when all five allocations floor to zero, and `W` counted it either way | omitting on the first zero token, or recomputing `W` over the survivors: 999,999 instead of 999,998 |
| `no-eligible-holders` | `W == 0` is a legitimate no-Root state | raising an error, or posting a Root over an empty tree |
| `zero-value-transfer-keeps-the-streak` | a `Transfer` of value 0 is neither a sell nor a buy: on the real token anyone can emit one from any address, so counting it would let a stranger reset any Holder's streak | resetting on it, and starting a streak on a zero-value receipt: 250/375/375 instead of 375/375/250 |
| `self-transfer-resets-the-streak` | `Transfer(a, a, v > 0)` is an outgoing transfer like any other | ignoring it as an economic no-op: 500/500 instead of 400/600 |
| `mint-is-a-buy` | `firstBuyAt` is the earliest incoming transfer of non-zero value, whatever the source, a mint included | leaving a minted Holder without a first buy: 545/454 or 444/555 instead of 500/500 |

### What the Cases cannot pin

A Case carries timestamps, never blocks. Spec §5 also fixes *which* blocks a Recompute reads
— "the end block is the last block with `timestamp < 3600(e+1)`" — and no hermetic fixture can
express that. A chain reader that stopped at the end block's timestamp instead of the Epoch
boundary would compute every Case here correctly and still be wrong on chain, so that half of
the resolution belongs to `chain` and its own tests.

Three of the TWAB Cases share the root `0x67ba…623e`, because all three end with the same
two allocations. That is the point of them — three different readings of the window converge
on one correct outcome — but it does mean the *allocations*, not the root, are what
distinguish those Cases.

## Provenance

**The expected Allocations, `totals`, `carryOut` and `totalWeight` are hand-authored**, worked
out from spec §5's formulas and recorded in each Case's `note` so a reviewer re-derives them
by eye. They are deliberately *not* generated: the rules are normative in Go (ADR-0003), and a
generator for them would be a second rules implementation free to drift from the first — and
an expectation produced by the code under test proves only that the code agrees with itself.

**`expected.root` is the exception.** Tree shape is normative in the Distributor and
OpenZeppelin `StandardMerkleTree` v1.0.8, not here (ADR-0003), so the root comes from
OpenZeppelin's own library via `tooling/`:

```sh
cd tooling && pnpm install && pnpm gen:case-roots
```

That script hashes the allocations a Case already declares and recomputes no rule. It is a
Node package kept deliberately outside the Go module: never run by `go test`, contributing
nothing to `go.mod`, `go.sum` or the shipped binary (ADR-0004, ADR-0005). Output is
deterministic, so a regeneration that changes a committed root is a real signal.

## Adding a Case

1. Write the JSON with `"root": null`, deriving the expected values by hand and recording the
   arithmetic and the competing reading in `note`.
2. Run `pnpm gen:case-roots` to fill in the root.
3. `go test ./internal/alloc/` — the runner discovers Cases by glob, so no Go changes are
   needed.
4. If the Case pins a spec §5 resolution, add its name to `requiredCases` in
   `internal/alloc/cases_test.go`.
5. Confirm it bites: break the rule in `internal/twab` or `internal/alloc` and check that
   this Case is among the failures. A Case that passes against a wrong implementation is
   costing maintenance and buying nothing.
