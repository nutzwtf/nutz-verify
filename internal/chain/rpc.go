package chain

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sync/atomic"
)

// bodyLimit caps how much of a response we will read. A verifier that streams an unbounded
// body from an endpoint it already does not trust (ADR-0002) has handed that endpoint a way
// to end the run. 64 MiB is far above the largest eth_getLogs page a 2,000-block range
// produces and far below anything that hurts.
const bodyLimit = 64 << 20

// errorBodyLimit is how much of a non-200 body is quoted back in the error. Enough to carry
// a provider's "rate limited" or "block range too wide"; not enough to paste an HTML page
// into a terminal.
const errorBodyLimit = 512

// endpoint is one RPC URL, and the unit of disagreement.
type endpoint struct {
	url    string
	label  string
	client *http.Client
	nextID atomic.Uint64
}

// newEndpoint checks the URL is one we can POST to before any run starts, rather than at the
// first request halfway through a sync.
//
// label is how this endpoint is named in errors: its position and host, never the URL. The
// path of a provider URL is routinely an API key, and an error message is the one place a
// Verifier's output reliably ends up pasted into an issue.
func newEndpoint(raw string, position int, client *http.Client) (*endpoint, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("chain: endpoint %d: %w", position, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("chain: endpoint %d: scheme is %q, and JSON-RPC here is http or https",
			position, parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("chain: endpoint %d: %q has no host", position, raw)
	}

	return &endpoint{
		url:    raw,
		label:  fmt.Sprintf("endpoint %d (%s)", position, parsed.Host),
		client: client,
	}, nil
}

// RPCError is a JSON-RPC error object. It is carried as a type rather than flattened to a
// string because the codes are how a caller tells a rate limit from a bad request.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// call makes one JSON-RPC request and returns the raw result.
//
// The result comes back undecoded so the caller can tell JSON null from a zero value. A node
// answers null for a block it does not have — an unsupported finality tag, a number past its
// tip — and unmarshalling that into a header struct yields block zero, which is a confident
// wrong answer rather than the INDETERMINATE this package owes its caller.
func (e *endpoint) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	if params == nil {
		params = []any{}
	}

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: e.nextID.Add(1), Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the request: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, bodyLimit)

	if resp.StatusCode != http.StatusOK {
		// A rate limit and a gateway error both arrive as HTML often enough that the status
		// line alone leaves a user guessing which endpoint to blame.
		snippet, _ := io.ReadAll(io.LimitReader(limited, errorBodyLimit))

		return nil, fmt.Errorf("%s: http %s: %s", method, resp.Status, bytes.TrimSpace(snippet))
	}

	var decoded rpcResponse
	if err := json.NewDecoder(limited).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%s: decoding the response: %w", method, err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("%s: %w", method, decoded.Error)
	}
	if isNull(decoded.Result) {
		return nil, fmt.Errorf("%s: the endpoint has no answer", method)
	}

	return decoded.Result, nil
}

func isNull(raw json.RawMessage) bool {
	return len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null"
}

// ------------------------------------------------------------------ wire shapes

// wireBlock is the part of a block header we read. Unknown fields are ignored on purpose:
// headers differ between chains and clients, and this is the one place where tolerating what
// we do not use costs nothing.
type wireBlock struct {
	Number     string `json:"number"`
	Hash       string `json:"hash"`
	ParentHash string `json:"parentHash"`
	Timestamp  string `json:"timestamp"`
}

func (w wireBlock) decode() (Block, error) {
	var b Block
	var err error

	if b.Number, err = parseQuantity(w.Number); err != nil {
		return b, fmt.Errorf("block number %w", err)
	}
	if b.Hash, err = parseHash(w.Hash); err != nil {
		return b, fmt.Errorf("block %d: hash %w", b.Number, err)
	}
	if b.ParentHash, err = parseHash(w.ParentHash); err != nil {
		return b, fmt.Errorf("block %d: parentHash %w", b.Number, err)
	}

	seconds, err := parseQuantity(w.Timestamp)
	if err != nil {
		return b, fmt.Errorf("block %d: timestamp %w", b.Number, err)
	}
	if seconds > math.MaxInt64 {
		return b, fmt.Errorf("block %d: timestamp %d is not a unix second", b.Number, seconds)
	}
	b.Timestamp = int64(seconds)

	return b, nil
}

// canonical is the form two endpoints' answers are compared in.
func (b Block) canonical() []byte {
	out := make([]byte, 0, 8+2*wordSize+8)
	out = appendUint64(out, b.Number)
	out = append(out, b.Hash[:]...)
	out = append(out, b.ParentHash[:]...)

	return appendUint64(out, uint64(b.Timestamp))
}

// wireLog is one eth_getLogs entry.
type wireLog struct {
	Address     string   `json:"address"`
	Topics      []string `json:"topics"`
	Data        string   `json:"data"`
	BlockNumber string   `json:"blockNumber"`
	BlockHash   string   `json:"blockHash"`
	LogIndex    string   `json:"logIndex"`
	Removed     bool     `json:"removed"`
}

func (w wireLog) decode() (eventLog, error) {
	var l eventLog
	var err error

	// A removed log is one a reorg took back. We only ever ask for ranges at or below a
	// finality level, so seeing one means the range was not as settled as we asked for.
	if w.Removed {
		return l, fmt.Errorf("log at block %s index %s is marked removed", w.BlockNumber, w.LogIndex)
	}

	if l.BlockNumber, err = parseQuantity(w.BlockNumber); err != nil {
		return l, fmt.Errorf("log blockNumber %w", err)
	}
	if l.LogIndex, err = parseQuantity(w.LogIndex); err != nil {
		return l, fmt.Errorf("log at block %d: logIndex %w", l.BlockNumber, err)
	}
	if l.BlockHash, err = parseHash(w.BlockHash); err != nil {
		return l, fmt.Errorf("log at block %d index %d: blockHash %w", l.BlockNumber, l.LogIndex, err)
	}
	if l.Address, err = ParseAddress(w.Address); err != nil {
		return l, fmt.Errorf("log at block %d index %d: address %w", l.BlockNumber, l.LogIndex, err)
	}
	if l.Data, err = parseHexBytes(w.Data); err != nil {
		return l, fmt.Errorf("log at block %d index %d: data %w", l.BlockNumber, l.LogIndex, err)
	}

	l.Topics = make([]Hash, 0, len(w.Topics))
	for i, raw := range w.Topics {
		topic, err := parseHash(raw)
		if err != nil {
			return l, fmt.Errorf("log at block %d index %d: topic %d %w", l.BlockNumber, l.LogIndex, i, err)
		}

		l.Topics = append(l.Topics, topic)
	}

	return l, nil
}

// canonicalLogs is the form two endpoints' log pages are compared in.
//
// The comparison is over the undecoded logs rather than the decoded events, so two endpoints
// that disagree about a log we would have rejected anyway still disagree. Ordering is not
// part of it — the logs are sorted before this is called — because the rules replay by
// timestamp and two providers returning the same set in a different order have not
// contradicted each other.
func canonicalLogs(logs []eventLog) []byte {
	out := make([]byte, 0, len(logs)*128)
	for _, l := range logs {
		out = appendUint64(out, l.BlockNumber)
		out = appendUint64(out, l.LogIndex)
		out = append(out, l.BlockHash[:]...)
		out = append(out, l.Address[:]...)
		out = appendUint64(out, uint64(len(l.Topics)))
		for _, t := range l.Topics {
			out = append(out, t[:]...)
		}
		out = appendUint64(out, uint64(len(l.Data)))
		out = append(out, l.Data...)
	}

	return out
}

func appendUint64(dst []byte, n uint64) []byte {
	var word [8]byte
	for i := 7; i >= 0; i-- {
		word[i] = byte(n)
		n >>= 8
	}

	return append(dst, word[:]...)
}

// sortLogs puts a page in (block, index) order, which is the order a node walks them in and
// the order the Cache appends them in.
func sortLogs(logs []eventLog) {
	slices.SortFunc(logs, func(a, b eventLog) int {
		if a.BlockNumber != b.BlockNumber {
			return cmp.Compare(a.BlockNumber, b.BlockNumber)
		}

		return cmp.Compare(a.LogIndex, b.LogIndex)
	})
}
