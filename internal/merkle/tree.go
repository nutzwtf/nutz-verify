package merkle

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
)

// ErrNoClaims is returned by New for an empty claim set; a Skipped Epoch posts no Root.
var ErrNoClaims = errors.New("merkle: cannot build a tree with no claims")

// Tree is a complete binary tree in a flat 2n-1 slice: root at 0, children at 2i+1 and
// 2i+2, leaves filling the tail in reverse, ordered by hash rather than by arrival.
type Tree struct {
	nodes []Hash
	// leafAt[i] is the node index of New's i-th claim.
	leafAt []int
}

// New builds the tree over claims. It does not retain them.
func New(claims []Claim) (*Tree, error) {
	if len(claims) == 0 {
		return nil, ErrNoClaims
	}

	// Pair each leaf with its arrival position so sorting keeps the caller's numbering.
	type hashed struct {
		hash       Hash
		claimIndex int
	}

	leaves := make([]hashed, 0, len(claims))
	for i, c := range claims {
		h, err := c.leafHash()
		if err != nil {
			return nil, fmt.Errorf("merkle: claim %d: %w", i, err)
		}
		leaves = append(leaves, hashed{hash: h, claimIndex: i})
	}

	// Ascending by hash; stable, so repeated claims still map deterministically.
	slices.SortStableFunc(leaves, func(a, b hashed) int {
		return bytes.Compare(a.hash[:], b.hash[:])
	})

	t := &Tree{
		nodes:  make([]Hash, 2*len(claims)-1),
		leafAt: make([]int, len(claims)),
	}

	// Leaves fill the tail back to front, so the first sorted leaf lands last.
	for i, leaf := range leaves {
		at := len(t.nodes) - 1 - i
		t.nodes[at] = leaf.hash
		t.leafAt[leaf.claimIndex] = at
	}

	for i := len(t.nodes) - 1 - len(leaves); i >= 0; i-- {
		t.nodes[i] = hashPair(t.nodes[2*i+1], t.nodes[2*i+2])
	}

	return t, nil
}

// Len is the number of claims in the tree.
func (t *Tree) Len() int { return len(t.leafAt) }

// Root is the value compared against the posted Root. For one claim it is the leaf.
func (t *Tree) Root() Hash { return t.nodes[0] }

// Leaf is the leaf hash of New's i-th claim. Out-of-range i panics, like a slice index.
func (t *Tree) Leaf(i int) Hash { return t.nodes[t.leafAt[i]] }

// Proof is the sibling chain from New's i-th claim to the root. One claim yields none.
func (t *Tree) Proof(i int) []Hash {
	proof := []Hash{}
	for at := t.leafAt[i]; at > 0; at = (at - 1) / 2 {
		proof = append(proof, t.nodes[siblingIndex(at)])
	}

	return proof
}

// siblingIndex is the other child of at's parent; left children sit at odd indices.
func siblingIndex(at int) int {
	if at%2 == 0 {
		return at - 1
	}

	return at + 1
}

// Verify replays a proof the way the Distributor's MerkleProof.verify does.
func Verify(root, leaf Hash, proof []Hash) bool {
	computed := leaf
	for _, sibling := range proof {
		computed = hashPair(computed, sibling)
	}

	return computed == root
}

// hashPair hashes two nodes in ascending order, so proofs need not record sides.
func hashPair(a, b Hash) Hash {
	if bytes.Compare(a[:], b[:]) > 0 {
		a, b = b, a
	}

	return keccak256(a[:], b[:])
}
