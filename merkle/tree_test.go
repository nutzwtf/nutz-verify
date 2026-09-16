package merkle_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/nutzwtf/nutz-verify/merkle"
)

// Pinned by value, not read from claims.json: a swapped fixture must fail, not redefine.
const (
	contractsFixtureRoot   = "0x88b44d7a37b5a4d381e091853e53d8c9e7964de7e1a41037e7e41b0062877591"
	contractsFixtureClaims = 6
)

// Replays every fixture: roots alone miss a mis-shaped tree, so leaves and proofs are
// compared too. Glob-discovered, so dropping in a new fixture extends this test.
func TestNew_MatchesOpenZeppelinFixtures(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(fixturePath("*.json"))
	if err != nil {
		t.Fatalf("globbing fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no fixtures found in %s", fixtureDir)
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			f, tree := loadTree(t, path)
			f.requireLeafEncoding(t)

			root := parseHash(t, f.Root)
			if tree.Root() != root {
				t.Errorf("Root() = %#x, want %s", tree.Root(), f.Root)
			}

			if tree.Len() != len(f.Claims) {
				t.Errorf("Len() = %d, want %d", tree.Len(), len(f.Claims))
			}

			for i, c := range f.Claims {
				leaf := parseHash(t, c.Leaf)
				if tree.Leaf(i) != leaf {
					t.Errorf("Leaf(%d) = %#x, want %s", i, tree.Leaf(i), c.Leaf)
				}

				proof := tree.Proof(i)
				if len(proof) != len(c.Proof) {
					t.Fatalf("Proof(%d) has %d elements, want %d", i, len(proof), len(c.Proof))
				}

				for j, expected := range c.Proof {
					if proof[j] != parseHash(t, expected) {
						t.Errorf("Proof(%d)[%d] = %#x, want %s", i, j, proof[j], expected)
					}
				}

				if !merkle.Verify(root, leaf, proof) {
					t.Errorf("Verify rejected the proof for claim %d (%s)", i, c.Account)
				}
			}
		})
	}
}

// The port must reproduce the exact Root the Distributor's own suite verifies against.
func TestNew_PinsTheContractsFixture(t *testing.T) {
	t.Parallel()

	f, tree := loadTree(t, fixturePath("claims.json"))

	if len(f.Claims) != contractsFixtureClaims {
		t.Fatalf("claims.json holds %d claims, want %d — is this still the contracts fixture?",
			len(f.Claims), contractsFixtureClaims)
	}

	if f.Root != contractsFixtureRoot {
		t.Fatalf("claims.json declares root %s, want %s — the fixture has been replaced",
			f.Root, contractsFixtureRoot)
	}

	root := parseHash(t, contractsFixtureRoot)
	if tree.Root() != root {
		t.Fatalf("Root() = %#x, want %s", tree.Root(), contractsFixtureRoot)
	}

	for i := range f.Claims {
		if !merkle.Verify(root, tree.Leaf(i), tree.Proof(i)) {
			t.Errorf("proof %d does not verify against the pinned root", i)
		}
	}
}

// A Skipped Epoch has no tree, and must not become one with a zero-hash root.
func TestNew_RejectsEmptyClaims(t *testing.T) {
	t.Parallel()

	if _, err := merkle.New(nil); !errors.Is(err, merkle.ErrNoClaims) {
		t.Errorf("New(nil) error = %v, want ErrNoClaims", err)
	}

	if _, err := merkle.New([]merkle.Claim{}); !errors.Is(err, merkle.ErrNoClaims) {
		t.Errorf("New(empty) error = %v, want ErrNoClaims", err)
	}
}

// The point of sorting leaves: the Root must not depend on the caller's iteration order.
func TestNew_RootIsIndependentOfClaimOrder(t *testing.T) {
	t.Parallel()

	f, forward := loadTree(t, fixturePath("claims.json"))
	claims := f.claims(t)

	reversed := slices.Clone(claims)
	slices.Reverse(reversed)

	backward, err := merkle.New(reversed)
	if err != nil {
		t.Fatalf("building reversed tree: %v", err)
	}

	if forward.Root() != backward.Root() {
		t.Fatalf("Root() = %#x for reversed claims, want %#x", backward.Root(), forward.Root())
	}

	// Each claim keeps its own leaf and proof across the permutation.
	for i := range claims {
		j := len(claims) - 1 - i

		if forward.Leaf(i) != backward.Leaf(j) {
			t.Errorf("claim %d changed leaf when reordered", i)
		}

		if !slices.Equal(forward.Proof(i), backward.Proof(j)) {
			t.Errorf("claim %d changed proof when reordered", i)
		}
	}
}

// Keeps the sortLeaves fixture honest: sorted input would test nothing.
func TestUnsortedInputFixtureIsNotAlreadySorted(t *testing.T) {
	t.Parallel()

	_, tree := loadTree(t, fixturePath("unsorted-input.json"))

	if tree.Len() < 2 {
		t.Fatalf("fixture has %d claims, need at least 2 to have an order", tree.Len())
	}

	for i := 1; i < tree.Len(); i++ {
		previous, current := tree.Leaf(i-1), tree.Leaf(i)
		if bytes.Compare(previous[:], current[:]) > 0 {
			return // claim order differs from hash order, which is the point
		}
	}

	t.Fatal("unsorted-input.json is in ascending leaf-hash order, so it no longer " +
		"distinguishes a correct sortLeaves from a missing one; regenerate it")
}

// Proofs that all verify are worthless if proofs for the wrong leaf verify too.
func TestVerify_RejectsForgedProofs(t *testing.T) {
	t.Parallel()

	f, tree := loadTree(t, fixturePath("claims.json"))
	claims := f.claims(t)
	root := tree.Root()

	t.Run("proof of one claim does not verify another", func(t *testing.T) {
		t.Parallel()

		if merkle.Verify(root, tree.Leaf(0), tree.Proof(1)) {
			t.Error("claim 1's proof verified claim 0's leaf")
		}
	})

	t.Run("leaf not in the tree", func(t *testing.T) {
		t.Parallel()

		// Same account, one wei more in the first token.
		forged := claims[0]
		forged.Amounts[0] = new(big.Int).Add(forged.Amounts[0], big.NewInt(1))

		single, err := merkle.New([]merkle.Claim{forged})
		if err != nil {
			t.Fatalf("building single-claim tree: %v", err)
		}

		if merkle.Verify(root, single.Root(), tree.Proof(0)) {
			t.Error("a forged leaf verified against the real root")
		}
	})

	t.Run("truncated proof", func(t *testing.T) {
		t.Parallel()

		full := tree.Proof(0)
		if merkle.Verify(root, tree.Leaf(0), full[:len(full)-1]) {
			t.Error("a truncated proof verified")
		}
	})
}

// Dump must be what OpenZeppelin's own dump() wrote for the same claims, compared as
// parsed JSON: load() is the contract, not JSON.stringify's layout. claims.json comes from
// nutz-contracts without a dump and is skipped.
func TestDump_MatchesOpenZeppelinDump(t *testing.T) {
	t.Parallel()

	paths, err := filepath.Glob(fixturePath("*.json"))
	if err != nil {
		t.Fatalf("globbing fixtures: %v", err)
	}

	dumped := 0
	for _, path := range paths {
		f, tree := loadTree(t, path)
		if len(f.Dump) == 0 {
			continue
		}
		dumped++

		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()

			var expected any
			if err := json.Unmarshal(f.Dump, &expected); err != nil {
				t.Fatalf("parsing the fixture's dump: %v", err)
			}

			raw, err := json.Marshal(tree.Dump())
			if err != nil {
				t.Fatalf("marshalling Dump(): %v", err)
			}
			var got any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("re-parsing Dump(): %v", err)
			}

			if !reflect.DeepEqual(got, expected) {
				t.Errorf("Dump() differs from OpenZeppelin's:\n got %s\nwant %s", raw, f.Dump)
			}

			d := tree.Dump()
			for i := range f.Claims {
				if d.Values[i].TreeIndex != tree.TreeIndex(i) {
					t.Errorf("claim %d: Dump treeIndex %d, TreeIndex() %d", i, d.Values[i].TreeIndex, tree.TreeIndex(i))
				}
			}
		})
	}

	if dumped == 0 {
		t.Fatal("no fixture carries a dump; regenerate with tooling/ (pnpm gen:fixtures)")
	}
}
