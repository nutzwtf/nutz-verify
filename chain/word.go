package chain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"golang.org/x/crypto/sha3"
)

// wordSize is one ABI word, a keccak-256 digest, and a log topic.
const wordSize = 32

// addressSize is how much of a word an address occupies, right-aligned.
const addressSize = 20

// Fragments, not sentinels: every caller wraps these with what was being read.
var (
	errNoPrefix  = errors.New("does not start with 0x")
	errNoDigits  = errors.New("has no digits after 0x")
	errOddDigits = errors.New("has an odd number of hex digits, so a byte is missing")
)

// parseQuantity reads a JSON-RPC quantity: 0x-prefixed hex, leading zeros tolerated because
// providers disagree about whether to strip them.
//
// Refusing beats guessing here. "" and "0x" both arrive from a node that has no answer, and
// reading either as block zero would turn "I do not know" into a confident wrong number.
func parseQuantity(s string) (uint64, error) {
	digits, err := digitsOf(s)
	if err != nil {
		return 0, err
	}
	if digits == "" {
		return 0, errNoDigits
	}

	n, err := strconv.ParseUint(digits, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("is not a uint64 quantity: %w", err)
	}

	return n, nil
}

// quantity encodes a block number the way eth_getLogs and eth_getBlockByNumber want it.
func quantity(n uint64) string {
	return "0x" + strconv.FormatUint(n, 16)
}

// parseHexBytes reads 0x-prefixed hex data of any even length. Log data and topics are byte
// strings rather than quantities, so an odd digit count means the response was truncated.
func parseHexBytes(s string) ([]byte, error) {
	digits, err := digitsOf(s)
	if err != nil {
		return nil, err
	}
	if len(digits)%2 != 0 {
		return nil, errOddDigits
	}

	out, err := hex.DecodeString(digits)
	if err != nil {
		return nil, fmt.Errorf("is not hex: %w", err)
	}

	return out, nil
}

// hexBytes encodes data the way the JSON-RPC wire does.
func hexBytes(b []byte) string {
	return "0x" + hex.EncodeToString(b)
}

func digitsOf(s string) (string, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return "", errNoPrefix
	}

	return s[2:], nil
}

// parseHash reads a 32-byte value: a block hash, a Root or a topic.
func parseHash(s string) (Hash, error) {
	var h Hash

	raw, err := parseHexBytes(s)
	if err != nil {
		return h, err
	}
	if len(raw) != wordSize {
		return h, fmt.Errorf("is %d bytes, and a hash is %d", len(raw), wordSize)
	}

	copy(h[:], raw)

	return h, nil
}

// ParseAddress reads a 20-byte address. Case is ignored rather than checked: EIP-55 is a
// typo check for humans, and the addresses reaching this are already either a flag we echo
// back in the run header or a value the chain itself produced.
func ParseAddress(s string) (Address, error) {
	var a Address

	raw, err := parseHexBytes(s)
	if err != nil {
		return a, err
	}
	if len(raw) != addressSize {
		return a, fmt.Errorf("is %d bytes, and an address is %d", len(raw), addressSize)
	}

	copy(a[:], raw)

	return a, nil
}

// String is the lowercase 0x form, which is what the run header echoes and what a JSON
// report carries.
func (a Address) String() string {
	return hexBytes(a[:])
}

// String is the lowercase 0x form.
func (h Hash) String() string {
	return hexBytes(h[:])
}

// wordUint256 reads a word as an unsigned 256-bit integer. Every ABI word is a valid one,
// so this cannot fail.
func wordUint256(word []byte) *big.Int {
	return new(big.Int).SetBytes(word)
}

// wordUint64 reads a word that must fit a uint64 — a period id, a timestamp, a length.
//
// A value that does not fit is refused rather than truncated: an Epoch id is a unix second
// over 3600 and a timestamp is a unix second, so anything above 2**64 is a word we have
// misread, and silently keeping its low 64 bits would answer confidently about the wrong
// Epoch.
func wordUint64(word []byte) (uint64, error) {
	for _, b := range word[:wordSize-8] {
		if b != 0 {
			return 0, fmt.Errorf("is %#x, which does not fit a uint64", word)
		}
	}

	var n uint64
	for _, b := range word[wordSize-8:] {
		n = n<<8 | uint64(b)
	}

	return n, nil
}

// wordAddress reads an address from its right-aligned word.
//
// The upper twelve bytes must be zero. Solidity always zero-pads them, so a dirty word means
// this is not an address — a shifted response, a misindexed topic — and masking it down to
// twenty bytes would invent a Holder that does not exist.
func wordAddress(word []byte) (Address, error) {
	var a Address

	for _, b := range word[:wordSize-addressSize] {
		if b != 0 {
			return a, fmt.Errorf("is %#x, which is not a right-aligned address", word)
		}
	}

	copy(a[:], word[wordSize-addressSize:])

	return a, nil
}

// wordBool reads a word Solidity encoded as a bool: exactly 0 or 1. Anything else is a word
// we have misread.
func wordBool(word []byte) (bool, error) {
	n, err := wordUint64(word)
	if err != nil || n > 1 {
		return false, fmt.Errorf("is %#x, which is not a bool", word)
	}

	return n == 1, nil
}

// keccak256 is Ethereum's legacy keccak, not FIPS-202 SHA-3: the two differ by one padding
// byte, and stdlib crypto/sha3 has only the latter (ADR-0004).
func keccak256(parts ...[]byte) Hash {
	h := sha3.NewLegacyKeccak256()
	for _, p := range parts {
		h.Write(p)
	}

	var out Hash
	h.Sum(out[:0])

	return out
}
