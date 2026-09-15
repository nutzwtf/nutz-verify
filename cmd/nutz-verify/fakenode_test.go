package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/sha3"

	"github.com/nutzwtf/nutz-verify/internal/chain"
)

// fakeNode is a JSON-RPC server with a chain of blocks, the token's and the Distributor's
// logs, and the Distributor's ledger() book — enough to drive the whole binary without a
// node. It is the system boundary the CLI tests mock; nothing inside the binary is.
//
// It speaks the wire format directly rather than borrowing internal/chain's encoders, so a
// decoding bug there cannot be matched by an identical encoding bug here.
type fakeNode struct {
	chainID    uint64
	timestamps []int64 // index is the block number
	tips       map[string]uint64
	hashSalt   byte // lets a second node disagree about a block hash

	logs    []wireLog
	ledgers map[uint64]fakeLedger // by Epoch id

	// httpStatus, when non-zero, is answered to every request instead of a reply.
	httpStatus int

	mu       sync.Mutex
	requests int
}

type fakeLedger struct {
	root         chain.Hash
	rootPostedAt int64
	skipped      bool
	funded       chain.Amounts
	totals       chain.Amounts
}

type wireLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	BlockHash   string   `json:"blockHash"`
	LogIndex    string   `json:"logIndex"`
	TxHash      string   `json:"transactionHash"`
	TxIndex     string   `json:"transactionIndex"`
	Removed     bool     `json:"removed"`
}

func newFakeNode(timestamps []int64) *fakeNode {
	last := uint64(len(timestamps) - 1)

	return &fakeNode{
		chainID:    4663,
		timestamps: timestamps,
		tips:       map[string]uint64{"latest": last, "safe": last, "finalized": last},
		ledgers:    map[uint64]fakeLedger{},
	}
}

func (n *fakeNode) serve(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(n.handle))
	t.Cleanup(server.Close)

	return server.URL
}

func (n *fakeNode) hashOf(block uint64) chain.Hash {
	return keccak(append(big.NewInt(int64(block)).Bytes(), n.hashSalt))
}

type rpcRequest struct {
	ID     json.RawMessage   `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

type rpcReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (n *fakeNode) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	n.mu.Lock()
	n.requests++
	n.mu.Unlock()

	if n.httpStatus != 0 {
		http.Error(w, "upstream is having a moment", n.httpStatus)

		return
	}

	w.Header().Set("Content-Type", "application/json")

	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		var reqs []rpcRequest
		if err := json.Unmarshal(body, &reqs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		replies := make([]rpcReply, 0, len(reqs))
		for _, req := range reqs {
			replies = append(replies, n.answer(req))
		}
		_ = json.NewEncoder(w).Encode(replies)

		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	_ = json.NewEncoder(w).Encode(n.answer(req))
}

func (n *fakeNode) answer(req rpcRequest) rpcReply {
	result, err := n.dispatch(req)
	if err != nil {
		return rpcReply{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32000, Message: err.Error()}}
	}

	return rpcReply{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (n *fakeNode) dispatch(req rpcRequest) (any, error) {
	switch req.Method {
	case "eth_chainId":
		return quantity(n.chainID), nil
	case "eth_getBlockByNumber":
		var tag string
		if err := json.Unmarshal(req.Params[0], &tag); err != nil {
			return nil, err
		}

		return n.block(tag), nil
	case "eth_getLogs":
		var filter struct {
			FromBlock string   `json:"fromBlock"`
			ToBlock   string   `json:"toBlock"`
			Address   string   `json:"address"`
			Topics    []string `json:"topics"`
		}
		if err := json.Unmarshal(req.Params[0], &filter); err != nil {
			return nil, err
		}

		return n.getLogs(filter.FromBlock, filter.ToBlock, filter.Address, filter.Topics[0])
	case "eth_call":
		var call struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(req.Params[0], &call); err != nil {
			return nil, err
		}

		return n.call(call.Data)
	default:
		return nil, fmt.Errorf("unsupported method %s", req.Method)
	}
}

func (n *fakeNode) block(tag string) any {
	number, ok := n.tips[tag]
	if !ok {
		parsed, err := parseQuantity(tag)
		if err != nil || parsed >= uint64(len(n.timestamps)) {
			return nil
		}

		number = parsed
	}

	parent := chain.Hash{}
	if number > 0 {
		parent = n.hashOf(number - 1)
	}

	return map[string]any{
		"number":     quantity(number),
		"hash":       n.hashOf(number).String(),
		"parentHash": parent.String(),
		"timestamp":  quantity(uint64(n.timestamps[number])),
	}
}

func (n *fakeNode) getLogs(fromHex, toHex, address, topic0 string) (any, error) {
	from, err := parseQuantity(fromHex)
	if err != nil {
		return nil, err
	}
	to, err := parseQuantity(toHex)
	if err != nil {
		return nil, err
	}

	out := []wireLog{}
	for _, l := range n.logs {
		block, err := parseQuantity(l.BlockNumber)
		if err != nil {
			return nil, err
		}
		if block < from || block > to || !strings.EqualFold(l.Address, address) || l.Topics[0] != topic0 {
			continue
		}

		l.BlockHash = n.hashOf(block).String()
		out = append(out, l)
	}

	return out, nil
}

// call answers ledger(kind, id): the selector, a kind word and an id word in, eighteen
// static words out. Anything else is an address with no code, which returns nothing.
func (n *fakeNode) call(data string) (any, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(data, "0x"))
	if err != nil {
		return nil, err
	}

	if len(raw) != 4+64 || !bytes.Equal(raw[:4], selector("ledger(uint8,uint256)")) || raw[4+31] != 0 {
		return "0x", nil
	}

	id := new(big.Int).SetBytes(raw[4+32:]).Uint64()
	l := n.ledgers[id]

	var out []byte
	out = append(out, l.root[:]...)
	out = append(out, word(big.NewInt(l.rootPostedAt))...)
	skipped := big.NewInt(0)
	if l.skipped {
		skipped = big.NewInt(1)
	}
	out = append(out, word(skipped)...)
	for _, group := range []chain.Amounts{l.funded, l.totals, zeroAmounts()} {
		for _, a := range group {
			if a == nil {
				a = new(big.Int) // an Epoch the book never touched
			}
			out = append(out, word(a)...)
		}
	}

	return "0x" + hex.EncodeToString(out), nil
}

// ----------------------------------------------------------------- log builders

func (n *fakeNode) transfer(block uint64, token, from, to chain.Address, value int64) {
	n.logs = append(n.logs, wireLog{
		Address:     token.String(),
		Topics:      []string{topic("Transfer(address,address,uint256)"), addressTopic(from), addressTopic(to)},
		Data:        "0x" + hex.EncodeToString(word(big.NewInt(value))),
		BlockNumber: quantity(block),
		LogIndex:    quantity(uint64(len(n.logs))),
	})
}

func (n *fakeNode) exclusion(block uint64, distributor, account chain.Address) {
	n.logs = append(n.logs, wireLog{
		Address:     distributor.String(),
		Topics:      []string{topic("ExcludedAppended(address)"), addressTopic(account)},
		Data:        "0x",
		BlockNumber: quantity(block),
		LogIndex:    quantity(uint64(len(n.logs))),
	})
}

func (n *fakeNode) rootPosted(block uint64, distributor chain.Address, id uint64, root chain.Hash, totals, carryIn chain.Amounts) {
	data := append([]byte{}, root[:]...)
	for _, group := range []chain.Amounts{totals, carryIn} {
		for _, a := range group {
			data = append(data, word(a)...)
		}
	}

	n.logs = append(n.logs, wireLog{
		Address: distributor.String(),
		Topics: []string{
			topic("RootPosted(uint8,uint256,bytes32,uint256[5],uint256[5])"),
			"0x" + hex.EncodeToString(word(big.NewInt(0))),
			"0x" + hex.EncodeToString(word(new(big.Int).SetUint64(id))),
		},
		Data:        "0x" + hex.EncodeToString(data),
		BlockNumber: quantity(block),
		LogIndex:    quantity(uint64(len(n.logs))),
	})
}

// ----------------------------------------------------------------- encoding

func keccak(b []byte) chain.Hash {
	h := sha3.NewLegacyKeccak256()
	h.Write(b)

	var out chain.Hash
	copy(out[:], h.Sum(nil))

	return out
}

func topic(signature string) string {
	return keccak([]byte(signature)).String()
}

func selector(signature string) []byte {
	h := keccak([]byte(signature))

	return h[:4]
}

func word(x *big.Int) []byte {
	out := make([]byte, 32)
	x.FillBytes(out)

	return out
}

func addressTopic(a chain.Address) string {
	out := make([]byte, 32)
	copy(out[12:], a[:])

	return "0x" + hex.EncodeToString(out)
}

func quantity(n uint64) string {
	return fmt.Sprintf("0x%x", n)
}

func parseQuantity(s string) (uint64, error) {
	var n uint64
	if _, err := fmt.Sscanf(s, "0x%x", &n); err != nil {
		return 0, err
	}

	return n, nil
}

func amounts(vs ...int64) chain.Amounts {
	var out chain.Amounts
	for i, v := range vs {
		out[i] = big.NewInt(v)
	}

	return out
}
