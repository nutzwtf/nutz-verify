package merkle_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/merkle"
)

// Values a static serialiser could otherwise truncate into a wrong but valid leaf.
func TestNew_RejectsUnencodableAmounts(t *testing.T) {
	t.Parallel()

	valid := func() merkle.Claim {
		c := merkle.Claim{ID: big.NewInt(1), Account: merkle.Address{0xaa}}
		for i := range c.Amounts {
			c.Amounts[i] = big.NewInt(int64(i))
		}

		return c
	}

	tests := []struct {
		name        string
		mutate      func(*merkle.Claim)
		expectedMsg string
	}{
		{
			name:        "nil amount",
			mutate:      func(c *merkle.Claim) { c.Amounts[2] = nil },
			expectedMsg: "amount 2: is nil",
		},
		{
			name:        "nil id",
			mutate:      func(c *merkle.Claim) { c.ID = nil },
			expectedMsg: "id: is nil",
		},
		{
			name:        "negative amount",
			mutate:      func(c *merkle.Claim) { c.Amounts[0] = big.NewInt(-1) },
			expectedMsg: "amount 0: is negative",
		},
		{
			name:        "amount wider than uint256",
			mutate:      func(c *merkle.Claim) { c.Amounts[4] = new(big.Int).Lsh(big.NewInt(1), 256) },
			expectedMsg: "amount 4: needs 257 bits",
		},
		{
			name:        "id wider than uint256",
			mutate:      func(c *merkle.Claim) { c.ID = new(big.Int).Lsh(big.NewInt(1), 300) },
			expectedMsg: "id: needs 301 bits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			claim := valid()
			tt.mutate(&claim)

			// Second in the slice, so the error has to name which claim broke.
			_, err := merkle.New([]merkle.Claim{valid(), claim})
			if err == nil {
				t.Fatal("New accepted an unencodable claim")
			}

			if !strings.Contains(err.Error(), tt.expectedMsg) {
				t.Errorf("error = %q, want it to contain %q", err, tt.expectedMsg)
			}

			if !strings.Contains(err.Error(), "claim 1") {
				t.Errorf("error = %q, want it to name claim 1", err)
			}
		})
	}
}

// The boundary the previous test stops one bit short of.
func TestNew_MaxUint256Encodes(t *testing.T) {
	t.Parallel()

	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	c := merkle.Claim{ID: maxUint256, Account: merkle.Address{0xff}}
	for i := range c.Amounts {
		c.Amounts[i] = maxUint256
	}

	if _, err := merkle.New([]merkle.Claim{c}); err != nil {
		t.Fatalf("New rejected the largest representable claim: %v", err)
	}
}

// big.Int has in-place methods, so encoding must not corrupt the caller's allocations.
func TestNew_DoesNotMutateClaims(t *testing.T) {
	t.Parallel()

	f := loadFixture(t, fixturePath("claims.json"))
	claims := f.claims(t)

	before := make([]string, 0, len(claims))
	for _, c := range claims {
		before = append(before, describe(c))
	}

	if _, err := merkle.New(claims); err != nil {
		t.Fatalf("building tree: %v", err)
	}

	for i, c := range claims {
		if got := describe(c); got != before[i] {
			t.Errorf("claim %d changed during New: %s, was %s", i, got, before[i])
		}
	}
}

func describe(c merkle.Claim) string {
	parts := []string{c.ID.String()}
	for _, a := range c.Amounts {
		parts = append(parts, a.String())
	}

	return strings.Join(parts, "/")
}

// Fixtures come from @openzeppelin/merkle-tree v1.0.8 (see tooling/), never from this
// package, which would only prove the port agrees with itself.
const fixtureDir = "../testdata/merkle"

func fixturePath(name string) string { return filepath.Join(fixtureDir, name) }

// fixture mirrors the JSON from tooling/src/gen-fixtures.ts.
type fixture struct {
	Encoding []string       `json:"encoding"`
	ID       string         `json:"id"`
	Root     string         `json:"root"`
	Claims   []fixtureClaim `json:"claims"`
	// Dump is OpenZeppelin's dump() of the same tree, absent from claims.json.
	Dump json.RawMessage `json:"dump"`
}

type fixtureClaim struct {
	Account string   `json:"account"`
	Amounts []string `json:"amounts"`
	Proof   []string `json:"proof"`
	Leaf    string   `json:"leaf"`
}

func loadFixture(t *testing.T, path string) fixture {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	var f fixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parsing fixture %s: %v", path, err)
	}

	return f
}

func loadTree(t *testing.T, path string) (fixture, *merkle.Tree) {
	t.Helper()

	f := loadFixture(t, path)

	tree, err := merkle.New(f.claims(t))
	if err != nil {
		t.Fatalf("building tree from %s: %v", path, err)
	}

	return f, tree
}

// Fails if a regenerated fixture changed the leaf shape this package implements.
func (f fixture) requireLeafEncoding(t *testing.T) {
	t.Helper()

	expected := []string{"uint256", "address", "uint256[5]"}
	if !slices.Equal(f.Encoding, expected) {
		t.Fatalf("fixture encoding %v, want %v", f.Encoding, expected)
	}
}

// claims converts to the package type, keeping the order the generator used.
func (f fixture) claims(t *testing.T) []merkle.Claim {
	t.Helper()

	id, ok := new(big.Int).SetString(f.ID, 10)
	if !ok {
		t.Fatalf("fixture id %q is not a decimal integer", f.ID)
	}

	out := make([]merkle.Claim, 0, len(f.Claims))
	for i, c := range f.Claims {
		claim := merkle.Claim{ID: id, Account: parseAddress(t, c.Account)}
		if len(c.Amounts) != len(claim.Amounts) {
			t.Fatalf("claim %d: got %d amounts, want %d", i, len(c.Amounts), len(claim.Amounts))
		}

		for j, a := range c.Amounts {
			amount, ok := new(big.Int).SetString(a, 10)
			if !ok {
				t.Fatalf("claim %d amount %d: %q is not a decimal integer", i, j, a)
			}
			claim.Amounts[j] = amount
		}

		out = append(out, claim)
	}

	return out
}

func parseAddress(t *testing.T, s string) merkle.Address {
	t.Helper()

	var a merkle.Address
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
