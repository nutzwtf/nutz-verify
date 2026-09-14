# 03 — Chain reader: JSON-RPC, log decoding, exclusion reconstruction

Status: resolved
Type: task
Spec: ../spec.md §4; ADR-0002, ADR-0004
Blocked by: 02

Implement `internal/chain` on `net/http` + `encoding/json`. No go-ethereum (ADR-0004). Four decode shapes, all known at compile time: `Transfer(address,address,uint256)` (2 topics + 1 data word), `RootPosted(uint8,uint256,bytes32,uint256[5],uint256[5])` (2 topics + 11 static data words), `ExcludedAppended(address)` (1 topic), and the one dynamic return, `excluded() → address[]`. Plus `ledger()`, block-by-number, and the `latest`/`safe`/`finalized` tags.

Page `eth_getLogs` in ≤ 2,000-block ranges (engineering spec §4.1: the public-RPC cap). Support repeatable `--rpc` with cross-check: any disagreement between endpoints is INDETERMINATE, never a silent pick.

Build the Excluded set **as of an Epoch** from the log stream alone: every `account` of an `ExcludedAppended` log whose block timestamp is `< 3600(e+1)`. No `excluded()` subtraction — the Distributor's constructor emits `ExcludedAppended` for every base entry (verified in `src/NutzDistributor.sol`), so the logs are self-sufficient. An entry applies to **the whole Epoch containing its block**, not from its block onward. Also expose `keccak256(abi.encodePacked(set))` over the ascending, de-duplicated addresses — that is engineering spec §4.5's `exclusion set hash` and makes artifact cross-checking free.

Tests: anvil fork of 4663 running `nutz-contracts/script/Deploy.s.sol`, a stand-in ERC-20 for NUTZ, transfers and `postRoot` driven from the harness, every decoder asserted against real node output. This harness is what justifies hand-rolling the decoding at all (ADR-0004), so it is part of this ticket and not a follow-up. Include an exclusion appended mid-Epoch and assert the set reconstructed for an earlier Epoch does not contain it.

## Comments

**2026-09-14 — implemented.**

`internal/chain` reads the chain over `net/http` and `encoding/json`: a cross-checking `Reader`,
the four decode shapes, `ledger()`, block headers, the three finality tags, `eth_getLogs` paging,
and the Excluded-set reconstruction with engineering spec §4.5's set hash. `go.mod` and `go.sum`
are untouched — the dependency tree is still the two modules ADR-0004 allows, and the only import
outside the standard library is `x/crypto/sha3` for keccak.

**The anvil harness is the point of the ticket and it runs.** `TestAnvil` forks chain 4663, deploys
the real compiled `NutzDistributor` and a `MockERC20` stand-in for NUTZ, funds two Epochs from an
EOA Converter, posts a Root under real 2-of-3 EIP-712 signatures, and appends an Excluded address
through the 48-hour timelock at minute 30 of a later Epoch. Fifteen subtests assert every decoder
against what that node returned. Verified in both modes: forked against 4663 (`safe` and
`finalized` are served there) and against a bare node.

**Nothing is validated against itself.** The chain is driven entirely through `cast` — calldata,
constructor arguments, EIP-712 digests and the exclusion-set keccak all come from foundry's
encoder, which shares no code with this package. The topic0 hashes and both selectors are
committed as literals from `cast keccak` / `cast sig` rather than computed by the code under test,
for the reason ticket 01 found the hard way: an expectation produced by the implementation proves
only that it agrees with itself. A drifted signature does not fail loudly — it matches no log and
reports an Epoch with no Root.

### Decisions worth knowing

- **`ledger()` returns eighteen words inline, with no head offset.** Every component of the Ledger
  struct is static, so the tuple is static. Predicted from the ABI rules and then confirmed on
  chain: the call returns exactly 576 bytes. That is what keeps the decoder twenty lines.
- **Disagreement is defined at a pinned block, never at a tip.** Two endpoints asked for `latest`
  answer honestly and differently, so treating that as a contradiction would fire constantly and
  mean nothing. `Head` takes the *lowest* tip any endpoint has reached and then re-reads that
  number from all of them: different blocks at the same height is the contradiction ADR-0002 is
  about, and everything downstream is a question about a fixed block. Every `eth_call` therefore
  takes a block, with no "latest" form — a closed Epoch's `claimed[]` still moves.
- **An endpoint that errors fails the whole read**, rather than being dropped in favour of the ones
  that answered. Settling for whichever endpoints happened to reply would weaken the cross-check
  exactly when an endpoint is unavailable, which is the moment ADR-0002 is worried about. A user
  who wants one endpoint's answer passes one endpoint.
- **Endpoints are named by position and host, never by URL.** A provider URL's path is routinely an
  API key, and these strings go into run headers, JSON reports and pasted issues. Ticket 05 should
  use `Reader.Endpoints()` for the header rather than the raw `--rpc` values.
- **The Excluded-set rule lives in `internal/twab`, and `internal/chain` calls it.** Spec §5's
  whole-Epoch rule is one of the resolutions ADR-0003 makes normative, so it belongs with the
  rules the Cases pin. `twab.ExcludedAsOf` is now exported and returns the set ascending and
  de-duplicated — ordering is load-bearing because engineering spec §4.5 hashes it with
  `abi.encodePacked` — and `chain.ExcludedAsOf` adds only the type conversion at the seam, the way
  `internal/alloc` converts to `merkle.Address`. `chain` also takes `twab.WindowOf` rather than
  restating `3600`. The direction is safe: `twab` still imports nothing but the standard library,
  so ticket 02's "no chain types in the rules engine" holds.
- **`EndBlock` is here, though the ticket did not list it.** Spec §5's window resolution has a half
  no hermetic Case can express — "the end block is the last block with `timestamp < 3600(e+1)`" —
  and `testdata/cases/README.md` names `internal/chain` as its owner. Binary search over
  non-decreasing timestamps, which is also correct when many blocks share a second, as they do at
  4663's ~100 ms cadence. An Epoch the tip has not reached is `EpochNotClosed`, which is
  INDETERMINATE and not a guess at the tip.
- **Refusing beats guessing, everywhere.** A null result is not block zero; an empty `eth_call`
  return is not an empty Ledger; a dirty address word is not an address; a log whose shape does not
  match its signature is a different event. Each of these would otherwise produce a confident wrong
  answer rather than the exit 2 the caller is owed, and each has a named test saying which.

### Fixed in review

Both review axes independently flagged the same thing, which is usually the sign it is real.

- **The Excluded-set rule was implemented twice** — `chain.ExcludedAsOf` alongside the pre-existing
  `twab.excludedAsOf`, with the same predicate and the doc comment copied verbatim. The Recompute
  runs twab's copy and only the §4.5 hash ran chain's, so a rule change landing as a Case would
  have fixed one and silently left the other wrong: exactly the divergence ADR-0003's Cases exist
  to catch, manufactured internally. The rule is now twab's alone. Noted while doing it: the
  original commit imported `twab.WindowOf` on the argument that a second `3600` would be drift, and
  then reimplemented the rule around it.
- **The harness could be skipped into irrelevance.** Every precondition failure was `t.Skipf`, so a
  CI run with no foundry installed was green having exercised no decoder against a node — ADR-0004's
  bargain quietly not being kept. `NUTZ_VERIFY_REQUIRE_ANVIL` now turns each of those into a
  failure, including a missing fork URL. CI sets it; a developer without foundry still gets a skip.
- **"Every finality tag is served" proved nothing on a bare node**, which was the default run. It
  now skips unless the node is actually a fork of 4663, so a green run never implies that 4663
  serves `finalized`.
- **`Head` set the horizon from the slowest endpoint silently.** Taking the lowest tip is right, but
  one endpoint stuck a long way back made a recent Epoch read as not yet closed for no visible
  reason. `Tip.Lag` now carries the distance for ticket 05's header to print.
- **`ExcludedSet.Contains` had no consumer** outside its own tests. Removed.
- A `Site` type now carries `BlockNumber`, `BlockHash`, `LogIndex` and `Timestamp`, embedded in all
  three event types — the four travelled together everywhere, and naming them collapsed the three
  copies of the decode-and-timestamp tail into one helper. It is also the shape of ticket 04's
  Cache record. Field access is unchanged for callers.
- `eachEndpoint` and `inParallel` shared a cancel-and-remember-the-first-error skeleton, now a
  `firstFailure` both use.
- Two avoided glossary terms: "quorum" (`CONTEXT.md` **Cross-check**) and "fixtures" for what are
  log builders (`CONTEXT.md` **Case**).

**Declined, with the reason.** All ten test files are `package chain` where every other package in
the repo tests externally. Eight of them genuinely need unexported access — this package's surface
is mostly unexported decoders, unlike `twab`, `alloc` and `merkle`, whose rules are all exported —
and the other two share their helpers. Splitting a file across the package boundary to match the
pattern would duplicate helpers to buy nothing.

### Fixed against the real endpoint, after review

The review round compared the package with a fake node. Pointing it at chain 4663's actual public
endpoint found two defects the fake could not have, both in the part of the ticket that was taken
on faith from the engineering spec. Both are recorded in ../spec.md §4 with the measurements.

- **The 2,000-block cap does not exist; the limit is 10,000 results.** A million-block range is
  accepted. A fixed 2,000-block page against USDG (4.9–5.4 logs per block) therefore straddled the
  cap: ~9,750 logs on a quiet stretch, ~13,500 on a busy one. It passed every test and would have
  failed intermittently in production, on the endpoint the Verifier's target user is most likely to
  have. Paging now opens at 2,000 and halves on a refusal that names a limit; the heuristic is safe
  in both directions because a wrong guess can only waste requests, never shorten a page.
- **The endpoint throttles**, and a `429` mid-sync became INDETERMINATE. It is not "could not
  check" — the provider is saying how to succeed — so `429` and `5xx` now retry five times with
  jittered backoff, honouring `Retry-After`, and only then fail. A `403` is not retried; the one
  observed was a short block earned by probing, and encoding one provider's quirk as "403 means
  wait" would mask a real key failure everywhere else.
- **Two independent providers agree.** The public endpoint and a Chainstack archive return
  byte-identical logs over 13,715 real entries. Until this, ADR-0002's cross-check had only been
  tested as one anvil compared with itself; a canonical comparison that could not survive two real
  providers' formatting would have made every multi-endpoint run INDETERMINATE.

Both live tests are in `live_test.go`, opt-in via `NUTZ_VERIFY_LIVE_RPC`, and run nightly (ticket
10).

### Not done as written

- **The harness does not run `script/Deploy.s.sol`.** It cannot: `run()` hardcodes
  `script/config/robinhood.json`, whose `signers` and `keeper` are still the zero address pending
  that file's own TODO, and the `Signers` constructor reverts `ZeroAddress` on them. The script also
  deploys the Converter and the Draw, from which this ticket decodes nothing. The harness therefore
  deploys the same compiled `NutzDistributor` artifact directly. **Ticket 09 is closed wontfix**:
  the deploy path is already covered by `nutz-contracts/test/unit/Deploy.t.sol` — the Converter
  address prediction, the revert when it misses, `checkRoles`, the Draw wiring — so running the
  script from here would buy no decoding coverage and couple this suite to a sibling repo's script
  API. The one real gap it found belongs over there and is written up in 09: nothing asserts that
  the committed `robinhood.json` actually deploys, and today it cannot.

### Follow-ups, not done here

- **Block headers are one request per block that produced a log**, fetched over a pool of eight —
  and at USDG's density that is one request per block, **~864,000 per day of history**. Ticket 04
  has the numbers. JSON-RPC batching is the obvious lever and was left out because an endpoint that
  does not support batching would fail loudly rather than degrade; expect ticket 04 to need it.
- **Nothing here finds the token's creation block or the Distributor's deploy block**, which every
  log query needs as its lower bound. Ticket 05 pins them as constants or flags.
- Two defensive branches in `endpoint.call` are unreachable and so uncovered (marshalling a struct
  we built; building a request from a URL `url.Parse` already accepted). Every other function in
  the package is at 100% of statements, 99.6% for the package and 99.7% across `internal/`.
- CI now exists and requires the harness — **ticket 10**. The enforcement variable was split in two
  while building it (`NUTZ_VERIFY_REQUIRE_ANVIL` for the tools, `NUTZ_VERIFY_REQUIRE_FORK` for the
  fork) so the decoders meet a real node on every push rather than only nightly.
- Carried over and still open: no `LICENSE` at the repo root though spec §3 says MIT; `go.mod` says
  `go 1.24.0` rather than spec §3's conservative directive; nothing exports the fixture set to the
  Solidity and TypeScript implementations ADR-0003 names.
