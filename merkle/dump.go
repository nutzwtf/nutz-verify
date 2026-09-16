package merkle

import (
	"encoding/hex"
	"math/big"
)

// DumpFormat is the format tag OpenZeppelin's StandardMerkleTree.dump() writes and load()
// requires.
const DumpFormat = "standard-v1"

// LeafEncoding is the Distributor's leaf shape, as OpenZeppelin spells it.
var LeafEncoding = []string{"uint256", "address", "uint256[5]"}

// Dump is OpenZeppelin's standard-v1 dump of a Tree: what `StandardMerkleTree.load` reads
// back, and the shape of a published Bundle's tree.json. Marshal it with encoding/json.
//
// Values are in New's claim order, each with the node index its leaf landed at; the OZ
// library derives proofs from that index, and so can anyone who loads the file with it.
type Dump struct {
	Format       string      `json:"format"`
	LeafEncoding []string    `json:"leafEncoding"`
	Tree         []string    `json:"tree"`
	Values       []DumpValue `json:"values"`
}

// DumpValue is one claim as OZ writes it from string inputs: the id and every amount as a
// decimal string, the account as lowercase 0x-hex. Nothing here is a JSON number: a uint256
// does not survive one.
type DumpValue struct {
	Value     [3]any `json:"value"` // [id, account, amounts[5]]
	TreeIndex int    `json:"treeIndex"`
}

// Dump is the tree as OpenZeppelin would dump it.
func (t *Tree) Dump() Dump {
	nodes := make([]string, len(t.nodes))
	for i, n := range t.nodes {
		nodes[i] = hexOf(n[:])
	}

	values := make([]DumpValue, len(t.encoded))
	for i, e := range t.encoded {
		amounts := make([]string, AmountCount)
		for j := range amounts {
			at := (2 + j) * wordSize
			amounts[j] = new(big.Int).SetBytes(e[at : at+wordSize]).String()
		}

		values[i] = DumpValue{
			Value: [3]any{
				new(big.Int).SetBytes(e[0:wordSize]).String(),
				hexOf(e[2*wordSize-len(Address{}) : 2*wordSize]),
				amounts,
			},
			TreeIndex: t.leafAt[i],
		}
	}

	return Dump{Format: DumpFormat, LeafEncoding: LeafEncoding, Tree: nodes, Values: values}
}

// TreeIndex is the node index of New's i-th claim's leaf: the dump's treeIndex, and the
// leafIndex a Bundle records. Out-of-range i panics, like a slice index.
func (t *Tree) TreeIndex(i int) int { return t.leafAt[i] }

func hexOf(b []byte) string { return "0x" + hex.EncodeToString(b) }
