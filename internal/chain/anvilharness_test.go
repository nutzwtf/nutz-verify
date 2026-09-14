package chain

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// that shares no code with internal/chain — and the Go side only reads. An encoding mistake
// we made twice in the same direction would cancel out in a round trip; it cannot cancel out
// against foundry.
//
// The test skips, loudly and with the reason, when the tools or the contracts are not here —
// unless NUTZ_VERIFY_REQUIRE_ANVIL is set, which turns every one of those skips into a
// failure. CI sets it. A harness that quietly does not run is ADR-0004's bargain quietly not
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

	// accounts are the addresses of anvilKeys, in the same order.
	accounts [5]Address
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
		// itself, so the subtests that ask about the chain skip themselves. Required mode
		// insists on the real thing.
		requireFork(t)
		args = append(args, "--chain-id", fmt.Sprint(harnessChainID))
	} else {
		args = append(args, "--fork-url", forkURL)
	}

	cmd := exec.Command("anvil", args...)
	cmd.Dir = contracts
	if err := cmd.Start(); err != nil {
		unavailable(t, "anvil harness: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	a := &anvil{
		t:         t,
		rpc:       fmt.Sprintf("http://127.0.0.1:%d", port),
		contracts: contracts,
		forked:    forkURL != "",
	}
	a.await()

	for i, key := range anvilKeys {
		a.accounts[i] = a.address(a.cast("wallet", "address", "--private-key", key))
	}

	return a
}

// await waits for the node to answer. A fork has an upstream to reach first, so this is
// generous; a node that never comes up skips rather than failing, because a missing archive
// endpoint is a machine that cannot run this test, not a broken Verifier.
func (a *anvil) await() {
	a.t.Helper()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Post(a.rpc, "application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`))
		if err == nil {
			resp.Body.Close()

			return
		}

		time.Sleep(200 * time.Millisecond)
	}

	unavailable(a.t, "anvil harness: the node at %s never answered", a.rpc)
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

// unavailable ends the harness: a skip normally, a failure when NUTZ_VERIFY_REQUIRE_ANVIL is
// set.
//
// ADR-0004 trades "we own the decoding bugs" for "the anvil harness exercises every decoder
// against a real node before launch". A machine without foundry should still be able to run
// `go test ./...`, so the default is a skip — but somewhere that trade has to be enforced, and
// a skip nobody reads does not enforce it. CI sets the variable and a missing tool, a missing
// contracts checkout or a missing fork URL all fail there.
func unavailable(t *testing.T, format string, args ...any) {
	t.Helper()

	if os.Getenv("NUTZ_VERIFY_REQUIRE_ANVIL") != "" {
		t.Fatalf(format, args...)
	}

	t.Skipf(format, args...)
}

// requireFork reports the degradation to a bare node — fatally in required mode, where "the
// fork of 4663" is the point, and as a log otherwise.
func requireFork(t *testing.T) {
	t.Helper()

	const message = "anvil harness: no NUTZ_VERIFY_FORK_RPC or RPC_4663, so this is a bare node " +
		"and not the fork of 4663 spec §10 asks for"

	if os.Getenv("NUTZ_VERIFY_REQUIRE_ANVIL") != "" {
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
