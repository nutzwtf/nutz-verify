package chain

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

	// noBatch is set once this endpoint has refused a batch request, so that every
	// subsequent read asks one at a time rather than paying a refused request per batch.
	noBatch atomic.Bool

	pace *pacer
}

// newEndpoint checks the URL is one we can POST to before any run starts, rather than at the
// first request halfway through a sync.
//
// label is how this endpoint is named in errors: its position and host, never the URL. The
// path of a provider URL is routinely an API key, and an error message is the one place a
// Verifier's output reliably ends up pasted into an issue.
func newEndpoint(raw string, position int, client *http.Client, callsPerSecond float64) (*endpoint, error) {
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
		pace:   newPacer(callsPerSecond, 2*HeaderBatch),
	}, nil
}

// pacer is a token bucket over JSON-RPC calls: the client-side half of the endpoint's own
// limiter, so that the budget is spent rather than tripped.
//
// Tokens may go negative. A caller that finds the bucket empty is charged anyway and told
// how long to wait for its debt to refill, which queues concurrent callers in arrival
// order without a queue.
type pacer struct {
	mu       sync.Mutex
	rate     float64 // tokens per second
	capacity float64
	tokens   float64
	last     time.Time
}

func newPacer(rate, capacity float64) *pacer {
	return &pacer{rate: rate, capacity: capacity, tokens: capacity, last: time.Now()}
}

// wait charges n calls and blocks until they are covered.
func (p *pacer) wait(ctx context.Context, n int) error {
	p.mu.Lock()
	now := time.Now()
	p.tokens = min(p.capacity, p.tokens+now.Sub(p.last).Seconds()*p.rate)
	p.last = now
	p.tokens -= float64(n)
	debt := -p.tokens
	p.mu.Unlock()

	if debt <= 0 {
		return nil
	}

	return waitFor(ctx, time.Duration(debt/p.rate*float64(time.Second)))
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
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// Retry bounds for a provider that is up but asking us to slow down.
//
// A public endpoint rate-limits, and the Verifier exists for the stranger using one: chain
// 4663's answers HTTP 429 partway through a sync of a busy token. A 429 is not "could not
// check" — the provider is telling us exactly how to succeed — and turning one into
// INDETERMINATE would mean a Recompute failing for the reason it was most likely to fail.
//
// Five attempts over roughly four seconds. Long enough to ride out a token-bucket refill,
// short enough that a genuinely wedged endpoint still becomes INDETERMINATE inside a
// 30-minute Dispute window rather than hanging in it.
const (
	maxAttempts = 5
	retryBase   = 250 * time.Millisecond
	retryCap    = 8 * time.Second
)

// call makes one JSON-RPC request and returns the raw result, retrying while the endpoint is
// asking us to wait.
//
// The result comes back undecoded so the caller can tell JSON null from a zero value. A node
// answers null for a block it does not have — an unsupported finality tag, a number past its
// tip — and unmarshalling that into a header struct yields block zero, which is a confident
// wrong answer rather than the INDETERMINATE this package owes its caller.
func (e *endpoint) call(ctx context.Context, method string, params ...any) (json.RawMessage, error) {
	body, err := json.Marshal(e.request(method, params))
	if err != nil {
		return nil, fmt.Errorf("%s: encoding the request: %w", method, err)
	}

	raw, err := e.roundTrip(ctx, method, body, 1)
	if err != nil {
		return nil, err
	}

	var decoded rpcResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: decoding the response: %w", method, err)
	}

	return decoded.result(method)
}

// callBatch makes one HTTP request carrying one JSON-RPC request per params entry, and
// returns the raw results in the same order.
//
// Replies are matched by id, never by position: the specification lets a node answer a
// batch in any order, and a client that trusted position would stamp every transfer with
// its neighbour's timestamp. Every request must be answered — a missing reply is an
// endpoint that did not answer the question, not a block with no timestamp.
//
// An endpoint that does not batch answers with something other than an array, or refuses
// the request outright; that is a batchRefused, and the caller asks one at a time instead.
func (e *endpoint) callBatch(ctx context.Context, method string, params [][]any) ([]json.RawMessage, error) {
	requests := make([]rpcRequest, 0, len(params))
	for _, p := range params {
		requests = append(requests, e.request(method, p))
	}

	body, err := json.Marshal(requests)
	if err != nil {
		return nil, fmt.Errorf("%s batch: encoding the request: %w", method, err)
	}

	raw, err := e.roundTrip(ctx, method+" batch", body, len(requests))
	if err != nil {
		// Only a status the endpoint chose says anything about batches. A rate limit was
		// retried and still refused, which is not "no batches"; a cancelled context or a
		// dropped connection says nothing about them at all, and treating either as a
		// refusal would turn one blip into every later read paying a request per header.
		var status *httpFailure
		if errors.As(err, &status) {
			return nil, &batchRefused{err: err}
		}

		return nil, err
	}

	var replies []rpcResponse
	if err := json.Unmarshal(raw, &replies); err != nil {
		// Not an array: a node without batch support answers a single error object.
		var single rpcResponse
		if json.Unmarshal(raw, &single) == nil && single.Error != nil {
			return nil, &batchRefused{err: fmt.Errorf("%s batch: %w", method, single.Error)}
		}

		return nil, fmt.Errorf("%s batch: decoding the response: %w", method, err)
	}

	byID := make(map[uint64]rpcResponse, len(replies))
	for _, r := range replies {
		byID[r.ID] = r
	}

	out := make([]json.RawMessage, 0, len(requests))
	for _, req := range requests {
		reply, answered := byID[req.ID]
		if !answered {
			return nil, fmt.Errorf("%s batch: request %d of %d was not answered", method, req.ID, len(requests))
		}

		result, err := reply.result(method)
		if err != nil {
			return nil, err
		}

		out = append(out, result)
	}

	return out, nil
}

// batchRefused is an endpoint that does not take batch requests. Not an error about the
// blocks asked for: the same question, asked one block at a time, may well be answered.
type batchRefused struct{ err error }

func (b *batchRefused) Error() string { return b.err.Error() }
func (b *batchRefused) Unwrap() error { return b.err }

func (e *endpoint) request(method string, params []any) rpcRequest {
	if params == nil {
		params = []any{}
	}

	return rpcRequest{JSONRPC: "2.0", ID: e.nextID.Add(1), Method: method, Params: params}
}

// result is the reply's result, or why there is none.
func (r rpcResponse) result(method string) (json.RawMessage, error) {
	if r.Error != nil {
		return nil, fmt.Errorf("%s: %w", method, r.Error)
	}
	if isNull(r.Result) {
		return nil, fmt.Errorf("%s: the endpoint has no answer", method)
	}

	return r.Result, nil
}

// roundTrip posts body, which carries calls JSON-RPC requests, and returns the response
// body, pacing every attempt and retrying while the endpoint is asking us to wait.
func (e *endpoint) roundTrip(ctx context.Context, what string, body []byte, calls int) ([]byte, error) {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			if err := waitFor(ctx, backoff(attempt, lastErr)); err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
		}

		if err := e.pace.wait(ctx, calls); err != nil {
			return nil, fmt.Errorf("%s: %w", what, err)
		}

		raw, err := e.attempt(ctx, what, body)
		if err == nil {
			return raw, nil
		}

		lastErr = err

		var retry *retryable
		if !errors.As(err, &retry) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("%s: gave up after %d attempts: %w", what, maxAttempts, lastErr)
}

// httpFailure is an HTTP status that is neither success nor worth retrying: the endpoint
// answered, and the answer was no. Carried as a type so a batch refused this way can be
// told from a transport failure.
type httpFailure struct {
	status int
	err    error
}

func (h *httpFailure) Error() string { return h.err.Error() }
func (h *httpFailure) Unwrap() error { return h.err }

// retryable is an endpoint that is up and refusing for a reason that may pass: a rate limit,
// or a gateway that is briefly unwell. It is distinguished from every other failure because
// those do not improve by being asked again — a bad key and an unsupported method would just
// cost five times as much before failing identically.
type retryable struct {
	status int
	after  time.Duration // the endpoint's own Retry-After, when it sent one
	err    error
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

func (e *endpoint) attempt(ctx context.Context, what string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, bodyLimit)

	if resp.StatusCode != http.StatusOK {
		// A rate limit and a gateway error both arrive as HTML often enough that the status
		// line alone leaves a user guessing which endpoint to blame.
		snippet, _ := io.ReadAll(io.LimitReader(limited, errorBodyLimit))
		failure := fmt.Errorf("%s: http %s: %s", what, resp.Status, bytes.TrimSpace(snippet))

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			return nil, &retryable{status: resp.StatusCode, after: retryAfter(resp), err: failure}
		}

		return nil, &httpFailure{status: resp.StatusCode, err: failure}
	}

	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("%s: reading the response: %w", what, err)
	}

	return raw, nil
}

// retryAfter is the endpoint's own instruction, in the delta-seconds form providers actually
// send. An HTTP-date is ignored rather than parsed: nothing observed sends one, and guessing
// wrong about a clock skew would be worse than falling back to the backoff.
func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
	if err != nil || seconds < 0 {
		return 0
	}

	return min(time.Duration(seconds)*time.Second, retryCap)
}

// backoff is how long to wait before attempt n. The endpoint's own Retry-After wins; failing
// that, exponential with full jitter, which spreads the eight header workers out instead of
// having them all come back at the same instant and rebuild the queue they just drained.
func backoff(attempt int, last error) time.Duration {
	var retry *retryable
	if errors.As(last, &retry) && retry.after > 0 {
		return retry.after
	}

	window := min(retryBase<<(attempt-1), retryCap)

	return time.Duration(rand.Int64N(int64(window)) + int64(retryBase))
}

func waitFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
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
