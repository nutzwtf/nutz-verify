package epoch_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/epoch"
	"github.com/nutzwtf/nutz-verify/internal/fakenode"
	"github.com/nutzwtf/nutz-verify/merkle"
)

// The same Cases internal/alloc runs hermetically, run here through the Engine against a
// fake node: each Case is put on a chain, read back over JSON-RPC through the Cache, and
// its expectations checked on Compute's Result. What this adds to the alloc run is the
// half the Cases README says a Case cannot pin — which blocks a Recompute reads — and the
// guarantee that the Engine's public path, the one the indexer links, is what the Cases
// pin rather than only the package underneath it.
const caseDir = "../testdata/cases"

// Contract addresses no Case mentions, checked by chainOf: a Case whose Holder was also
// the token would put its transfers in the wrong logs.
var (
	caseToken       = repeatAddr(0xf1)
	caseDistributor = repeatAddr(0xf2)

	// caseDevWallet stands in for the zero address, which a Case uses to mean "no dev
	// wallet" and which Deployment refuses. An address that holds nothing is the same
	// thing to the rules.
	caseDevWallet = repeatAddr(0xfe)
)

func TestCases_ThroughTheEngine(t *testing.T) {
	t.Parallel()

	for _, path := range casePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			c := loadCase(t, path)
			node, dep, endBlock := chainOf(t, c)
			eng := newEngine(t, node, dep)

			result, err := eng.Compute(context.Background(), c.EpochID)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}

			// The chain half: the Epoch's end block and window, and the posted carryIn
			// the Case's carryIn was smuggled in as.
			if result.EndBlock == nil || result.EndBlock.Number != endBlock {
				t.Errorf("EndBlock = %v, want block %d", result.EndBlock, endBlock)
			}
			if want := int64(c.EpochID) * 3600; result.Window.Start != want || result.Window.End != want+3600 {
				t.Errorf("Window = %+v, want [%d, %d)", result.Window, want, want+3600)
			}
			if result.Posted == nil {
				t.Fatal("Posted is nil, and the fake node posted a Root")
			}
			requireAmountStrings(t, "CarryIn", result.CarryIn, c.CarryIn)
			requireAmountStrings(t, "Funded", result.Funded, c.Funded)

			// The rules half, as internal/alloc checks it.
			if got := len(result.Allocations); got != len(c.Expected.Allocations) {
				t.Fatalf("got %d allocations, want %d", got, len(c.Expected.Allocations))
			}
			for i, want := range c.Expected.Allocations {
				got := result.Allocations[i]

				if account := parseAddress(t, want.Account); got.Account != account {
					t.Errorf("allocation %d is for %s, want %s", i, got.Account, account)
				}
				requireInt(t, fmt.Sprintf("allocation %d TWAB", i), got.TWAB, want.TWAB)
				requireInt(t, fmt.Sprintf("allocation %d weight", i), got.Weight, want.Weight)
				if got.MultBps != want.MultBps {
					t.Errorf("allocation %d MultBps = %d, want %d", i, got.MultBps, want.MultBps)
				}
				requireAmountStrings(t, fmt.Sprintf("allocation %d amounts", i), got.Amounts, want.Amounts)

				if result.Tree != nil && got.LeafIndex != result.Tree.TreeIndex(i) {
					t.Errorf("allocation %d LeafIndex = %d, tree says %d", i, got.LeafIndex, result.Tree.TreeIndex(i))
				}
			}

			requireAmountStrings(t, "Totals", result.Totals, c.Expected.Totals)
			requireAmountStrings(t, "CarryOut", result.CarryOut, c.Expected.CarryOut)
			requireInt(t, "TotalWeight", result.TotalWeight, c.Expected.TotalWeight)
			c.requireRoot(t, result)
		})
	}
}

// --- a Case on a chain ---

// chainOf puts a Case on a fake chain: one block per distinct timestamp, the token's
// Transfer logs and the Distributor's ExcludedAppended logs in the blocks their timestamps
// name, and — because the Engine computes over the posted carryIn when a Root stands and
// derives it from the Carry chain otherwise — a Root posted at the tip carrying the Case's
// carryIn, under any hash, since Compute compares nothing. Returns the Epoch's end block.
func chainOf(t *testing.T, c testCase) (*fakenode.Node, epoch.Deployment, uint64) {
	t.Helper()

	window := int64(c.EpochID) * 3600
	end := window + 3600

	// Every timestamp a log lands at, plus block 0 at the dawn of time, the boundary, and
	// a tip an Epoch past everything so the target has closed at every finality.
	stamps := []int64{0, end}
	for _, tr := range c.Transfers {
		stamps = append(stamps, tr.Timestamp)
	}
	for _, e := range c.ExcludedAt {
		stamps = append(stamps, e.Timestamp)
	}
	stamps = append(stamps, slices.Max(stamps)+3600)
	slices.Sort(stamps)
	stamps = slices.Compact(stamps)

	blockOf := func(ts int64) uint64 {
		i, found := slices.BinarySearch(stamps, ts)
		if !found {
			t.Fatalf("no block at timestamp %d", ts)
		}

		return uint64(i)
	}
	tip := uint64(len(stamps) - 1)

	var endBlock uint64
	for i, ts := range stamps {
		if ts < end {
			endBlock = uint64(i)
		}
	}

	node := fakenode.New(stamps)
	for _, tr := range c.Transfers {
		value, err := strconv.ParseInt(tr.Value, 10, 64)
		if err != nil {
			t.Fatalf("transfer value %q does not fit the fake node's int64: %v", tr.Value, err)
		}
		node.Transfer(blockOf(tr.Timestamp), caseToken, parseAddress(t, tr.From), parseAddress(t, tr.To), value)
	}
	for _, e := range c.ExcludedAt {
		node.Exclusion(blockOf(e.Timestamp), caseDistributor, parseAddress(t, e.Account))
	}

	root := chain.Hash{0x01} // any: the Engine reads it and compares nothing
	funded, carryIn := parseVector(t, "funded", c.Funded), parseVector(t, "carryIn", c.CarryIn)
	node.RootPosted(tip, caseDistributor, c.EpochID, root, fakenode.Amounts(), carryIn)
	node.Ledgers[c.EpochID] = fakenode.Ledger{Root: root, RootPostedAt: stamps[tip], Funded: funded, Totals: fakenode.Amounts()}

	dep := epoch.Deployment{
		ChainID:          4663,
		Token:            caseToken,
		Distributor:      caseDistributor,
		DevWallet:        parseAddress(t, c.DevWallet),
		TokenBlock:       0,
		DistributorBlock: 0,
	}
	if dep.DevWallet == (chain.Address{}) {
		dep.DevWallet = caseDevWallet
	}

	for _, reserved := range []chain.Address{caseToken, caseDistributor, caseDevWallet} {
		if bytes.Contains(c.raw, []byte(reserved.String())) {
			t.Fatalf("Case mentions %s, which this harness uses for a contract", reserved)
		}
	}

	return node, dep, endBlock
}

// newEngine binds an Engine to the node at safe finality, with a throwaway Cache.
func newEngine(t *testing.T, node *fakenode.Node, dep epoch.Deployment) *epoch.Engine {
	t.Helper()

	reader, err := chain.New(chain.Config{
		Endpoints:      []string{node.Serve(t)},
		Token:          dep.Token,
		Distributor:    dep.Distributor,
		CallsPerSecond: 1_000_000, // a fake node has no budget to respect
	})
	if err != nil {
		t.Fatalf("chain.New: %v", err)
	}

	tip, err := reader.Head(context.Background(), chain.Safe)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	eng, err := epoch.New(reader, tip, t.TempDir(), dep, epoch.Options{})
	if err != nil {
		t.Fatalf("epoch.New: %v", err)
	}

	return eng
}

// --- the Case format, as internal/alloc reads it ---

type testCase struct {
	Name       string          `json:"name"`
	Note       string          `json:"note"`
	EpochID    uint64          `json:"epochId"`
	DevWallet  string          `json:"devWallet"`
	Transfers  []caseTransfer  `json:"transfers"`
	ExcludedAt []caseExclusion `json:"excludedAt"`
	Funded     []string        `json:"funded"`
	CarryIn    []string        `json:"carryIn"`
	Expected   caseExpectation `json:"expected"`

	raw []byte
}

type caseTransfer struct {
	Timestamp int64  `json:"timestamp"`
	From      string `json:"from"`
	To        string `json:"to"`
	Value     string `json:"value"`
}

type caseExclusion struct {
	Timestamp int64  `json:"timestamp"`
	Account   string `json:"account"`
}

type caseExpectation struct {
	TotalWeight string           `json:"totalWeight"`
	Allocations []caseAllocation `json:"allocations"`
	Totals      []string         `json:"totals"`
	CarryOut    []string         `json:"carryOut"`
	Root        *string          `json:"root"`
}

type caseAllocation struct {
	Account string   `json:"account"`
	TWAB    string   `json:"twab"`
	MultBps int64    `json:"multBps"`
	Weight  string   `json:"weight"`
	Amounts []string `json:"amounts"`
}

func (c testCase) requireRoot(t *testing.T, result epoch.Result) {
	t.Helper()

	if c.Expected.Root == nil {
		if result.HasRoot {
			t.Errorf("Root = %s, but the Case expects none", result.Root)
		}

		return
	}

	if !result.HasRoot {
		t.Fatalf("no Root, want %s", *c.Expected.Root)
	}
	if want := parseHash(t, *c.Expected.Root); result.Root != want {
		t.Errorf("Root = %s, want %s", result.Root, *c.Expected.Root)
	}

	for i := range result.Tree.Len() {
		if !merkle.Verify(merkle.Hash(result.Root), result.Tree.Leaf(i), result.Tree.Proof(i)) {
			t.Errorf("the proof for claim %d does not verify against the Root", i)
		}
	}
}

func casePaths(t *testing.T) []string {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join(caseDir, "*.json"))
	if err != nil {
		t.Fatalf("globbing Cases: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no Cases found in %s", caseDir)
	}

	return paths
}

func loadCase(t *testing.T, path string) testCase {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading Case: %v", err)
	}

	var c testCase
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("parsing Case %s: %v", path, err)
	}
	c.raw = raw

	return c
}

func parseVector(t *testing.T, what string, values []string) chain.Amounts {
	t.Helper()

	var out chain.Amounts
	if len(values) != len(out) {
		t.Fatalf("%s has %d values, want %d", what, len(values), len(out))
	}
	for i, v := range values {
		out[i] = parseInt(t, fmt.Sprintf("%s[%d]", what, i), v)
	}

	return out
}

func parseInt(t *testing.T, what, s string) *big.Int {
	t.Helper()

	x, ok := new(big.Int).SetString(s, 10)
	if !ok {
		t.Fatalf("%s: %q is not a decimal integer", what, s)
	}

	return x
}

func parseAddress(t *testing.T, s string) chain.Address {
	t.Helper()

	a, err := chain.ParseAddress(s)
	if err != nil {
		t.Fatalf("address %q: %v", s, err)
	}

	return a
}

func parseHash(t *testing.T, s string) chain.Hash {
	t.Helper()

	raw, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil || len(raw) != len(chain.Hash{}) {
		t.Fatalf("hash %q is not 32 bytes of hex", s)
	}

	var h chain.Hash
	copy(h[:], raw)

	return h
}

func repeatAddr(b byte) chain.Address {
	var a chain.Address
	for i := range a {
		a[i] = b
	}

	return a
}

func requireInt(t *testing.T, what string, got *big.Int, want string) {
	t.Helper()

	if got == nil {
		t.Errorf("%s is nil, want %s", what, want)

		return
	}
	if got.String() != want {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func requireAmountStrings(t *testing.T, what string, got chain.Amounts, want []string) {
	t.Helper()

	if len(want) != len(got) {
		t.Fatalf("%s: the Case lists %d values, want %d", what, len(want), len(got))
	}
	for i := range got {
		requireInt(t, fmt.Sprintf("%s[%d]", what, i), got[i], want[i])
	}
}
