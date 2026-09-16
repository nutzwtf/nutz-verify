package chain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"slices"
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

	// noBatch answers a batch request the way a node without batch support does: one
	// error object rather than an array. shuffleBatch answers a batch in reverse order,
	// which the JSON-RPC spec allows and which a client that trusts position gets wrong.
	noBatch      bool
	shuffleBatch bool

	// dropFromBatch leaves the last reply out of a batch's answer. batchHTTPStatus, when
	// non-zero, is the HTTP status a batch request gets instead of a reply.
	dropFromBatch   bool
	batchHTTPStatus int

	// refuseFirst is how many requests answer refuseWith before the node starts behaving,
	// which is how a rate limit that passes looks from the client side. retryAfter, when set,
	// is sent as the Retry-After header.
	refuseFirst int
	refuseWith  int
	retryAfter  string
	refused     int

	// resultCap is the real shape of a public endpoint's log limit: not a cap on how many
	// blocks a query covers, but on how many logs it may match. Zero means unlimited.
	resultCap int

	// rangeCap is the widest block range one eth_getLogs may cover. Zero is MaxLogRange,
	// the way engineering spec §4.1 describes the public endpoint; a test that wants the
	// measured behaviour — a million blocks accepted — raises it.
	rangeCap uint64

	// missing are block numbers the node answers null for, the way a node does for a block
	// it has pruned or never had.
	missing map[uint64]bool

	mu       sync.Mutex
	ranges   []string
	requests []string
	batches  []int // the size of every batch request seen
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
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	// A batch is a JSON array of requests, answered by an array of replies in any order.
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		n.handleBatch(w, body)

		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	n.mu.Lock()
	n.requests = append(n.requests, req.Method)
	n.mu.Unlock()

	if n.gate(w) {
		return
	}

	n.answer(req).write(w)
}

func (n *fakeNode) handleBatch(w http.ResponseWriter, body []byte) {
	var reqs []rpcRequest
	if err := json.Unmarshal(body, &reqs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	n.mu.Lock()
	n.batches = append(n.batches, len(reqs))
	for _, req := range reqs {
		n.requests = append(n.requests, req.Method)
	}
	n.mu.Unlock()

	if n.gate(w) {
		return
	}

	if n.batchHTTPStatus != 0 {
		http.Error(w, "batch requests are not accepted here", n.batchHTTPStatus)

		return
	}

	if n.noBatch {
		(&fakeReply{Error: &RPCError{Code: -32600, Message: "batch requests are not supported"}}).write(w)

		return
	}

	replies := make([]*fakeReply, 0, len(reqs))
	for _, req := range reqs {
		replies = append(replies, n.answer(req))
	}
	if n.shuffleBatch {
		slices.Reverse(replies)
	}
	if n.dropFromBatch {
		replies = replies[:len(replies)-1]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(replies)
}

// gate applies the whole-request failures — an HTTP status, or a refusal that passes — and
// reports whether it consumed the request.
func (n *fakeNode) gate(w http.ResponseWriter) bool {
	if n.httpStatus != 0 {
		http.Error(w, "upstream is having a moment", n.httpStatus)

		return true
	}

	n.mu.Lock()
	refusing := n.refused < n.refuseFirst
	if refusing {
		n.refused++
	}
	n.mu.Unlock()

	if refusing {
		if n.retryAfter != "" {
			w.Header().Set("Retry-After", n.retryAfter)
		}
		http.Error(w, "slow down", n.refuseWith)

		return true
	}

	return false
}

// answer is the reply to one request, staged failures included.
func (n *fakeNode) answer(req rpcRequest) *fakeReply {
	if result, staged := n.override[req.Method]; staged {
		return n.reply(req.ID, result, nil)
	}

	if message, failing := n.failWith[req.Method]; failing {
		return n.reply(req.ID, nil, &RPCError{Code: -32000, Message: message})
	}

	result, err := n.dispatch(req)
	if err != nil {
		return n.reply(req.ID, nil, &RPCError{Code: -32602, Message: err.Error()})
	}

	return n.reply(req.ID, result, nil)
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

	rangeCap := n.rangeCap
	if rangeCap == 0 {
		rangeCap = MaxLogRange
	}
	if to-from+1 > rangeCap {
		// Phrased the way providers with a range cap phrase it, so the Reader narrows.
		return nil, fmt.Errorf("block range is too wide: %d blocks, and the cap is %d", to-from+1, rangeCap)
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

	out = append(out, n.always...)

	if n.resultCap > 0 && len(out) > n.resultCap {
		// Phrased the way chain 4663's public endpoint phrases it, verbatim.
		return nil, fmt.Errorf("logs matched by query exceeds limit of %d", n.resultCap)
	}

	return out, nil
}

type fakeReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error,omitempty"`
}

func (n *fakeNode) reply(id uint64, result any, rpcErr *RPCError) *fakeReply {
	encoded, err := json.Marshal(result)
	if err != nil {
		panic(err) // a test staged something json cannot encode
	}

	return &fakeReply{JSONRPC: "2.0", ID: id, Result: encoded, Error: rpcErr}
}

func (r *fakeReply) write(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r)
}

func (n *fakeNode) seenRanges() []string {
	n.mu.Lock()
	defer n.mu.Unlock()

	return append([]string(nil), n.ranges...)
}

// ----------------------------------------------------------------- log builders

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
