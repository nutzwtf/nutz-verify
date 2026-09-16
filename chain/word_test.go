package chain

import (
	"bytes"
	"math/big"
	"strings"
	"testing"
)

func TestParseQuantity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want uint64
	}{
		{"0x0", 0},
		{"0x1", 1},
		{"0xff", 255},
		{"0xFF", 255}, // providers are not consistent about case
		{"0x0000ff", 255},
		{"0xffffffffffffffff", 1<<64 - 1},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := parseQuantity(tt.in)
			if err != nil {
				t.Fatalf("parseQuantity(%q) = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseQuantity(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseQuantity_Refuses(t *testing.T) {
	t.Parallel()

	// A quantity we cannot read is not a quantity we may guess at: every one of these
	// would otherwise decode as a plausible block number or log index.
	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"no prefix", "2a"},
		{"prefix only", "0x"},
		{"decimal", "42"},
		{"not hex", "0xzz"},
		{"overflows uint64", "0x10000000000000000"},
		{"negative", "-0x1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := parseQuantity(tt.in); err == nil {
				t.Errorf("parseQuantity(%q) = nil error, want refusal", tt.in)
			}
		})
	}
}

func TestQuantity_RoundTrips(t *testing.T) {
	t.Parallel()

	for _, n := range []uint64{0, 1, 15, 16, 2000, 1<<32 - 1, 1<<64 - 1} {
		encoded := quantity(n)
		if !strings.HasPrefix(encoded, "0x") {
			t.Errorf("quantity(%d) = %q, want a 0x prefix", n, encoded)
		}

		got, err := parseQuantity(encoded)
		if err != nil {
			t.Fatalf("parseQuantity(quantity(%d)) = %v", n, err)
		}
		if got != n {
			t.Errorf("parseQuantity(quantity(%d)) = %d", n, got)
		}
	}
}

func TestParseHexBytes(t *testing.T) {
	t.Parallel()

	got, err := parseHexBytes("0xdeadBEEF")
	if err != nil {
		t.Fatalf("parseHexBytes = %v", err)
	}
	if want := []byte{0xde, 0xad, 0xbe, 0xef}; !bytes.Equal(got, want) {
		t.Errorf("parseHexBytes = %x, want %x", got, want)
	}

	empty, err := parseHexBytes("0x")
	if err != nil {
		t.Fatalf("parseHexBytes(0x) = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("parseHexBytes(0x) = %x, want empty", empty)
	}

	// An odd digit count is a truncated response, not a number with a missing nibble.
	for _, in := range []string{"0xabc", "abcd", "0xgg"} {
		if _, err := parseHexBytes(in); err == nil {
			t.Errorf("parseHexBytes(%q) = nil error, want refusal", in)
		}
	}
}

func TestParseAddress(t *testing.T) {
	t.Parallel()

	got, err := ParseAddress("0x000000000000000000000000000000000000dEaD")
	if err != nil {
		t.Fatalf("ParseAddress = %v", err)
	}
	if got[19] != 0xad || got[18] != 0xde {
		t.Errorf("ParseAddress = %x", got)
	}
	if want := "0x000000000000000000000000000000000000dead"; got.String() != want {
		t.Errorf("String = %q, want %q", got, want)
	}

	for _, in := range []string{"", "0x", "0xdead", "0x" + strings.Repeat("00", 21)} {
		if _, err := ParseAddress(in); err == nil {
			t.Errorf("ParseAddress(%q) = nil error, want refusal", in)
		}
	}
}

func TestWordAddress_RefusesADirtyWord(t *testing.T) {
	t.Parallel()

	// An address is right-aligned in its word. A non-zero upper twelve bytes means the word
	// is not an address at all — truncating to the low twenty would invent a Holder.
	var clean Hash
	clean[31] = 0x01
	if _, err := wordAddress(clean[:]); err != nil {
		t.Fatalf("wordAddress(clean) = %v", err)
	}

	dirty := clean
	dirty[11] = 0x01
	if _, err := wordAddress(dirty[:]); err == nil {
		t.Error("wordAddress(dirty) = nil error, want refusal")
	}
}

func TestWordUint64_RefusesOverflow(t *testing.T) {
	t.Parallel()

	var word Hash
	word[23] = 0x01 // bit 64: one past what a uint64 holds

	if _, err := wordUint64(word[:]); err == nil {
		t.Error("wordUint64 = nil error, want refusal")
	}

	word = Hash{}
	for i := 24; i < 32; i++ {
		word[i] = 0xff
	}
	got, err := wordUint64(word[:])
	if err != nil {
		t.Fatalf("wordUint64 = %v", err)
	}
	if got != 1<<64-1 {
		t.Errorf("wordUint64 = %d, want MaxUint64", got)
	}
}

func TestWordBool(t *testing.T) {
	t.Parallel()

	var word Hash
	if v, err := wordBool(word[:]); err != nil || v {
		t.Errorf("wordBool(0) = %v, %v", v, err)
	}

	word[31] = 1
	if v, err := wordBool(word[:]); err != nil || !v {
		t.Errorf("wordBool(1) = %v, %v", v, err)
	}

	// Solidity only ever encodes 0 or 1; anything else is a word we have misread.
	word[31] = 2
	if _, err := wordBool(word[:]); err == nil {
		t.Error("wordBool(2) = nil error, want refusal")
	}
}

func TestWordUint256(t *testing.T) {
	t.Parallel()

	var word Hash
	for i := range word {
		word[i] = 0xff
	}

	got := wordUint256(word[:])
	want := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	if got.Cmp(want) != 0 {
		t.Errorf("wordUint256 = %s, want 2**256-1", got)
	}
}

func TestKeccak256_MatchesTheKnownEmptyDigest(t *testing.T) {
	t.Parallel()

	// The most-quoted constant in Ethereum, and the one that catches sha3-256 having been
	// used in place of legacy keccak — the two differ only in a padding byte.
	const empty = "c5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470"

	if got := keccak256(nil); hexOf(got[:]) != empty {
		t.Errorf("keccak256() = %s, want %s", hexOf(got[:]), empty)
	}
}

func hexOf(b []byte) string {
	return strings.TrimPrefix(hexBytes(b), "0x")
}
