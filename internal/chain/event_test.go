package chain

import (
	"math/big"
	"testing"
)

// The three topic0 hashes, computed independently with `cast keccak` rather than by the code
// under test. A signature that drifts from the contract's — an enum written as `Kind` instead
// of `uint8`, a `uint256[5]` written `uint256[]` — produces a topic that matches no log at
// all, and the silent result is an Epoch with no transfers and no Root rather than an error.
const (
	transferTopic0         = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"
	rootPostedTopic0       = "0xc377a6b62e14fbb0940036b993035cc0e835a02fb65ae7504809cd31e12849f4"
	excludedAppendedTopic0 = "0xbfea3669337025af0c433482968db0e731a560837b2070705f134fdd04e2ff13"
)

func TestTopics_MatchTheContractSignatures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		topic Hash
		want  string
	}{
		{"Transfer", topicTransfer, transferTopic0},
		{"RootPosted", topicRootPosted, rootPostedTopic0},
		{"ExcludedAppended", topicExcludedAppended, excludedAppendedTopic0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.topic.String(); got != tt.want {
				t.Errorf("topic0 = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDecodeTransfer(t *testing.T) {
	t.Parallel()

	alice, bob := repeatAddr(0xa1), repeatAddr(0xb2)

	got, err := decodeTransfer(eventLog{
		Topics:      []Hash{topicTransfer, addressTopic(alice), addressTopic(bob)},
		Data:        word(big.NewInt(1234)),
		BlockNumber: 99,
		LogIndex:    7,
	})
	if err != nil {
		t.Fatalf("decodeTransfer = %v", err)
	}

	if got.From != alice || got.To != bob {
		t.Errorf("from/to = %s/%s, want %s/%s", got.From, got.To, alice, bob)
	}
	if got.Value.Cmp(big.NewInt(1234)) != 0 {
		t.Errorf("value = %s, want 1234", got.Value)
	}
	if got.BlockNumber != 99 || got.LogIndex != 7 {
		t.Errorf("block/index = %d/%d, want 99/7", got.BlockNumber, got.LogIndex)
	}
}

func TestDecodeTransfer_Refuses(t *testing.T) {
	t.Parallel()

	alice, bob := repeatAddr(0xa1), repeatAddr(0xb2)
	full := []Hash{topicTransfer, addressTopic(alice), addressTopic(bob)}

	dirty := addressTopic(alice)
	dirty[0] = 0x01

	tests := []struct {
		name   string
		topics []Hash
		data   []byte
	}{
		{"a Transfer with only one indexed argument", full[:2], word(big.NewInt(1))},
		{"a topic count no ERC-20 produces", append(full, Hash{}), word(big.NewInt(1))},
		{"a value word that is not there", full, nil},
		{"a value with a second word after it", full, append(word(big.NewInt(1)), word(big.NewInt(2))...)},
		{"a from topic that is not an address", []Hash{topicTransfer, dirty, full[2]}, word(big.NewInt(1))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := decodeTransfer(eventLog{Topics: tt.topics, Data: tt.data}); err == nil {
				t.Error("decodeTransfer = nil error, want refusal")
			}
		})
	}
}

func TestDecodeRootPosted(t *testing.T) {
	t.Parallel()

	root := repeatHash(0x7e)
	totals := amountsOf(1, 2, 3, 4, 5)
	carryIn := amountsOf(10, 20, 30, 40, 50)

	data := append([]byte{}, root[:]...)
	for _, a := range totals {
		data = append(data, word(a)...)
	}
	for _, a := range carryIn {
		data = append(data, word(a)...)
	}

	got, err := decodeRootPosted(eventLog{
		Topics: []Hash{topicRootPosted, word32(uint64(KindEpoch)), word32(4242)},
		Data:   data,
	})
	if err != nil {
		t.Fatalf("decodeRootPosted = %v", err)
	}

	if got.Kind != KindEpoch || got.ID != 4242 {
		t.Errorf("kind/id = %s/%d, want epoch/4242", got.Kind, got.ID)
	}
	if got.Root != root {
		t.Errorf("root = %s, want %s", got.Root, root)
	}
	assertAmounts(t, "totals", got.Totals, totals)
	assertAmounts(t, "carryIn", got.CarryIn, carryIn)
}

func TestDecodeRootPosted_Refuses(t *testing.T) {
	t.Parallel()

	eleven := make([]byte, 11*wordSize)

	tests := []struct {
		name   string
		topics []Hash
		data   []byte
	}{
		// Eleven words is the whole point: root, totals[5], carryIn[5], all static and
		// inline. Ten would decode as a Root with someone else's Carry.
		{"ten data words", []Hash{topicRootPosted, word32(0), word32(1)}, eleven[:10*wordSize]},
		{"twelve data words", []Hash{topicRootPosted, word32(0), word32(1)}, make([]byte, 12*wordSize)},
		{"one indexed argument", []Hash{topicRootPosted, word32(0)}, eleven},
		{"a kind outside the enum", []Hash{topicRootPosted, word32(2), word32(1)}, eleven},
		{"a period id no clock produces", []Hash{topicRootPosted, word32(0), repeatHash(0xff)}, eleven},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := decodeRootPosted(eventLog{Topics: tt.topics, Data: tt.data}); err == nil {
				t.Error("decodeRootPosted = nil error, want refusal")
			}
		})
	}
}

func TestDecodeExcludedAppended(t *testing.T) {
	t.Parallel()

	carol := repeatAddr(0xc3)

	got, err := decodeExcludedAppended(eventLog{
		Topics:      []Hash{topicExcludedAppended, addressTopic(carol)},
		BlockNumber: 12,
		LogIndex:    3,
	})
	if err != nil {
		t.Fatalf("decodeExcludedAppended = %v", err)
	}
	if got.Account != carol {
		t.Errorf("account = %s, want %s", got.Account, carol)
	}
	if got.BlockNumber != 12 || got.LogIndex != 3 {
		t.Errorf("block/index = %d/%d, want 12/3", got.BlockNumber, got.LogIndex)
	}

	// The account is indexed, so it is never in the data. Anything there is a different event.
	bad := eventLog{Topics: []Hash{topicExcludedAppended, addressTopic(carol)}, Data: word(big.NewInt(1))}
	if _, err := decodeExcludedAppended(bad); err == nil {
		t.Error("decodeExcludedAppended with data = nil error, want refusal")
	}
}

func TestDecoders_RefuseTheWrongEvent(t *testing.T) {
	t.Parallel()

	// eth_getLogs is filtered on topic0, so a log with the wrong one means the filter was
	// built wrong or the provider ignored it. Either way we are not looking at what we asked
	// for, and decoding it anyway is how a Transfer becomes a Root.
	stranger := []Hash{repeatHash(0x99), Hash{}, Hash{}}

	if _, err := decodeTransfer(eventLog{Topics: stranger, Data: word(big.NewInt(1))}); err == nil {
		t.Error("decodeTransfer accepted a foreign topic0")
	}
	if _, err := decodeRootPosted(eventLog{Topics: stranger, Data: make([]byte, 11*wordSize)}); err == nil {
		t.Error("decodeRootPosted accepted a foreign topic0")
	}
	if _, err := decodeExcludedAppended(eventLog{Topics: stranger[:2]}); err == nil {
		t.Error("decodeExcludedAppended accepted a foreign topic0")
	}
}

// --------------------------------------------------------------------- helpers

func repeatAddr(b byte) Address {
	var a Address
	for i := range a {
		a[i] = b
	}

	return a
}

func repeatHash(b byte) Hash {
	var h Hash
	for i := range h {
		h[i] = b
	}

	return h
}

// addressTopic is an address as Solidity indexes it: right-aligned in a word.
func addressTopic(a Address) Hash {
	var h Hash
	copy(h[wordSize-addressSize:], a[:])

	return h
}

func word32(n uint64) Hash {
	var h Hash
	new(big.Int).SetUint64(n).FillBytes(h[:])

	return h
}

func word(x *big.Int) []byte {
	out := make([]byte, wordSize)
	x.FillBytes(out)

	return out
}

func amountsOf(vs ...int64) Amounts {
	var out Amounts
	for i, v := range vs {
		out[i] = big.NewInt(v)
	}

	return out
}

func assertAmounts(t *testing.T, what string, got, want Amounts) {
	t.Helper()

	for i := range want {
		if got[i] == nil || got[i].Cmp(want[i]) != 0 {
			t.Errorf("%s[%d] = %v, want %s", what, i, got[i], want[i])
		}
	}
}
