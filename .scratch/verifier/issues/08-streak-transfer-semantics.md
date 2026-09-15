# 08 — Confirm with `nutz-contracts` which `Transfer` logs reset a streak

Status: resolved
Type: research
Spec: ../spec.md §5; engineering spec §4.3
Blocked by: —

Engineering spec §4.3 defines `lastSellAt[a]` as "the timestamp of the most recent
`Transfer(from = a)`, **whatever the destination**: sells, wallet-to-wallet transfers and burns
all reset the streak". Three log shapes are not obviously covered by that sentence, and ticket
02 resolved all three by taking it literally. Each reading is implemented and pinned by a Go
test in `internal/twab`, but **none is a committed Case**, deliberately: ADR-0003 makes a Case
normative for the private indexer, and publishing a reading the indexer disagrees with would
manufacture the divergence it exists to catch.

| Log | What ticket 02 implemented | The other reading |
|---|---|---|
| `Transfer(a, b, 0)` — zero value | resets the streak | ignore it: no balance moved, so nothing was sold |
| `Transfer(a, a, v)` — self-transfer | resets the streak | ignore it: economically a no-op |
| `Transfer(0x0, a, v)` — mint | sets `firstBuyAt` | only Converter purchases count as a buy |

## Why it matters more than it looks

A divergence here is not a rounding difference. If the indexer does not reset on a zero-value
transfer and the Verifier does, a wallet that made one gets 1.25x from them and 1.00x from us:
the weights differ, `W` differs, **every** Holder's allocation differs, and the Verifier reports
MISMATCH against a Root that is in fact correct. The warm Signer of engineering spec §3.2 signs
only on exit `0`, so a legitimate hourly Root stops being signed and the 2-of-3 degrades — the
precise failure spec §7 is built to avoid. Low probability, launch-blocking impact, and the fix
costs one question.

The literal reading is also the ungameable one, which is the argument to lead with: under any
"ignore it" reading a wallet can shuffle coins through a no-op transfer and keep a 1.50x
Multiplier it is not holding for. That asymmetry is worth putting to them directly rather than
asking neutrally.

## Done when

`nutz-contracts` confirms (or corrects) each of the three, engineering spec §4.3 is patched to
say so explicitly, and each confirmed reading gains a named Case in `testdata/cases/` so it
becomes normative for both implementations. If they correct one, `internal/twab` changes and the
Go test pinning it moves to a Case.

Related: the same round should settle §4.6's Seed — spec §11 notes it still reads
`keccak256(signature)` where the built contract stores `sha256(signature)`, and Acorn Draw work
is blocked on it.

## Answer

**2026-09-14 — researched; needs the spec owner's decision, because the research reverses
the ticket's premise for one of the three.** Everything below is checked against primary
sources, and the yes-path is prepared so the decision costs a read and a word.

### What the primary sources say

- **Engineering spec v0.5** (`nutz-contracts` at `1d29036`, 2026-09-14): §4.3 is unchanged
  from the quote above. §4.3 uses `firstBuyAt[a]` and **the spec never defines it** — §4.1
  and §4.2 define balances, TWAB and the Excluded set, nothing about what a buy is. That
  gap is the third row of the table, and it needs a sentence whichever way the row goes.
- **The token.** NUTZ will be a `PonsV2LauncherToken`, deployed by the Pons factory
  (`0x7eD5…EC7e`, Sourcify-verified source for chain 4663, fetched today): OpenZeppelin
  v5.5 `ERC20` + `ERC20Burnable`, **no override of `transferFrom`, `_update` or anything
  else**, the entire supply minted to the curve in the constructor, no mint function
  (whitepaper: "Supply fixed at launch… no mint function"; "the entire supply is minted to
  the bonding curve").
- **OpenZeppelin's `transferFrom` spends no allowance for a zero value**:
  `_spendAllowance` reverts only when `currentAllowance < value`, and `0 < 0` is false. So
  **any address can emit `Transfer(victim, x, 0)` for any `victim`**, and `burnFrom(victim, 0)`
  emits `Transfer(victim, 0x0, 0)` the same way. EIP-20 requires the event: "Transfers of 0
  values MUST be treated as normal transfers and fire the Transfer event."
- **Probe, compiled from the Sourcify source of `PonsV2LauncherToken` with Foundry** (the
  test is at the end of this answer): a stranger with zero allowance emits
  `Transfer(victim, stranger, 0)`, `Transfer(victim, 0x0, 0)` and `Transfer(victim, victim, 0)`;
  each costs about 43k gas; a one-wei `transferFrom` or `burnFrom` reverts as expected; the
  victim's own `transfer(victim, 7)` emits `Transfer(victim, victim, 7)` and leaves the
  balance unchanged. Five tests, five passes.

### The three readings

| Log | Ticket 02 implemented | Recommendation | Why |
|---|---|---|---|
| `Transfer(a, b, 0)` | resets the streak | **ignore it — in both directions** | Under the literal reading **anyone can reset any Holder's streak for 43k gas**: one bot, one `transferFrom(holder, bot, 0)` per Holder every six days, and every 1.50x Holder is held at 1.00x forever while the bot's own weight rises. Winter Mode would not survive its first week. The incoming side has a mirror: a zero-value receipt would set `firstBuyAt` 30 days before an address holds anything, so addresses could be pre-aged for free and bought into at 1.50x. Nothing moves in a zero-value log, so nothing was sold and nothing was bought. The public copy ("any outgoing transfer resets the streak") survives untouched: sending nothing is not sending. |
| `Transfer(a, a, v > 0)` | resets the streak | **keep** | Only `a`, or a spender `a` approved for at least `v`, can produce this log, and a spender with that allowance could take the coins instead, so there is no new surface. Ignoring it would add a special case that buys nothing and would invite "shuffle to yourself" as a streak-safe move the copy never promised. |
| `Transfer(0x0, a, v > 0)` | sets `firstBuyAt` | **keep, and define `firstBuyAt`** | Moot on chain: NUTZ has no mint function and its only mint goes to the curve, an Excluded address. But every hermetic Case funds itself with a mint, the indexer's CI runs those Cases, and the spec never says what a buy is. The definition that matches the code: the earliest incoming `Transfer` of non-zero value, whatever the source. |

The ticket's argument — "the literal reading is the ungameable one" — is right for the
self-transfer and backwards for the zero-value transfer: under the literal reading the game
is played *against* other Holders, and it is cheaper than any shuffle.

### Proposed patch to engineering spec §4.3 (`nutz-contracts/docs/spec/engineering-spec.md`)

> - `lastSellAt[a]` = timestamp of the most recent `Transfer(from = a, value > 0)`, **whatever
>   the destination**: sells, wallet-to-wallet transfers, self-transfers and burns all reset the
>   streak (no free reshuffling; document it publicly as "any outgoing transfer").
> - `firstBuyAt[a]` = timestamp of the earliest `Transfer(to = a, value > 0)`, whatever the
>   source: curve and pool buys, wallet-to-wallet receipts and mints alike.
> - **A `Transfer` of value 0 is ignored in both directions.** It moves nothing, and on an ERC-20
>   anyone can emit one from any address through `transferFrom` with no allowance, so counting
>   it would let a stranger reset any Holder's streak for the price of gas.

And one sentence on the `Streak` entry of `nutz-contracts/CONTEXT.md`: "A transfer of zero
value is not a transfer for this purpose." The whitepaper and website copy need no change.

### Prepared, not committed as normative

`.scratch/verifier/cases-draft/` holds one Case per row — `zero-value-transfer-keeps-the-streak`,
`self-transfer-resets-the-streak`, `mint-is-a-buy` — in the exact `testdata/cases` format,
expectations hand-derived and recorded in each `note`, roots filled by the same OpenZeppelin
hashing as `tooling/` (the script that filled them reproduces every committed root
byte-for-byte). Validated against the Go runner by copying them in temporarily:

| | Today's literal code | With `value == 0` skipped in `replayState.apply` |
|---|---|---|
| `self-transfer-resets-the-streak` | pass | pass |
| `mint-is-a-buy` | pass | pass |
| `zero-value-transfer-keeps-the-streak` | **fail** (250/375/375 as its note predicts) | pass |
| `TestReplay_AZeroValueTransferStillCountsAsASell` | pass | fail, as it should |

So the zero-value Case bites, and the change it asks for is three lines. Nothing in
`testdata/cases` or `internal/twab` changed in this ticket beyond a new Go test pinning the
self-transfer reading (the ticket claimed all three were pinned; that one was not) and a
corrected comment on the zero-value test, which no longer claims the literal reading cannot
be gamed.

### On a yes (one mechanical ticket)

1. In `replayState.apply`, after the nil/negative checks: `if tr.Value.Sign() == 0 { return nil }`.
   The Ledger shares that method, so `--chain` follows.
2. `git mv` the three drafts into `testdata/cases/`, add the names to `requiredCases` in
   `internal/alloc/cases_test.go`, add three rows to the README's "What each Case pins".
3. Delete `TestReplay_AZeroValueTransferStillCountsAsASell` and
   `TestReplay_ASelfTransferStillCountsAsASell` (the Cases replace them); reword the
   `Holder.FirstBuyAt`/`LastSellAt` doc comment and the zero-value line in
   `TestLedger_AgreesWithReplayAcrossEpochs` (that transfer stays in the history; it now pins
   the Ledger agreeing with Replay that it is a no-op).
4. Patch §4.3 and `CONTEXT.md` in `nutz-contracts` as above; `nutz-platform`'s copy of the
   spec is still v0.4 and needs the same patch plus the v0.5 sync.

If the zero-value row is decided the other way, only that draft's `expected` changes
(250 / 375 / 375, `W` unchanged at 4,000,000) and its note swaps sides; the gate is not added
and the Go test moves to that Case as-is. Either way the Case is what makes the reading
normative for both implementations (ADR-0003).

### Related: the Seed

Already resolved on their side: `1d29036` ("fix: spec §4.6 seed is sha256…") made §4.6
read `sha256(signature)`, matching `NutzDraw.fulfill`. Spec §11 here now says so;
`nutz-platform/docs/spec/engineering-spec.md` is still v0.4 and still says `keccak256`.

### Noticed, out of scope

Under every reading, an address that holds one wei for 30 days and then buys earns 1.50x
on the whole position from that hour, because "adding to a position does not reset" is the
whitepaper's rule. That is a design consequence rather than an ambiguity, and pre-aging a
set of addresses costs dust; it is recorded here so the spec owner has seen it, not as a
request to change it.

### The probe

Foundry project in a scratch directory, `foundry.toml` remapping `forge-std/` and
`@openzeppelin/contracts/` to `nutz-contracts/lib` (OpenZeppelin v5.7.0, one minor above the
token's v5.5, same `transferFrom`), `src/PonsV2LauncherToken.sol` copied verbatim from the
Sourcify source bundle of the factory. Run from `nutz-contracts` so mise picks up forge 1.8.1.

```solidity
contract PonsTokenProbe is Test {
    event Transfer(address indexed from, address indexed to, uint256 value);
    address constant victim = address(0xA11CE);
    address constant stranger = address(0xBAD);
    PonsV2LauncherToken t;

    function setUp() public {
        // This contract plays the curve, so it receives the supply and hands the victim a balance.
        t = new PonsV2LauncherToken("NUTZ", "NUTZ", "", "", PonsV2LauncherToken.Socials("", "", "", "", ""),
            address(0xDE), address(this), address(0xFA), 1e9 ether);
        t.transfer(victim, 1000 ether);
        assertEq(t.allowance(victim, stranger), 0);
    }
    function test_strangerEmitsZeroValueTransferFromVictim() public {
        vm.prank(stranger);
        vm.expectEmit(true, true, false, true);
        emit Transfer(victim, stranger, 0);
        t.transferFrom(victim, stranger, 0);
    }
    function test_strangerEmitsZeroValueBurnFromVictim() public {
        vm.prank(stranger);
        vm.expectEmit(true, true, false, true);
        emit Transfer(victim, address(0), 0);
        t.burnFrom(victim, 0);
    }
    function test_oneWeiFromVictimReverts() public {
        vm.prank(stranger);
        vm.expectRevert();
        t.transferFrom(victim, stranger, 1);
    }
}
```

## Comments

**2026-09-15 — decided and landed.** Eduar chose option A: a zero-value `Transfer` is ignored in
both directions; self-transfers of non-zero value reset the streak; a mint is a first buy and
`firstBuyAt` is now defined. What changed:

- `internal/twab`: `replayState.apply` returns before touching any state when `value == 0`
  (the Ledger shares it, so `--chain` follows). The two Go tests that pinned the zero-value
  and self-transfer readings are gone; the three Cases replace them and are in
  `requiredCases`. Done test-first: the promoted zero-value Case failed against the old code
  with exactly 250/375/375, then passed with the gate; the other two passed throughout.
- `testdata/cases/`: `zero-value-transfer-keeps-the-streak`, `self-transfer-resets-the-streak`,
  `mint-is-a-buy`, with README rows and a line in the `transfers` field description saying
  a value of 0 is a legitimate no-op entry.
- `nutz-contracts`: engineering spec §4.3 patched as proposed (v0.5.1, changelog entry),
  `CONTEXT.md` Streak entry gains "A transfer of zero value is not a transfer for this
  purpose." No contract changes: the streak is an off-chain rule and the token is Pons's.
- `nutz-platform`: its copy of the engineering spec synced from v0.4 to v0.5.1.
- Spec §5 here says the same in one sentence.
