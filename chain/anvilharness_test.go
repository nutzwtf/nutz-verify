package chain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The anvil harness: a real node, the real NutzDistributor, and a stand-in ERC-20 for NUTZ.
//
// ADR-0004 lets this repo hand-roll its ABI decoding on the argument that the surface is four
// shapes fixed at compile time. The debt that argument takes on is that we own the decoding
// bugs, and this file is how it is paid: every shape is decoded from output a real node
// produced, driven by a contract we did not write and encoded by foundry rather than by us.
//
// Nothing here validates the Verifier against itself. The chain is driven entirely through
// `cast` — which builds the calldata, the EIP-712 signatures and the events with an encoder
// that shares no code with chain — and the Go side only reads. An encoding mistake
// we made twice in the same direction would cancel out in a round trip; it cannot cancel out
// against foundry.
//
// The test skips, loudly and with the reason, when the tools or the contracts are not here.
// Two environment variables turn those skips into failures, and they are separate on purpose:
//
//   - NUTZ_VERIFY_REQUIRE_ANVIL — anvil, cast and a built nutz-contracts must be present. A
//     bare node satisfies it, so this needs no secrets and CI can demand it on every push.
//   - NUTZ_VERIFY_REQUIRE_FORK — the node must really be a fork of 4663, which needs an
//     archive endpoint. CI demands this nightly.
//
// One variable for both would mean the decoders could only be required to meet a node where
// the archive secret is available, which is nightly — and a decoder regression would then sit
// unnoticed for a day. A harness that quietly does not run is ADR-0004's bargain quietly not
// being kept: a green suite with none of the decoders ever having met a node.

// The well-known anvil development keys. Public constants of the tool, funded on every fresh
// node, and worthless anywhere else.
var anvilKeys = [5]string{
	"0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80",
	"0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d",
	"0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a",
	"0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6",
	"0x47e179ec197488593b187f80a00eb0da91f1b9d0b13f8733639f19c30a34926a",
}

// harnessChainID is the chain the Verifier is built for. A fork carries it; a bare anvil is
// told to claim it, so the two modes differ in the history behind block 0 and nothing else.
const harnessChainID = 4663

// anvil is a running node and the foundry commands that drive it.
type anvil struct {
	t         *testing.T
	rpc       string
	contracts string
	forked    bool

	// log is anvil's stderr, and exited closes when the process does. Together they turn
	// "the node never answered" — a bad archive URL, an upstream 401, a rate limit — from a
	// silent wait into the reason it did not.
	log    *lockedBuffer
	exited chan struct{}

	// accounts are the addresses of anvilKeys, in the same order.
	accounts [5]Address
}

// lockedBuffer collects a subprocess's output from its own goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return strings.TrimSpace(b.buf.String())
}

// startAnvil brings up a node, forking chain 4663 when an archive URL is available.
//
// The fork is what spec §10 asks for and is the more honest test: real chain id, real
// history behind block 0, and a real answer to whether safe and finalized are served there.
// A bare node is the degraded mode, and the test says which one it ran.
func startAnvil(t *testing.T) *anvil {
	t.Helper()

	contracts := contractsDir(t)
	requireTool(t, contracts, "anvil")
	requireTool(t, contracts, "cast")

	port, err := freePort()
	if err != nil {
		unavailable(t, "anvil harness: no free port: %v", err)
	}

	args := []string{"--port", fmt.Sprint(port), "--host", "127.0.0.1", "--silent"}

	forkURL := firstEnv("NUTZ_VERIFY_FORK_RPC", "RPC_4663")
	if forkURL == "" {
		// A bare node still exercises every decoder, which is what ADR-0004 needs, so this
		// degrades rather than stopping. What it cannot answer is anything about chain 4663
		// itself, so the subtests that ask about the chain skip themselves below.
		noFork(t)
		args = append(args, "--chain-id", fmt.Sprint(harnessChainID))
	} else {
		args = append(args, "--fork-url", forkURL)
	}

	log := &lockedBuffer{}

	cmd := exec.Command("anvil", args...)
	cmd.Dir = contracts
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		unavailable(t, "anvil harness: %v", err)
	}

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})

	a := &anvil{
		t:         t,
		rpc:       fmt.Sprintf("http://127.0.0.1:%d", port),
		contracts: contracts,
		forked:    forkURL != "",
		log:       log,
		exited:    exited,
	}
	a.await()

	for i, key := range anvilKeys {
		a.accounts[i] = a.address(a.cast("wallet", "address", "--private-key", key))
	}

	return a
}

// startupBudget is how long the node has to answer. A fork must reach an archive endpoint and
// pull a block before it binds, and a busy or rate-limited provider makes that minutes rather
// than seconds; a bare node is up almost immediately, so waiting that long for one only turns
// a mistake into a slow mistake.
func startupBudget(forked bool) time.Duration {
	if forked {
		return 3 * time.Minute
	}

	return 20 * time.Second
}

// await waits for the node to answer, and says why if it does not.
//
// Two ways to fail, and they need different messages: anvil exiting — a bad archive URL, an
// upstream 401 — leaves its reason on stderr, while anvil still running at the deadline is a
// slow or throttled upstream. Reporting either as "never answered" makes a CI failure
// something to reproduce locally rather than something to read.
func (a *anvil) await() {
	a.t.Helper()

	// Short per-probe timeout: the budget is for the node coming up, not for one connection
	// hanging its way through all of it.
	probe := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(startupBudget(a.forked))

	for time.Now().Before(deadline) {
		select {
		case <-a.exited:
			unavailable(a.t, "anvil harness: anvil exited before it answered: %s", a.log)
		default:
		}

		resp, err := probe.Post(a.rpc, "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`))
		if err == nil {
			resp.Body.Close()

			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	unavailable(a.t, "anvil harness: the node at %s did not answer within %s: %s",
		a.rpc, startupBudget(a.forked), a.log)
}

// cast runs one foundry command and returns its trimmed output.
//
// The working directory is the contracts repo: that is where the artifacts are, and it is
// also what lets a version-managed foundry shim resolve at all.
func (a *anvil) cast(args ...string) string {
	a.t.Helper()

	cmd := exec.CommandContext(a.t.Context(), "cast", args...)
	cmd.Dir = a.contracts

	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exit.Stderr))
		}

		// Both streams: foundry puts a revert reason on one or the other depending on the
		// subcommand, and a harness failure with neither is unactionable.
		a.t.Fatalf("cast %s: %v\nstderr: %s\nstdout: %s",
			strings.Join(args, " "), err, stderr, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out))
}

// rpcArgs are the flags every chain-touching cast command needs.
func (a *anvil) rpcArgs(key string) []string {
	return []string{"--rpc-url", a.rpc, "--private-key", key}
}

// send broadcasts one transaction from account 0 and returns the receipt.
func (a *anvil) send(to Address, signature string, args ...string) castReceipt {
	a.t.Helper()

	return a.sendFrom(anvilKeys[0], to, signature, args...)
}

// sendFrom broadcasts from a particular account, which is how a NUTZ holder moves its own
// balance rather than having it minted.
func (a *anvil) sendFrom(key string, to Address, signature string, args ...string) castReceipt {
	a.t.Helper()

	full := append([]string{"send"}, a.rpcArgs(key)...)
	full = append(full, "--json", to.String(), signature)
	full = append(full, args...)

	return a.receipt(a.cast(full...))
}

// deployReceipt puts raw bytecode on chain. The receipt carries the block as well as the
// address, and the block is what a log query has to start from.
func (a *anvil) deployReceipt(bytecode, signature string, args ...string) castReceipt {
	a.t.Helper()

	full := append([]string{"send"}, a.rpcArgs(anvilKeys[0])...)
	full = append(full, "--json", "--create", bytecode, signature)
	full = append(full, args...)

	receipt := a.receipt(a.cast(full...))
	if receipt.ContractAddress == "" {
		a.t.Fatalf("deploy produced no contract address: %+v", receipt)
	}

	return receipt
}

// deploy is deployReceipt for the contracts whose block nobody needs.
func (a *anvil) deploy(bytecode, signature string, args ...string) Address {
	a.t.Helper()

	return a.address(a.deployReceipt(bytecode, signature, args...).ContractAddress)
}

// castReceipt is the part of `cast send --json` output the harness uses.
type castReceipt struct {
	Status          string `json:"status"`
	BlockNumber     string `json:"blockNumber"`
	ContractAddress string `json:"contractAddress"`
}

func (a *anvil) receipt(raw string) castReceipt {
	a.t.Helper()

	var out castReceipt
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		a.t.Fatalf("decoding a cast receipt: %v: %s", err, raw)
	}
	if out.Status != "0x1" {
		a.t.Fatalf("transaction reverted: %s", raw)
	}

	return out
}

func (a *anvil) blockOf(receipt castReceipt) uint64 {
	a.t.Helper()

	number, err := parseQuantity(receipt.BlockNumber)
	if err != nil {
		a.t.Fatalf("receipt block number: %v", err)
	}

	return number
}

// warp fixes the timestamp of the next block, which is what makes the Epoch boundaries in
// this harness exact rather than approximate.
func (a *anvil) warp(timestamp int64) {
	a.t.Helper()

	a.cast("rpc", "--rpc-url", a.rpc, "evm_setNextBlockTimestamp", fmt.Sprint(timestamp))
}

func (a *anvil) mine() {
	a.t.Helper()

	a.cast("rpc", "--rpc-url", a.rpc, "evm_mine")
}

// latestTimestamp is the head block's timestamp, used to start the timeline after whatever
// the fork left us at.
func (a *anvil) latestTimestamp() int64 {
	a.t.Helper()

	raw := a.cast("block", "latest", "--rpc-url", a.rpc, "--field", "timestamp")

	var seconds int64
	if _, err := fmt.Sscan(raw, &seconds); err != nil {
		a.t.Fatalf("head timestamp %q: %v", raw, err)
	}

	return seconds
}

// signTyped signs EIP-712 typed data with one Signer's key.
//
// The Distributor verifies 2-of-3 over _hashTypedDataV4(structHash), so this is the only way
// to reach postRoot and executeExclusion at all — and it means the RootPosted log this
// harness decodes was produced by the contract's own governance path rather than injected.
func (a *anvil) signTyped(key, typedData string) string {
	a.t.Helper()

	path := filepath.Join(a.t.TempDir(), fmt.Sprintf("typed-%d.json", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(typedData), 0o600); err != nil {
		a.t.Fatalf("writing typed data: %v", err)
	}

	return a.cast("wallet", "sign", "--private-key", key, "--data", "--from-file", path)
}

func (a *anvil) address(s string) Address {
	a.t.Helper()

	parsed, err := ParseAddress(strings.TrimSpace(s))
	if err != nil {
		a.t.Fatalf("address %q: %v", s, err)
	}

	return parsed
}

// bytecode reads a compiled contract's creation code out of the foundry artifact.
func (a *anvil) bytecode(contract string) string {
	a.t.Helper()

	path := filepath.Join(a.contracts, "out", contract+".sol", contract+".json")

	raw, err := os.ReadFile(path)
	if err != nil {
		unavailable(a.t, "anvil harness: %v; run `forge build` in %s", err, a.contracts)
	}

	var artifact struct {
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
	}
	if err := json.Unmarshal(raw, &artifact); err != nil {
		a.t.Fatalf("decoding %s: %v", path, err)
	}
	if artifact.Bytecode.Object == "" {
		a.t.Fatalf("%s carries no creation bytecode", path)
	}

	return artifact.Bytecode.Object
}

// ------------------------------------------------------------------ preconditions

// contractsDir is the sibling nutz-contracts checkout. The harness needs its compiled
// artifacts: the whole point is to decode what the real Distributor emits.
func contractsDir(t *testing.T) string {
	t.Helper()

	dir := os.Getenv("NUTZ_CONTRACTS")
	if dir == "" {
		dir = filepath.Join("..", "..", "..", "nutz-contracts")
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		unavailable(t, "anvil harness: %v", err)
	}
	if _, err := os.Stat(filepath.Join(absolute, "foundry.toml")); err != nil {
		unavailable(t, "anvil harness: no nutz-contracts checkout at %s; set NUTZ_CONTRACTS", absolute)
	}

	return absolute
}

func requireTool(t *testing.T, dir, name string) {
	t.Helper()

	cmd := exec.Command(name, "--version")
	cmd.Dir = dir

	if err := cmd.Run(); err != nil {
		unavailable(t, "anvil harness: %s is not runnable from %s: %v", name, dir, err)
	}
}

// unavailable ends the harness: a skip normally, a failure under NUTZ_VERIFY_REQUIRE_ANVIL.
//
// ADR-0004 trades "we own the decoding bugs" for "the anvil harness exercises every decoder
// against a real node". A machine without foundry should still be able to run `go test ./...`,
// so the default is a skip — but somewhere that trade has to be enforced, and a skip nobody
// reads does not enforce it.
func unavailable(t *testing.T, format string, args ...any) {
	t.Helper()

	if os.Getenv("NUTZ_VERIFY_REQUIRE_ANVIL") != "" {
		t.Fatalf(format, args...)
	}

	t.Skipf(format, args...)
}

// noFork reports the degradation to a bare node — fatally under NUTZ_VERIFY_REQUIRE_FORK,
// where the fork is the point, and as a log otherwise. The run continues either way: every
// decoder is still exercised against a node, which is the part ADR-0004 bought.
func noFork(t *testing.T) {
	t.Helper()

	const message = "anvil harness: no NUTZ_VERIFY_FORK_RPC or RPC_4663, so this is a bare node " +
		"and not the fork of 4663 spec §10 asks for"

	if os.Getenv("NUTZ_VERIFY_REQUIRE_FORK") != "" {
		t.Fatal(message)
	}

	t.Log(message)
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}

	return ""
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port, nil
}
