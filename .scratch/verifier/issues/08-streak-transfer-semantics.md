# 08 — Confirm with `nutz-contracts` which `Transfer` logs reset a streak

Status: ready-for-agent
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
