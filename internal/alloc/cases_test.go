package alloc_test

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/merkle"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// The Cases are an interface, not internal scaffolding: ADR-0003 has the private indexer's
// CI consume this same directory and fail on divergence. testdata/cases/README.md documents
// the format.
const caseDir = "../../testdata/cases"

// One Case per resolution in spec §5, named here so a Case that quietly disappears fails
// the suite rather than silently narrowing what the rules are pinned to.
var requiredCases = []string{
	"buy-at-minute-59",
	"dev-wallet-flat",
	"excluded-holder",
	"funding-plus-carry",
	"no-eligible-holders",
	"omission-last",
	"sell-at-minute-1",
	"streak-boundaries",
	"transfers-within-one-hour",
	"weight-carries-bps",
	"window-closes-at-the-boundary",
}

func TestCases(t *testing.T) {
	t.Parallel()

	for _, path := range casePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			c := loadCase(t, path)
			result := compute(t, c.params(t))

			if got := len(result.Allocations); got != len(c.Expected.Allocations) {
				t.Fatalf("got %d allocations, want %d", got, len(c.Expected.Allocations))
			}

			for i, want := range c.Expected.Allocations {
				got := result.Allocations[i]

				// Ascending by address, so a diff against the indexer's artifact is stable.
				if account := want.account(t); got.Account != account {
					t.Errorf("allocation %d is for %#x, want %#x", i, got.Account, account)
				}

				requireInt(t, fmt.Sprintf("allocation %d TWAB", i), got.TWAB, want.TWAB)
				requireInt(t, fmt.Sprintf("allocation %d weight", i), got.Weight, want.Weight)
				if got.MultBps != want.MultBps {
					t.Errorf("allocation %d MultBps = %d, want %d", i, got.MultBps, want.MultBps)
				}

				requireAmountStrings(t, fmt.Sprintf("allocation %d amounts", i), got.Amounts, want.Amounts)
			}

			requireAmountStrings(t, "Totals", result.Totals, c.Expected.Totals)
			requireAmountStrings(t, "CarryOut", result.CarryOut, c.Expected.CarryOut)
			requireInt(t, "TotalWeight", result.TotalWeight, c.Expected.TotalWeight)
			c.requireRoot(t, result)
		})
	}
}

// Engineering spec §7: same inputs, same Root, byte for byte — the property that lets two
// machines, and two implementations, be compared at all.
func TestCases_AreDeterministic(t *testing.T) {
	t.Parallel()

	for _, path := range casePaths(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			c := loadCase(t, path)
			first := fingerprint(compute(t, c.params(t)))

			if second := fingerprint(compute(t, c.params(t))); second != first {
				t.Fatalf("a second Recompute of the same Case differs:\n got %s\nwant %s",
					second, first)
			}

			// Log order within a block is not part of the input, so reversing the whole list
			// must not reach the Root either.
			shuffled := c.params(t)
			slices.Reverse(shuffled.Transfers)
			slices.Reverse(shuffled.Exclusions)

			if got := fingerprint(compute(t, shuffled)); got != first {
				t.Fatalf("reordering the logs changed the Recompute:\n got %s\nwant %s", got, first)
			}
		})
	}
}

// A rule is only pinned while its Case is still on disk.
func TestCases_CoverEveryResolution(t *testing.T) {
	t.Parallel()

	found := []string{}
	for _, path := range casePaths(t) {
		found = append(found, strings.TrimSuffix(filepath.Base(path), ".json"))
	}

	for _, name := range requiredCases {
		if !slices.Contains(found, name) {
			t.Errorf("no Case named %q: spec §5 resolutions must each keep a named Case", name)
		}
	}
}

// --- the Case format ---

// testCase mirrors testdata/cases/*.json. Every expected value there is hand-authored
// except expected.root, which tooling/ produces with OpenZeppelin (ADR-0005).
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

	// Root is null exactly when the Epoch posts none: spec §5's W == 0, or every allocation
	// flooring to zero.
	Root *string `json:"root"`
}

type caseAllocation struct {
	Account string   `json:"account"`
	TWAB    string   `json:"twab"`
	MultBps int64    `json:"multBps"`
	Weight  string   `json:"weight"`
	Amounts []string `json:"amounts"`
}

func (a caseAllocation) account(t *testing.T) twab.Address {
	t.Helper()

	return parseAddress(t, a.Account)
}

func (c testCase) params(t *testing.T) alloc.Params {
	t.Helper()

	params := alloc.Params{
		EpochID:   c.EpochID,
		DevWallet: parseAddress(t, c.DevWallet),
		Funded:    parseVector(t, "funded", c.Funded),
		CarryIn:   parseVector(t, "carryIn", c.CarryIn),
	}

	for i, tr := range c.Transfers {
		params.Transfers = append(params.Transfers, twab.Transfer{
			Timestamp: tr.Timestamp,
			From:      parseAddress(t, tr.From),
			To:        parseAddress(t, tr.To),
			Value:     parseInt(t, fmt.Sprintf("transfer %d value", i), tr.Value),
		})
	}

	for _, e := range c.ExcludedAt {
		params.Exclusions = append(params.Exclusions, twab.Exclusion{
			Timestamp: e.Timestamp,
			Account:   parseAddress(t, e.Account),
		})
	}

	return params
}

func (c testCase) requireRoot(t *testing.T, result alloc.Result) {
	t.Helper()

	if c.Expected.Root == nil {
		if result.HasRoot {
			t.Errorf("Root = %#x, but the Case expects none", result.Root)
		}

		return
	}

	if !result.HasRoot {
		t.Fatalf("no Root, want %s", *c.Expected.Root)
	}

	if want := parseHash(t, *c.Expected.Root); result.Root != want {
		t.Errorf("Root = %#x, want %s", result.Root, *c.Expected.Root)
	}

	// The Root is only worth its place if the tree under it holds these allocations.
	for i := range result.Tree.Len() {
		if !merkle.Verify(result.Root, result.Tree.Leaf(i), result.Tree.Proof(i)) {
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
	// Unknown fields are a typo in an expectation, which would otherwise pass as a default.
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		t.Fatalf("parsing Case %s: %v", path, err)
	}

	if name := strings.TrimSuffix(filepath.Base(path), ".json"); c.Name != name {
		t.Fatalf("Case %s declares the name %q; the two must agree", path, c.Name)
	}
	if c.Note == "" {
		t.Errorf("Case %s carries no note: a Case that does not say which reading it rules "+
			"out cannot be reviewed", path)
	}

	return c
}

func requireInt(t *testing.T, what string, got *big.Int, want string) {
	t.Helper()

	if expected := parseInt(t, what, want); got.Cmp(expected) != 0 {
		t.Errorf("%s = %s, want %s", what, got, expected)
	}
}

// requireAmountStrings is requireAmounts over a Case's decimal strings.
func requireAmountStrings(t *testing.T, what string, got alloc.Amounts, want []string) {
	t.Helper()

	if expected, actual := "["+strings.Join(want, " ")+"]", vector(got); actual != expected {
		t.Errorf("%s = %s, want %s", what, actual, expected)
	}
}

func parseVector(t *testing.T, what string, values []string) alloc.Amounts {
	t.Helper()

	if len(values) != alloc.TokenCount {
		t.Fatalf("%s holds %d values, want %d", what, len(values), alloc.TokenCount)
	}

	var out alloc.Amounts
	for i, v := range values {
		out[i] = parseInt(t, fmt.Sprintf("%s %d", what, i), v)
	}

	return out
}

func parseInt(t *testing.T, what, value string) *big.Int {
	t.Helper()

	// Decimal strings, not JSON numbers: a uint256 does not survive a float64.
	x, ok := new(big.Int).SetString(value, 10)
	if !ok {
		t.Fatalf("%s: %q is not a decimal integer", what, value)
	}

	return x
}

func parseAddress(t *testing.T, s string) twab.Address {
	t.Helper()

	var a twab.Address
	decodeHex(t, s, a[:])

	return a
}

func parseHash(t *testing.T, s string) merkle.Hash {
	t.Helper()

	var h merkle.Hash
	decodeHex(t, s, h[:])

	return h
}

func decodeHex(t *testing.T, s string, into []byte) {
	t.Helper()

	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		t.Fatalf("decoding hex %q: %v", s, err)
	}
	if len(b) != len(into) {
		t.Fatalf("hex %q decodes to %d bytes, want %d", s, len(b), len(into))
	}

	copy(into, b)
}
