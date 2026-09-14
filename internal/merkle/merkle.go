// Package merkle ports OpenZeppelin StandardMerkleTree v1.0.8 for the Distributor's leaf
// shape: abi.encode(uint256 id, address account, uint256[5] amounts).
package merkle

import (
	"errors"
	"fmt"
	"math/big"

	"golang.org/x/crypto/sha3"
)

// AmountCount is the number of reward tokens a claim carries.
const AmountCount = 5

// wordSize is one ABI word, and a keccak-256 digest.
const wordSize = 32

// Hash is a keccak-256 digest.
type Hash [wordSize]byte

// Address is a 20-byte Ethereum address.
type Address [20]byte

// Claim is one row of a Root. Amounts must be non-nil, non-negative and below 2**256.
type Claim struct {
	ID      *big.Int
	Account Address
	Amounts [AmountCount]*big.Int
}

// leafWords is the ABI word count: id, account, and one per amount.
const leafWords = 2 + AmountCount

// encode writes abi.encode(uint256, address, uint256[5]): 224 static big-endian words.
func (c Claim) encode() ([leafWords * wordSize]byte, error) {
	var out [leafWords * wordSize]byte

	if err := putUint256(out[0:wordSize], c.ID); err != nil {
		return out, fmt.Errorf("id: %w", err)
	}

	// An address is right-aligned in its word.
	copy(out[2*wordSize-len(c.Account):2*wordSize], c.Account[:])

	for i, amount := range c.Amounts {
		at := (2 + i) * wordSize
		if err := putUint256(out[at:at+wordSize], amount); err != nil {
			return out, fmt.Errorf("amount %d: %w", i, err)
		}
	}

	return out, nil
}

// Fragments, not sentinels: New always wraps these as "merkle: claim N: amount M: ...".
var (
	errNilAmount      = errors.New("is nil, which is not the same as zero")
	errNegativeAmount = errors.New("is negative, and uint256 has no sign")
)

// putUint256 writes x big-endian into a word, refusing what uint256 cannot hold.
func putUint256(word []byte, x *big.Int) error {
	switch {
	case x == nil:
		return errNilAmount
	case x.Sign() < 0:
		return errNegativeAmount
	case x.BitLen() > 8*wordSize:
		return fmt.Errorf("needs %d bits, and uint256 holds %d", x.BitLen(), 8*wordSize)
	}

	x.FillBytes(word)

	return nil
}

// leafHash double-hashes, so a leaf can never collide with a 64-byte internal node.
func (c Claim) leafHash() (Hash, error) {
	encoded, err := c.encode()
	if err != nil {
		return Hash{}, err
	}

	inner := keccak256(encoded[:])

	return keccak256(inner[:]), nil
}

func keccak256(parts ...[]byte) Hash {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}

	var out Hash
	h.Sum(out[:0])

	return out
}
