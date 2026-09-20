# 15 — `--expect <root>`: judge a proposed Root before it is posted

Status: resolved
Type: task
Blocked by: —
Spec: nutz-platform `.scratch/keeper/spec.md` §5 (the Warm Signer), engineering spec §3.2

Written 2026-09-18 from the keeper grilling. The warm Signer of engineering spec §3.2 signs a
Root before it is posted, but `epoch` judges against the on-chain Root and "no Root posted
yet" is INDETERMINATE (ADR-0002). So today the Signer cannot sign on exit 0 at all: the
verdict it needs does not exist until after the signature it gates. The `--json` report does
carry `recomputed.root`, `totals` and `carryIn` regardless, so a wrapper could compare them
itself, but that moves the comparison, the part an attacker would swap, out of the pinned
public binary and into private code, which is exactly what the checksum pin exists to prevent.

- `--expect <root>` (name open) on `epoch`: when the chain has no Root for the Epoch, the
  proposed Root is the comparand for the Root Assertion; `totals` and the cap are asserted
  against the Recompute's own `totals` and the chain's `funded + carry`, `carryIn` against
  the chain, as they are now. MATCH means "my Recompute is what you propose and it fits the
  cap"; exit 0.
- With `--expect` and a Root already on chain: assert against the chain as today, and add a
  fifth line saying whether the expected Root equals the posted one; a differing expectation
  is MISMATCH.
- `--expect` on an Epoch not closed at the requested finality stays INDETERMINATE.
- The report gains `expected` beside `posted`, and the header echoes it: an unverifiable
  input shown, not buried.

Done when: a Case with a Root, run against a fake node holding no `RootPosted`, is MATCH with
`--expect <its root>` and MISMATCH with any other; with the Root posted, `--expect` of the
posted Root is MATCH and another is MISMATCH; an open Epoch is INDETERMINATE either way. In
nutz-platform, `cmd/signer` (keeper ticket 04) pins the release that carries it.

## Comments

**2026-09-20, built.** `--expect <root>` on `epoch`, refused on `latest` and `sync` and for the
zero hash; with `--chain` it is for the target only. `report.Assess` takes the Expectation as a
third argument and adds a fifth line, `expected`, whenever one was given: pass/fail against a
posted Root, n/a with none posted (it was the root line's comparand), fail against a Skipped
Epoch. `report.Run.Expected` and `report.Epoch.Expected` carry it in `--json`, and the text
header and the Epoch block echo it. Additive on `nutz-verify-report/1`; the fields the Signer
reads (`verdict`, `reason`, `run.chainId`, `run.distributor`, `epochs[].id`,
`epochs[].recomputed.{hasRoot,root,totals,carryIn}`) are unchanged, and
`TestRun_JSONKeepsTheFieldsTheSignerReads` decodes the `--expect` report into exactly that
shape. `chain.ParseHash` is exported for the flag. The four "Done when" cases are
`TestEpoch_ExpectJudgesARootBeforeItIsPosted`, `TestEpoch_ExpectAgainstAPostedRootIsAFifthLine`
and `TestEpoch_ExpectOnAnOpenEpochStaysIndeterminate`, with the pure cases in
`internal/report/assess_test.go` and an `epoch-expected` golden. One reading of the ask made
explicit: "`totals` and the cap against the Recompute and funded + carry" is the Recompute's
own `totals` shown on the line, the cap over the ledger's `funded` plus the expected `carryIn`,
and `carryIn` is the expected value with its provenance — there is no posted value to compare,
and the Signer compares the report's `recomputed` values with the request's. `CONTEXT.md` gains
**Expectation**; ADR-0002 is amended rather than superseded, since the four lines and the
three Verdicts stand and only "no Root posted yet" gains a way out.
