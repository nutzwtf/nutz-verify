package chain

import "fmt"

// The three log signatures the Verifier reads, hashed rather than written out as literals so
// the signature stays legible next to the contract's declaration. All three are fixed at
// compile time, which is what makes hand-rolled decoding affordable (ADR-0004).
//
// The enum in RootPosted is `uint8` here because that is how a signature names it; writing
// `Kind` would hash to a topic no log ever carries, and the failure would be a silent
// absence of Roots rather than an error.
var (
	topicTransfer         = keccak256([]byte("Transfer(address,address,uint256)"))
	topicRootPosted       = keccak256([]byte("RootPosted(uint8,uint256,bytes32,uint256[5],uint256[5])"))
	topicExcludedAppended = keccak256([]byte("ExcludedAppended(address)"))
)

// eventLog is one decoded eth_getLogs entry, before it is read as any particular event.
type eventLog struct {
	Address     Address
	Topics      []Hash
	Data        []byte
	BlockNumber uint64
	BlockHash   Hash
	LogIndex    uint64
}

// check asserts the shape the event's signature fixes: its topic0, how many arguments are
// indexed, and how many static words the rest occupy.
//
// Every one of these is exact rather than a minimum. A log that is longer than the signature
// says is not this event with something appended — it is a different event whose topic0 we
// matched by accident, or a provider that ignored the filter, and reading its first words as
// ours is how a Transfer becomes a Root.
func (l eventLog) check(name string, topic0 Hash, topics, dataWords int) error {
	switch {
	case len(l.Topics) == 0 || l.Topics[0] != topic0:
		return fmt.Errorf("chain: %s: log at block %d index %d is not this event", name, l.BlockNumber, l.LogIndex)
	case len(l.Topics) != topics+1:
		return fmt.Errorf("chain: %s at block %d index %d: has %d topics, and this event has %d",
			name, l.BlockNumber, l.LogIndex, len(l.Topics), topics+1)
	case len(l.Data) != dataWords*wordSize:
		return fmt.Errorf("chain: %s at block %d index %d: has %d data bytes, and this event has %d",
			name, l.BlockNumber, l.LogIndex, len(l.Data), dataWords*wordSize)
	default:
		return nil
	}
}

// decodeTransfer reads Transfer(address indexed from, address indexed to, uint256 value):
// two indexed arguments and one data word.
func decodeTransfer(l eventLog) (Transfer, error) {
	if err := l.check("Transfer", topicTransfer, 2, 1); err != nil {
		return Transfer{}, err
	}

	from, err := wordAddress(l.Topics[1][:])
	if err != nil {
		return Transfer{}, fmt.Errorf("chain: Transfer at block %d index %d: from %w", l.BlockNumber, l.LogIndex, err)
	}

	to, err := wordAddress(l.Topics[2][:])
	if err != nil {
		return Transfer{}, fmt.Errorf("chain: Transfer at block %d index %d: to %w", l.BlockNumber, l.LogIndex, err)
	}

	return Transfer{
		BlockNumber: l.BlockNumber,
		BlockHash:   l.BlockHash,
		LogIndex:    l.LogIndex,
		From:        from,
		To:          to,
		Value:       wordUint256(l.Data),
	}, nil
}

// rootPostedWords is RootPosted's unindexed payload: root, totals[5], carryIn[5]. A fixed
// uint256[5] is encoded inline, so the whole tail is static and carries no offsets.
const rootPostedWords = 1 + 2*TokenCount

// decodeRootPosted reads
// RootPosted(Kind indexed kind, uint256 indexed id, bytes32 root, uint256[5] totals, uint256[5] carryIn).
func decodeRootPosted(l eventLog) (RootPosted, error) {
	if err := l.check("RootPosted", topicRootPosted, 2, rootPostedWords); err != nil {
		return RootPosted{}, err
	}

	at := fmt.Sprintf("chain: RootPosted at block %d index %d", l.BlockNumber, l.LogIndex)

	kind, err := wordUint64(l.Topics[1][:])
	if err != nil || kind > uint64(KindDraw) {
		return RootPosted{}, fmt.Errorf("%s: kind is %#x, and the enum has %d members",
			at, l.Topics[1], KindDraw+1)
	}

	id, err := wordUint64(l.Topics[2][:])
	if err != nil {
		return RootPosted{}, fmt.Errorf("%s: id %w", at, err)
	}

	posted := RootPosted{
		BlockNumber: l.BlockNumber,
		BlockHash:   l.BlockHash,
		LogIndex:    l.LogIndex,
		Kind:        Kind(kind),
		ID:          id,
	}
	copy(posted.Root[:], l.Data[:wordSize])

	for i := range posted.Totals {
		posted.Totals[i] = wordUint256(dataWord(l.Data, 1+i))
		posted.CarryIn[i] = wordUint256(dataWord(l.Data, 1+TokenCount+i))
	}

	return posted, nil
}

// decodeExcludedAppended reads ExcludedAppended(address indexed account): one indexed
// argument and nothing else.
func decodeExcludedAppended(l eventLog) (Exclusion, error) {
	if err := l.check("ExcludedAppended", topicExcludedAppended, 1, 0); err != nil {
		return Exclusion{}, err
	}

	account, err := wordAddress(l.Topics[1][:])
	if err != nil {
		return Exclusion{}, fmt.Errorf("chain: ExcludedAppended at block %d index %d: account %w",
			l.BlockNumber, l.LogIndex, err)
	}

	return Exclusion{
		BlockNumber: l.BlockNumber,
		BlockHash:   l.BlockHash,
		LogIndex:    l.LogIndex,
		Account:     account,
	}, nil
}

// dataWord is word i of an already length-checked static payload.
func dataWord(data []byte, i int) []byte {
	return data[i*wordSize : (i+1)*wordSize]
}
