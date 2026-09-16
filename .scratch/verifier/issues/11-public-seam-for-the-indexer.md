# 11 — The public seam: nutz-verify becomes the indexer's engine

Status: resolved
Type: task
Blocked by: —
Spec: ../spec.md §3; engineering spec §1 (v0.6, nutz-contracts ticket `engineering-spec/01`); nutz-platform `.scratch/indexer/spec.md`

Decided 2026-09-16: the private indexer is Go and **imports this module** rather than
reimplementing the rules. Byte-identity with the Verifier becomes a property of the code
(one path) instead of a property of a test. That needs a public Go API, which this repo has
deliberately not had (spec §3: "everything under `internal/`"), and it retires the
implementation-independence half of ADR-0001's argument for Go.

`CONTEXT.md` now names the shared path the **Engine**: the one code path that reads the chain
and rebuilds an Epoch's Allocations, `totals`, Carry and Root. A Recompute is the Engine's
output compared against the chain; the indexer's Bundle is the Engine's output written out.

## What becomes public

| Package | Why the indexer needs it |
|---|---|
| `github.com/nutzwtf/nutz-verify/epoch` (new) | the Engine: everything `cmd/nutz-verify/recompute.go` and `roots.go` do up to, but not including, the comparison |
| `chain` (moved from `internal/`) | the indexer constructs the `Reader`, picks endpoints and rate, reads Distributor state |
| `cache` (moved from `internal/`) | the indexer owns the Cache's path and lifecycle |
| `merkle` (moved from `internal/`) | the Bundle's `tree.json` is the OpenZeppelin dump and the keeper derives proofs |

`twab`, `alloc` and `report` stay `internal/`. `chain` keeps importing `internal/twab` for
`ExcludedAsOf`, which Go allows within one module. The Engine defines its own result types so
nothing internal leaks through a signature.

## The Engine, as grilled 2026-09-16

```go
package epoch

type Deployment struct { ... }            // moved from cmd/nutz-verify/deployment.go
func (d Deployment) Check() error
func Pinned() Deployment                  // chain 4663; a func, not a var an importer could assign

type Options struct {
    SyncChunk int                         // blocks per Cache read; 0 = the CLI's 2,000
    Fresh     bool
    Progress  func(format string, args ...any)
}

// One Engine is one run at one tip: the ledger reads are pinned to it, the Cache is brought
// to it once, and the Ledger advances through it (ticket 07). New makes no calls: it runs
// dep.Check() and refuses a Reader whose Token or Distributor differ from dep.
func New(reader *chain.Reader, tip chain.Tip, cacheDir string, dep Deployment, opts Options) (*Engine, error)

// Sync brings the Cache to the tip and reports what it did (dir, repaired, reorg, appended,
// records, through). Idempotent; Compute and Walk call it if it has not run.
func (e *Engine) Sync(ctx context.Context) (Synced, error)

// Compute rebuilds one Epoch. It finds the standing Root, always derives the expected
// carryIn as the walker does (previous rooted Epoch's recomputed carryOut plus the funding of
// every Skipped Epoch between), and computes over the posted carryIn when a Root stands. A
// chain.EpochNotClosed error means "not yet". A lower id than the last one rebuilds the
// Ledger from the retained history; not an error.
func (e *Engine) Compute(ctx context.Context, id uint64) (Result, error)

// Walk is --chain: every rooted Epoch from the deploy Epoch through target, in order, target
// always last whether or not it has a Root. Unrooted Epochs contribute their funding to the
// carry and no Result. Each Result's ExpectedCarryIn is the recomputed chain's carry, which
// is what makes this different from a series of Computes.
func (e *Engine) Walk(ctx context.Context, target uint64) ([]Result, error)

// Latest is the Epoch of the most recent standing Root at the tip.
func (e *Engine) Latest(ctx context.Context) (uint64, error)

type Result struct {
    EpochID         uint64
    Window          Window            // Start, End
    EndBlock        chain.Block
    Finality        chain.Finality
    Skipped         bool              // the ledger says Skipped
    Funded          chain.Amounts
    CarryIn         chain.Amounts     // what the Root was computed over
    ExpectedCarryIn chain.Amounts     // what the Carry chain says it should be
    CarryFrom       string            // the walker's provenance string, as the Assertion prints it
    Posted          *chain.RootPosted // nil when no Root stands
    Totals          chain.Amounts
    CarryOut        chain.Amounts
    TotalWeight     *big.Int
    Excluded        chain.ExcludedSet // .Hash() for input.json
    HasRoot         bool
    Root            chain.Hash
    Tree            *merkle.Tree      // nil when !HasRoot
    Allocations     []Allocation      // Account, Amounts, TWAB, MultBps, Weight, LeafIndex
}
```

Only `chain.EpochNotClosed` and `cache.Mismatch` are typed errors; everything else is a plain
error and INDETERMINATE.

`cmd/nutz-verify` keeps `assess`, `report` and `--artifacts`, all running on `Result` with
nothing of their own recomputed: `epoch` is `Compute` then `assess`; `--chain` is `Walk` then
`assess` per `Result`; `latest` is `Latest` then `Compute`; `sync` is `Sync`. `roots.go`
moves into the Engine. The point of the ticket is that after it, the binary the Signer runs
and the library the indexer links execute one function for the rules.

### `merkle` grows the OpenZeppelin dump

The indexer's `tree.json` is OZ's `standard-v1` dump and its store carries `leaf_index`, and
`Tree` today keeps both its nodes and its leaf positions private. So: `Tree` retains its
claims; `Tree.Dump()` returns a JSON-tagged struct (`Format "standard-v1"`, `LeafEncoding`,
`Tree []Hash`, `Values []{Value [id, account, amounts[5]], TreeIndex}`) with integers as
decimal strings and the address as lowercase hex, which is what OZ writes from string inputs;
`Tree.TreeIndex(i)` is what `Allocation.LeafIndex` carries. `gen-fixtures.ts` adds a `dump`
field per generated fixture from `tree.dump()` and the Go fixture test compares `Dump()`
against it structurally — `load()` is the contract, not `JSON.stringify`'s layout.
`claims.json` from nutz-contracts has no dump and is left alone.

## Tests

- Every existing test keeps passing; the moves are `git mv` plus import paths.
- `cmd/nutz-verify/fakenode_test.go` is lifted into `internal/fakenode` so `epoch` and `cmd`
  share it. `internal/chain`'s own wire-level fake node is left alone.
- `epoch` gets a table test over `testdata/cases/` that goes through `Compute` against the
  fake node, so the Engine is pinned by the Cases and not only `internal/alloc`, and the
  half the Cases cannot pin (which blocks are read) is exercised too. The fake node posts a
  `RootPosted` log for the Case's Epoch carrying the Case's `carryIn` — any root hash, since
  `Compute` does not compare — and serves zero-funded ledgers for the Epochs before it; the
  test checks `Result` against `expected`. `internal/alloc`'s hermetic Cases test stays.
- A test that `cmd/nutz-verify`'s `assess` for a MATCH fixture consumes a `Result` produced by
  `Compute` with no field of its own recomputed.
- The Merkle fixture test compares `Dump()` with each generated fixture's `dump`.

## Docs

- Spec §3: replace "Everything under `internal/` — the Signer execs the binary, so there is
  no public Go API to keep stable" with the table above and: "the public packages are a
  versioned API with one consumer, the private indexer, which pins an exact tag and records it
  in every Bundle; the Signer's binary is built from that same tag."
- **ADR-0006** (new): "nutz-verify is the indexer's engine". Supersedes the independence
  clause of ADR-0001 (mark ADR-0001 "amended by ADR-0006" in its status line). Records: the
  alternatives (TypeScript indexer as spec'd; Go written independently), why one path won
  (byte-identity by construction, one place for a rule to land, the same author writing both
  from the same spec was never real independence), and what the Signer's MATCH now proves: a
  separate host, endpoints and Cache catch a compromised keeper, bad RPC data or a tampered
  Bundle; a shared rules bug is bounded by the Cases and the per-Epoch cap.
- README "What MATCH does not prove": add the shared-code sentence from ADR-0006; its pointer
  to `cmd/nutz-verify/deployment.go` becomes `epoch/deployment.go`, as does the unpinned-build
  error message. (Ticket 06's pin is the binary's checksum on the Signer host, not the
  Deployment, and is untouched.)
- `testdata/cases/README.md`: the consumer sentence becomes "the private indexer links this
  module and runs the same directory through the Engine in its CI".
- `testdata/merkle/README.md`: the generated fixtures now carry OZ's `dump`.

## Release

Tag **v0.2.0** once merged: it is the first version with a public API, and the version the
indexer's `go.mod` pins. `scripts/build-release.sh` is unchanged.

## Done when

`go build ./...` and `go test ./...` green; `cmd/nutz-verify` imports `epoch` and not
`internal/alloc` or `internal/twab`; the Cases pass through the Engine; `Dump()` matches OZ;
spec §3, ADR-0001, ADR-0006, README, the Cases README and the Merkle README read as above;
v0.2.0 tagged with reproduced checksums.

## Comments

**2026-09-16, grilling.** Seven decisions reshaped the sketch, all recorded above:
1. The Engine owns Root discovery and the Carry walk (`Compute` derives the expected carry
   and reads the standing Root; `Walk` is `--chain`; `Latest`), because Assertion 4 needs the
   expected carry whatever the Root was computed over, the indexer needs the same derivation,
   and a private copy in `cmd/` is the duplication the ticket exists to kill.
2. One Engine is one tip, bound at `New`; the Cache is synced to it once.
3. `New` takes a concrete `*chain.Reader`, no interface: an interface is a seam a second
   reader could be swapped into. The Cases run through a fake JSON-RPC node.
4. `merkle.Tree.Dump()` lands here, pinned against OZ via the existing tooling, in this
   ticket: v0.2.0 without it is not the version the indexer can pin.
5. `Pinned()` is a function, for the reason `deployment.go` already gives.
6. `New` refuses a Reader whose addresses differ from the Deployment.
7. `CONTEXT.md` gains **Engine**; Recompute is unchanged.

**2026-09-16, built** on branch `11-public-seam`, uncommitted pending review. `go build`,
`go vet`, staticcheck and `go test ./...` green, the reproducible-build test included; the
14 Cases pass through `Compute`; `Dump()` matches OpenZeppelin's on all five generated
fixtures; `cmd/nutz-verify` imports `epoch`, `chain`, `cache` and `internal/report` only.
Two deviations from the grilled text, both in the Engine's favour: `Compute` and `Walk` find
the end block before syncing the Cache, as the CLI did, so an Epoch not yet closed is
INDETERMINATE without a sync; the CLI reads the Cache status back through `Engine.Synced()`
instead of calling `Sync` first. `Result.EndBlock` is a pointer, nil on Walk's earlier links,
because the search costs a header per Epoch and no link's report shows it. Release checksums
from `scripts/build-release.sh` at this tree (GOTOOLCHAIN=local, go1.26.5): linux_amd64
`d817a9f7…96c3f6`, linux_arm64 `f5963962…a4cd32`, darwin_amd64 `e5ab95ef…7472510`,
darwin_arm64 `adeb2302…0bfc436`. Left for the human: commit, merge, tag v0.2.0.

**2026-09-16, resolved.** Engine merged to main (a2f3a49, CI green); docs and ADR-0006 committed; tagged v0.2.0.
