package chain

import (
	"math/big"
	"strings"
	"testing"
)

// The wire decoders are where a hand-rolled JSON-RPC client actually fails: not on a
// well-formed response, but on a field a provider renders differently from the one we
// developed against. Every field is refused rather than defaulted, because a default here is
// an invented block number or an invented address.

func TestWireBlock_Decode(t *testing.T) {
	t.Parallel()

	sound := wireBlock{
		Number:     "0x2a",
		Hash:       "0x" + strings.Repeat("11", 32),
		ParentHash: "0x" + strings.Repeat("22", 32),
		Timestamp:  "0x64",
	}

	got, err := sound.decode()
	if err != nil {
		t.Fatalf("decode = %v", err)
	}
	if got.Number != 42 || got.Timestamp != 100 {
		t.Errorf("block = %+v", got)
	}
	if got.Hash != repeatHash(0x11) || got.ParentHash != repeatHash(0x22) {
		t.Errorf("hashes = %s / %s", got.Hash, got.ParentHash)
	}

	tests := []struct {
		name  string
		mutve func(*wireBlock)
	}{
		{"no number", func(w *wireBlock) { w.Number = "" }},
		{"a short hash", func(w *wireBlock) { w.Hash = "0xdead" }},
		{"a short parent hash", func(w *wireBlock) { w.ParentHash = "0xdead" }},
		{"no timestamp", func(w *wireBlock) { w.Timestamp = "" }},
		// A unix second past 2**63 is not a time; carrying it as a negative int64 would put
		// the block before the Epoch window and quietly drop its logs.
		{"a timestamp past int64", func(w *wireBlock) { w.Timestamp = "0xffffffffffffffff" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			broken := sound
			tt.mutve(&broken)

			if _, err := broken.decode(); err == nil {
				t.Error("decode = nil error, want a refusal")
			}
		})
	}
}

func TestWireLog_Decode(t *testing.T) {
	t.Parallel()

	sound := wireLog{
		Address:     nutz.String(),
		Topics:      []string{topicTransfer.String(), addressTopic(alice).String(), addressTopic(bob).String()},
		Data:        hexBytes(word(big.NewInt(9))),
		BlockNumber: "0x5",
		BlockHash:   "0x" + strings.Repeat("33", 32),
		LogIndex:    "0x2",
	}

	got, err := sound.decode()
	if err != nil {
		t.Fatalf("decode = %v", err)
	}
	if got.BlockNumber != 5 || got.LogIndex != 2 || got.Address != nutz {
		t.Errorf("log = %+v", got)
	}
	if len(got.Topics) != 3 {
		t.Errorf("topics = %v", got.Topics)
	}

	tests := []struct {
		name  string
		mutve func(*wireLog)
	}{
		{"removed by a reorg", func(w *wireLog) { w.Removed = true }},
		{"no block number", func(w *wireLog) { w.BlockNumber = "" }},
		{"no log index", func(w *wireLog) { w.LogIndex = "" }},
		{"a short block hash", func(w *wireLog) { w.BlockHash = "0xdead" }},
		{"a short address", func(w *wireLog) { w.Address = "0xdead" }},
		{"data with an odd digit count", func(w *wireLog) { w.Data = "0xabc" }},
		{"a topic that is not 32 bytes", func(w *wireLog) { w.Topics[1] = "0xdead" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			broken := sound
			broken.Topics = append([]string(nil), sound.Topics...)
			tt.mutve(&broken)

			if _, err := broken.decode(); err == nil {
				t.Error("decode = nil error, want a refusal")
			}
		})
	}
}

func TestParseHash_RefusesNonHex(t *testing.T) {
	t.Parallel()

	if _, err := parseHash("0x" + strings.Repeat("zz", 32)); err == nil {
		t.Error("parseHash = nil error, want a refusal")
	}
}

func TestNewEndpoint_RefusesAnUnparseableURL(t *testing.T) {
	t.Parallel()

	if _, err := newEndpoint("http://[::1", 1, nil, 1); err == nil {
		t.Error("newEndpoint = nil error, want a refusal")
	}
}

func TestDecodeTransfer_RefusesADirtyToTopic(t *testing.T) {
	t.Parallel()

	dirty := addressTopic(bob)
	dirty[0] = 0x01

	l := eventLog{
		Topics: []Hash{topicTransfer, addressTopic(alice), dirty},
		Data:   word(big.NewInt(1)),
	}
	if _, err := decodeTransfer(l); err == nil {
		t.Error("decodeTransfer = nil error, want a refusal")
	}
}

func TestDecodeExcludedAppended_RefusesADirtyAccountTopic(t *testing.T) {
	t.Parallel()

	dirty := addressTopic(alice)
	dirty[0] = 0x01

	if _, err := decodeExcludedAppended(eventLog{Topics: []Hash{topicExcludedAppended, dirty}}); err == nil {
		t.Error("decodeExcludedAppended = nil error, want a refusal")
	}
}

func TestDecodeLedger_RefusesAnImpossibleTimestamp(t *testing.T) {
	t.Parallel()

	// A rootPostedAt past 2**63 would land as a negative int64, and a negative posting time
	// reads as "inside the Dispute window" forever.
	tests := []struct {
		name string
		fill func([]byte)
	}{
		{"filling all 256 bits", func(word []byte) {
			for i := range word {
				word[i] = 0xff
			}
		}},
		{"one bit past int64", func(word []byte) { word[wordSize-8] = 0x80 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := make([]byte, ledgerWords*wordSize)
			tt.fill(data[wordSize : 2*wordSize])

			if _, err := decodeLedger(data); err == nil {
				t.Error("decodeLedger = nil error, want a refusal")
			}
		})
	}
}

func TestDecodeAddressArray_RefusesAnImpossibleLength(t *testing.T) {
	t.Parallel()

	data := make([]byte, 2*wordSize)
	data[wordSize-1] = wordSize // a well-formed head offset
	for i := range wordSize {
		data[wordSize+i] = 0xff // a length no array has
	}

	if _, err := decodeAddressArray(data); err == nil {
		t.Error("decodeAddressArray = nil error, want a refusal")
	}
}
