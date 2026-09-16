// Package fakenode is a JSON-RPC server with a chain of blocks, the token's and the
// Distributor's logs, and the Distributor's ledger() book — enough to drive the Engine and
// the whole binary without a node. It is the system boundary the tests mock; nothing
// inside the module is.
//
// It speaks the wire format directly rather than borrowing chain's encoders, so a decoding
// bug there cannot be matched by an identical encoding bug here.
package fakenode

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

	"github.com/nutzwtf/nutz-verify/chain"
)

// Node is one fake endpoint. Fields are set before Serve; the server does not lock them.
type Node struct {
	ChainID    uint64
	Timestamps []int64 // index is the block number
	Tips       map[string]uint64
	HashSalt   byte // lets a second node disagree about a block hash

	logs    []wireLog
	Ledgers map[uint64]Ledger // by Epoch id

	// HTTPStatus, when non-zero, is answered to every request instead of a reply.
	HTTPStatus int

	mu       sync.Mutex
	requests int
}

// Ledger is what ledger(kind, id) answers for one Epoch.
type Ledger struct {
	Root         chain.Hash
	RootPostedAt int64
	Skipped      bool
	Funded       chain.Amounts
	Totals       chain.Amounts
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

// New is a chain 4663 node whose block i has timestamps[i], with every tip at the last block.
func New(timestamps []int64) *Node {
	last := uint64(len(timestamps) - 1)

	return &Node{
		ChainID:    4663,
		Timestamps: timestamps,
		Tips:       map[string]uint64{"latest": last, "safe": last, "finalized": last},
		Ledgers:    map[uint64]Ledger{},
	}
}

// Serve starts the node for the test's lifetime and returns its URL.
func (n *Node) Serve(t *testing.T) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(n.handle))
	t.Cleanup(server.Close)

	return server.URL
}

// HashOf is the node's hash for a block.
func (n *Node) HashOf(block uint64) chain.Hash {
	return keccak(append(big.NewInt(int64(block)).Bytes(), n.HashSalt))
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

func (n *Node) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	n.mu.Lock()
	n.requests++
	n.mu.Unlock()

	if n.HTTPStatus != 0 {
		http.Error(w, "upstream is having a moment", n.HTTPStatus)

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

func (n *Node) answer(req rpcRequest) rpcReply {
	result, err := n.dispatch(req)
	if err != nil {
		return rpcReply{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32000, Message: err.Error()}}
	}

	return rpcReply{JSONRPC: "2.0", ID: req.ID, Result: result}
}

func (n *Node) dispatch(req rpcRequest) (any, error) {
	switch req.Method {
	case "eth_chainId":
		return quantity(n.ChainID), nil
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

func (n *Node) block(tag string) any {
	number, ok := n.Tips[tag]
	if !ok {
		parsed, err := parseQuantity(tag)
		if err != nil || parsed >= uint64(len(n.Timestamps)) {
			return nil
		}

		number = parsed
	}

	parent := chain.Hash{}
	if number > 0 {
		parent = n.HashOf(number - 1)
	}

	return map[string]any{
		"number":     quantity(number),
		"hash":       n.HashOf(number).String(),
		"parentHash": parent.String(),
		"timestamp":  quantity(uint64(n.Timestamps[number])),
	}
}

func (n *Node) getLogs(fromHex, toHex, address, topic0 string) (any, error) {
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

		l.BlockHash = n.HashOf(block).String()
		out = append(out, l)
	}

	return out, nil
}

// call answers ledger(kind, id): the selector, a kind word and an id word in, eighteen
// static words out. Anything else is an address with no code, which returns nothing.
func (n *Node) call(data string) (any, error) {
	raw, err := hex.DecodeString(strings.TrimPrefix(data, "0x"))
	if err != nil {
		return nil, err
	}

	if len(raw) != 4+64 || !bytes.Equal(raw[:4], selector("ledger(uint8,uint256)")) || raw[4+31] != 0 {
		return "0x", nil
	}

	id := new(big.Int).SetBytes(raw[4+32:]).Uint64()
	l := n.Ledgers[id]

	var out []byte
	out = append(out, l.Root[:]...)
	out = append(out, word(big.NewInt(l.RootPostedAt))...)
	skipped := big.NewInt(0)
	if l.Skipped {
		skipped = big.NewInt(1)
	}
	out = append(out, word(skipped)...)
	for _, group := range []chain.Amounts{l.Funded, l.Totals, chain.Amounts{}} {
		for _, a := range group {
			if a == nil {
				a = new(big.Int) // an Epoch the book never touched
			}
			out = append(out, word(a)...)
		}
	}

	return "0x" + hex.EncodeToString(out), nil
}

// DropLastLog forgets the most recently appended log, so a test can re-post a Root.
func (n *Node) DropLastLog() {
	n.logs = n.logs[:len(n.logs)-1]
}

// Requests is how many HTTP requests the node has answered.
func (n *Node) Requests() int {
	n.mu.Lock()
	defer n.mu.Unlock()

	return n.requests
}

// ----------------------------------------------------------------- log builders

// Transfer appends a NUTZ Transfer log at block.
func (n *Node) Transfer(block uint64, token, from, to chain.Address, value int64) {
	n.logs = append(n.logs, wireLog{
		Address:     token.String(),
		Topics:      []string{topic("Transfer(address,address,uint256)"), addressTopic(from), addressTopic(to)},
		Data:        "0x" + hex.EncodeToString(word(big.NewInt(value))),
		BlockNumber: quantity(block),
		LogIndex:    quantity(uint64(len(n.logs))),
	})
}

// Exclusion appends an ExcludedAppended log at block.
func (n *Node) Exclusion(block uint64, distributor, account chain.Address) {
	n.logs = append(n.logs, wireLog{
		Address:     distributor.String(),
		Topics:      []string{topic("ExcludedAppended(address)"), addressTopic(account)},
		Data:        "0x",
		BlockNumber: quantity(block),
		LogIndex:    quantity(uint64(len(n.logs))),
	})
}

// RootPosted appends an Epoch RootPosted log at block.
func (n *Node) RootPosted(block uint64, distributor chain.Address, id uint64, root chain.Hash, totals, carryIn chain.Amounts) {
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

// Amounts is a five-token vector from up to five small values; the rest are zero.
func Amounts(vs ...int64) chain.Amounts {
	var out chain.Amounts
	for i := range out {
		out[i] = new(big.Int)
	}
	for i, v := range vs {
		out[i] = big.NewInt(v)
	}

	return out
}
