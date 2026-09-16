package chain

import (
	"bytes"
	"math/big"
	"testing"
)

// Selectors computed independently with `cast sig`, for the same reason the topic0 hashes
// are: a selector built from a drifted signature hits the fallback and returns empty data,
// which decodes as "no ledger" rather than as an error.
const (
	ledgerSelector   = "0x0640e9e9"
	excludedSelector = "0x0bdca9a1"
)

func TestSelectors_MatchTheContractSignatures(t *testing.T) {
	t.Parallel()

	if got := hexBytes(selectorLedger[:]); got != ledgerSelector {
		t.Errorf("ledger selector = %s, want %s", got, ledgerSelector)
	}
	if got := hexBytes(selectorExcluded[:]); got != excludedSelector {
		t.Errorf("excluded selector = %s, want %s", got, excludedSelector)
	}
}

func TestEncodeLedgerCall(t *testing.T) {
	t.Parallel()

	got := encodeLedgerCall(KindDraw, 4242)

	if len(got) != 4+2*wordSize {
		t.Fatalf("calldata is %d bytes, want %d", len(got), 4+2*wordSize)
	}
	if !bytes.Equal(got[:4], selectorLedger[:]) {
		t.Errorf("selector = %x", got[:4])
	}

	kind, err := wordUint64(got[4 : 4+wordSize])
	if err != nil || kind != uint64(KindDraw) {
		t.Errorf("kind word = %x", got[4:4+wordSize])
	}
	id, err := wordUint64(got[4+wordSize:])
	if err != nil || id != 4242 {
		t.Errorf("id word = %x", got[4+wordSize:])
	}
}

func TestDecodeLedger(t *testing.T) {
	t.Parallel()

	root := repeatHash(0x5a)
	funded := amountsOf(11, 12, 13, 14, 15)
	totals := amountsOf(1, 2, 3, 4, 5)
	claimed := amountsOf(0, 0, 3, 0, 0)

	// Ledger is a tuple of bytes32, uint256, bool and three uint256[5]: every component is
	// static, so the tuple is static too and is returned inline with no head offset. Eighteen
	// words, in declaration order.
	var data []byte
	data = append(data, root[:]...)
	data = append(data, word(big.NewInt(1_700_000_000))...)
	data = append(data, word(big.NewInt(1))...)
	for _, group := range []Amounts{funded, totals, claimed} {
		for _, a := range group {
			data = append(data, word(a)...)
		}
	}

	got, err := decodeLedger(data)
	if err != nil {
		t.Fatalf("decodeLedger = %v", err)
	}

	if got.Root != root {
		t.Errorf("root = %s, want %s", got.Root, root)
	}
	if got.RootPostedAt != 1_700_000_000 {
		t.Errorf("rootPostedAt = %d", got.RootPostedAt)
	}
	if !got.Skipped {
		t.Error("skipped = false, want true")
	}
	if !got.HasRoot() {
		t.Error("HasRoot = false, want true")
	}
	assertAmounts(t, "funded", got.Funded, funded)
	assertAmounts(t, "totals", got.Totals, totals)
	assertAmounts(t, "claimed", got.Claimed, claimed)
}

func TestDecodeLedger_Refuses(t *testing.T) {
	t.Parallel()

	full := make([]byte, ledgerWords*wordSize)

	skipped := make([]byte, ledgerWords*wordSize)
	skipped[2*wordSize+31] = 2 // a bool Solidity would never encode

	tests := []struct {
		name string
		data []byte
	}{
		// A call to a contract with no code returns empty data. Reading that as an all-zero
		// Ledger would report "no Root posted" for a Distributor we failed to find.
		{"nothing at all", nil},
		{"seventeen words", full[:17*wordSize]},
		{"nineteen words", make([]byte, 19*wordSize)},
		{"a partial word", full[:ledgerWords*wordSize-1]},
		{"a skipped flag that is not a bool", skipped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := decodeLedger(tt.data); err == nil {
				t.Error("decodeLedger = nil error, want refusal")
			}
		})
	}
}

func TestDecodeAddressArray(t *testing.T) {
	t.Parallel()

	want := []Address{repeatAddr(0xa1), repeatAddr(0xb2), repeatAddr(0xc3)}

	got, err := decodeAddressArray(encodeAddressArray(want))
	if err != nil {
		t.Fatalf("decodeAddressArray = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	empty, err := decodeAddressArray(encodeAddressArray(nil))
	if err != nil {
		t.Fatalf("decodeAddressArray(empty) = %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty array decoded to %d entries", len(empty))
	}
}

func TestDecodeAddressArray_Refuses(t *testing.T) {
	t.Parallel()

	one := encodeAddressArray([]Address{repeatAddr(0xa1)})

	shortOffset := append([]byte{}, one...)
	shortOffset[wordSize-1] = 0x40 // points past the length word

	dirty := append([]byte{}, one...)
	dirty[2*wordSize] = 0x01 // upper bytes of the element word

	lying := append([]byte{}, one...)
	lying[2*wordSize-1] = 0x02 // claims two elements and carries one

	tests := []struct {
		name string
		data []byte
	}{
		{"nothing at all", nil},
		{"a header with no length", one[:wordSize]},
		{"an offset that is not 0x20", shortOffset},
		{"a length longer than the tail", lying},
		{"an element that is not an address", dirty},
		{"a trailing partial word", append(append([]byte{}, one...), 0x00)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := decodeAddressArray(tt.data); err == nil {
				t.Error("decodeAddressArray = nil error, want refusal")
			}
		})
	}
}

// encodeAddressArray writes the dynamic return shape abi.encode(address[]) has: a head
// offset, a length, then one right-aligned word per address.
func encodeAddressArray(addrs []Address) []byte {
	out := make([]byte, 0, (2+len(addrs))*wordSize)
	out = append(out, word(big.NewInt(wordSize))...)
	out = append(out, word(big.NewInt(int64(len(addrs))))...)
	for _, a := range addrs {
		topic := addressTopic(a)
		out = append(out, topic[:]...)
	}

	return out
}
