package chain

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeNode is a JSON-RPC server with a handful of blocks and logs, enough to drive the
// Reader without a chain. It records what it was asked so the tests can assert on the
// requests themselves — paging into 2,000-block ranges is a property of the calls made, not
// of the answer returned, and nothing else can see it.
type fakeNode struct {
	chainID uint64
	blocks  []Block // index is the block number
	tips    map[Finality]uint64
	logs    []wireLog

	// returns answers eth_call, keyed by "<calldata>@<block>". A miss returns "0x", which
	// is what a node with no code at the address does.
	returns map[string]string

	// failWith, when set, is returned as a JSON-RPC error for every call of that method.
	failWith map[string]string

	// override replaces a method's result outright, which is how a misbehaving provider is
	// staged: a quantity that is not hex, a header with a short hash, a log the filter did
	// not ask for.
	override map[string]any

	// always is returned by eth_getLogs whatever the filter said.
	always []wireLog

	// httpStatus, when non-zero, is returned instead of a JSON-RPC reply at all.
	httpStatus int

	// missing are block numbers the node answers null for, the way a node does for a block
	// it has pruned or never had.
	missing map[uint64]bool

	mu       sync.Mutex
	ranges   []string
	requests []string
}

func newFakeNode(blocks int, interval int64) *fakeNode {
	n := &fakeNode{
		chainID:  4663,
		tips:     map[Finality]uint64{},
		returns:  map[string]string{},
		failWith: map[string]string{},
		override: map[string]any{},
		missing:  map[uint64]bool{},
	}

	for i := range blocks {
		n.blocks = append(n.blocks, Block{
			Number:     uint64(i),
			Hash:       blockHash(uint64(i), 0),
			ParentHash: blockHash(uint64(i)-1, 0),
			Timestamp:  int64(i) * interval,
		})
	}

	last := uint64(blocks - 1)
	n.tips[Latest], n.tips[Safe], n.tips[Finalized] = last, last, last

	return n
}

// blockHash is a stand-in block hash. salt lets one node in a cross-check pair answer
// differently at the same height, which is the only shape of disagreement that matters.
func blockHash(number uint64, salt byte) Hash {
	h := keccak256(appendUint64(nil, number), []byte{salt})

	return h
}

func (n *fakeNode) serve(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(n.handle))
	t.Cleanup(server.Close)

	return server.URL
}

func (n *fakeNode) handle(w http.ResponseWriter, r *http.Request) {
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	n.mu.Lock()
	n.requests = append(n.requests, req.Method)
	n.mu.Unlock()

	if n.httpStatus != 0 {
		http.Error(w, "upstream is having a moment", n.httpStatus)

		return
	}

	if result, staged := n.override[req.Method]; staged {
		n.reply(w, req.ID, result, nil)

		return
	}

	if message, failing := n.failWith[req.Method]; failing {
		n.reply(w, req.ID, nil, &RPCError{Code: -32000, Message: message})

		return
	}

	result, err := n.dispatch(req)
	if err != nil {
		n.reply(w, req.ID, nil, &RPCError{Code: -32602, Message: err.Error()})

		return
	}

	n.reply(w, req.ID, result, nil)
}

func (n *fakeNode) dispatch(req rpcRequest) (any, error) {
	switch req.Method {
	case "eth_chainId":
		return quantity(n.chainID), nil
	case "eth_getBlockByNumber":
		return n.block(req.Params[0].(string)), nil
	case "eth_getLogs":
		return n.getLogs(req.Params[0].(map[string]any))
	case "eth_call":
		call := req.Params[0].(map[string]any)
		key := call["data"].(string) + "@" + req.Params[1].(string)
		if out, ok := n.returns[key]; ok {
			return out, nil
		}

		return "0x", nil
	default:
		return nil, fmt.Errorf("unsupported method %s", req.Method)
	}
}

// block answers a tag or a number, and null for a block it does not have — which is what a
// node without a safe or finalized tag does, and the case that must not decode as block zero.
func (n *fakeNode) block(tag string) any {
	number, ok := n.tips[Finality(tag)]
	if !ok {
		parsed, err := parseQuantity(tag)
		if err != nil || parsed >= uint64(len(n.blocks)) {
			return nil
		}

		number = parsed
	}

	if n.missing[number] {
		return nil
	}

	b := n.blocks[number]

	return wireBlock{
		Number:     quantity(b.Number),
		Hash:       b.Hash.String(),
		ParentHash: b.ParentHash.String(),
		Timestamp:  quantity(uint64(b.Timestamp)),
	}
}

func (n *fakeNode) getLogs(filter map[string]any) (any, error) {
	from, err := parseQuantity(filter["fromBlock"].(string))
	if err != nil {
		return nil, err
	}
	to, err := parseQuantity(filter["toBlock"].(string))
	if err != nil {
		return nil, err
	}

	n.mu.Lock()
	n.ranges = append(n.ranges, fmt.Sprintf("%d..%d", from, to))
	n.mu.Unlock()

	if to-from+1 > MaxLogRange {
		return nil, fmt.Errorf("block range is %d, and the cap is %d", to-from+1, MaxLogRange)
	}

	address := filter["address"].(string)
	topic0 := filter["topics"].([]any)[0].(string)

	out := []wireLog{}
	for _, l := range n.logs {
		block, err := parseQuantity(l.BlockNumber)
		if err != nil {
			return nil, err
		}
		if block < from || block > to || l.Address != address || l.Topics[0] != topic0 {
			continue
		}

		out = append(out, l)
	}

	return append(out, n.always...), nil
}

func (n *fakeNode) reply(w http.ResponseWriter, id uint64, result any, rpcErr *RPCError) {
	encoded, err := json.Marshal(result)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      uint64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *RPCError       `json:"error,omitempty"`
	}{JSONRPC: "2.0", ID: id, Result: encoded, Error: rpcErr})
}

func (n *fakeNode) seenRanges() []string {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]string(nil), n.ranges...)
}

// ---------------------------------------------------------------- log fixtures

func (n *fakeNode) addTransfer(block, index uint64, token Address, from, to Address, value int64) {
	n.logs = append(n.logs, wireLog{
		Address:     token.String(),
		Topics:      []string{topicTransfer.String(), addressTopic(from).String(), addressTopic(to).String()},
		Data:        hexBytes(word(big.NewInt(value))),
		BlockNumber: quantity(block),
		BlockHash:   n.blocks[block].Hash.String(),
		LogIndex:    quantity(index),
	})
}

func (n *fakeNode) addRoot(block, index uint64, distributor Address, kind Kind, id uint64,
	root Hash, totals, carryIn Amounts,
) {
	data := append([]byte{}, root[:]...)
	for _, group := range []Amounts{totals, carryIn} {
		for _, a := range group {
			data = append(data, word(a)...)
		}
	}

	n.logs = append(n.logs, wireLog{
		Address:     distributor.String(),
		Topics:      []string{topicRootPosted.String(), word32(uint64(kind)).String(), word32(id).String()},
		Data:        hexBytes(data),
		BlockNumber: quantity(block),
		BlockHash:   n.blocks[block].Hash.String(),
		LogIndex:    quantity(index),
	})
}

func (n *fakeNode) addExclusion(block, index uint64, distributor, account Address) {
	n.logs = append(n.logs, wireLog{
		Address:     distributor.String(),
		Topics:      []string{topicExcludedAppended.String(), addressTopic(account).String()},
		Data:        "0x",
		BlockNumber: quantity(block),
		BlockHash:   n.blocks[block].Hash.String(),
		LogIndex:    quantity(index),
	})
}
