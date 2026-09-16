package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/epoch"
	"github.com/nutzwtf/nutz-verify/internal/fakenode"
)

func repeatAddr(b byte) chain.Address {
	var a chain.Address
	for i := range a {
		a[i] = b
	}

	return a
}

var (
	token       = repeatAddr(0x11)
	distributor = repeatAddr(0x22)
	devWallet   = repeatAddr(0xdd)
	treasury    = repeatAddr(0xcc) // Excluded from the deploy block
	alice       = repeatAddr(0xa1)
	bob         = repeatAddr(0xb2)
)

// caseRoot is testdata/cases/funding-plus-carry.json's expected root, produced by
// OpenZeppelin's own library over the two allocations that Case pins (ADR-0003, ADR-0005).
// The scenario below puts that Case on a chain, so the Root the Distributor posts is a
// known-good literal rather than whatever the code under test computes.
var caseRoot = mustHash("0xfab8800732f22a8e19fc6855ff24c1b1a77e561791a86b9efbf0262b44e7b7b0")

func mustHash(s string) chain.Hash {
	var h chain.Hash
	raw := strings.TrimPrefix(s, "0x")
	for i := range h {
		var b byte
		for _, c := range raw[2*i : 2*i+2] {
			b = b<<4 | byte(strings.IndexByte("0123456789abcdef", byte(c)))
		}
		h[i] = b
	}

	return h
}

// testDeployment is the Deployment the fake chain is pinned to: the token created at block
// 1, the Distributor deployed at block 5990, in Epoch 998.
var testDeployment = epoch.Deployment{
	ChainID:          4663,
	Token:            token,
	Distributor:      distributor,
	DevWallet:        devWallet,
	TokenBlock:       1,
	DistributorBlock: 5990,
}

// scenario is the funding-plus-carry Case on a chain. Blocks are ten minutes apart, so
// Epoch e is blocks 6e..6e+5; block 5999 is moved to the last second of Epoch 999 so the
// buys land as the Case has them. Epoch 999 is funded and has no eligible Holder — the
// buys at its last second give a TWAB of zero — so its funding rolls into Carry, and the
// Root for Epoch 1000, posted in Epoch 1001, spends funded [1000 0 500 1 0] plus that
// carryIn [500 0 0 0 7] over Alice (3 NUTZ) and Bob (1 NUTZ). Totals [1500 0 500 0 6].
//
// Tips: latest has Epoch 1001 closed, safe has the Root's block, finalized has not
// reached the end of Epoch 1000.
func scenario() *fakenode.Node {
	timestamps := make([]int64, 6020)
	for i := range timestamps {
		timestamps[i] = int64(i) * 600
	}
	timestamps[5999] = 3599999

	n := fakenode.New(timestamps)
	n.Tips["safe"] = 6012
	n.Tips["finalized"] = 6004

	n.Exclusion(5990, distributor, treasury)
	n.Transfer(1, token, chain.Address{}, treasury, 1000000)
	n.Transfer(5999, token, treasury, alice, 3)
	n.Transfer(5999, token, treasury, bob, 1)
	n.RootPosted(6008, distributor, 1000, caseRoot, fakenode.Amounts(1500, 0, 500, 0, 6), fakenode.Amounts(500, 0, 0, 0, 7))

	n.Ledgers[999] = fakenode.Ledger{Skipped: true, Funded: fakenode.Amounts(500, 0, 0, 0, 7), Totals: fakenode.Amounts()}
	n.Ledgers[1000] = fakenode.Ledger{
		Root:         caseRoot,
		RootPostedAt: timestamps[6008],
		Funded:       fakenode.Amounts(1000, 0, 500, 1, 0),
		Totals:       fakenode.Amounts(1500, 0, 500, 0, 6),
	}

	return n
}

// cli runs the binary's entry point against the given nodes with a throwaway Cache, and
// returns the exit code and both streams.
func cli(t *testing.T, dep epoch.Deployment, nodes []*fakenode.Node, args ...string) (int, string, string) {
	t.Helper()

	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	for _, n := range nodes {
		args = append(args, "--rpc", n.Serve(t))
	}
	args = append(args, "--rate", "1000000") // a fake node has no budget to respect

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr, dep)

	t.Logf("exit %d\n--- stdout\n%s--- stderr\n%s", code, stdout.String(), stderr.String())

	return code, stdout.String(), stderr.String()
}

func TestEpoch_MatchExitsZeroOnFourLines(t *testing.T) {
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	for _, want := range []string{
		"\n  root     ok    " + caseRoot.String(),
		"\n  totals   ok    [1500 0 500 0 6]",
		"\n  cap      ok    ",
		"\n  carryIn  ok    [500 0 0 0 7] = ",
		"\nMATCH\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q", want)
		}
	}
}

func TestEpoch_MismatchNamesTheBrokenLine(t *testing.T) {
	// The indexer posted a Root over different numbers: the same tree shape, one unit more
	// for Bob in token 4, which the funding plus Carry still covers. Root and totals break;
	// cap and carryIn hold, and the output says so line by line.
	node := scenario()
	wrong := chain.Hash{0xbb}
	node.DropLastLog()
	node.RootPosted(6008, distributor, 1000, wrong, fakenode.Amounts(1500, 0, 500, 0, 7), fakenode.Amounts(500, 0, 0, 0, 7))
	l := node.Ledgers[1000]
	l.Root, l.Totals = wrong, fakenode.Amounts(1500, 0, 500, 0, 7)
	node.Ledgers[1000] = l

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1000")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	for _, want := range []string{
		"\n  root     FAIL  recomputed " + caseRoot.String() + ", posted " + wrong.String(),
		"\n  totals   FAIL  token 4: recomputed 6, posted 7",
		"\n  cap      ok    ",
		"\n  carryIn  ok    ",
		"\nMISMATCH\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q", want)
		}
	}
}

func TestEpoch_BelowTheRequestedFinalityIsIndeterminate(t *testing.T) {
	// The finalized head is block 6004, inside Epoch 1000. Nothing was checked, so exit 2,
	// and the reason names the finality level so the reader knows what to lower.
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--finality", "finalized")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "\nINDETERMINATE: ") || !strings.Contains(stdout, "finalized head is block 6004") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestEpoch_EndpointsDisagreeingIsIndeterminate(t *testing.T) {
	// Two endpoints serving the same history with different block hashes. Neither is
	// preferred; the run stops with exit 2 and says which two disagree.
	other := scenario()
	other.HashSalt = 1

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario(), other}, "epoch", "1000")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "INDETERMINATE: ") || !strings.Contains(stdout, "disagree") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestEpoch_EndpointsAgreeingCrossCheck(t *testing.T) {
	// The same scenario from two endpoints that agree is a MATCH, and the header names both.
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario(), scenario()}, "epoch", "1000")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "endpoint 1 (127.0.0.1:") || !strings.Contains(stdout, "endpoint 2 (127.0.0.1:") {
		t.Errorf("header does not name both endpoints:\n%s", stdout)
	}
}

func TestEpoch_AFundedEpochWithNoRootExitsZero(t *testing.T) {
	// Epoch 999 is funded and has no eligible Holder — Alice and Bob bought at its last
	// second — so no Root is right, whether the Distributor has already skipped it or not.
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "999")
	if code != 0 {
		t.Fatalf("Skipped: exit %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "posted       no Root: skipped by a later Root") {
		t.Errorf("stdout = %q", stdout)
	}

	// Before the Root for 1000 was posted, 999 was simply funded and unrooted.
	node := scenario()
	node.Tips["safe"] = 6007
	l := node.Ledgers[999]
	l.Skipped = false
	node.Ledgers[999] = l

	code, stdout, _ = cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "999")
	if code != 0 {
		t.Fatalf("pending: exit %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "\n  root     ok    no Root posted, and none recomputed") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestEpoch_NoRootPostedYetIsIndeterminate(t *testing.T) {
	// Epoch 1000 has closed at safe finality but its Root is not up yet: the Recompute has
	// a tree and the chain has nothing. Exit 2, and the reason says so.
	node := scenario()
	node.Tips["safe"] = 6007
	delete(node.Ledgers, 1000)

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1000")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "INDETERMINATE: no Root posted yet: the Recompute has one over 2 Holders") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestLatest_ResolvesToTheMostRecentRoot(t *testing.T) {
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "latest")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "\nepoch 1000  window") || !strings.Contains(stdout, "\nMATCH\n") {
		t.Errorf("stdout = %q", stdout)
	}

	// Voided and not re-posted: the ledger holds no Root, so the log alone does not make
	// Epoch 1000 the latest, and with nothing before it there is no latest at all.
	node := scenario()
	delete(node.Ledgers, 1000)

	code, stdout, _ = cli(t, testDeployment, []*fakenode.Node{node}, "latest")
	if code != 2 {
		t.Fatalf("voided: exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "INDETERMINATE: no Root has been posted") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestSync_AdvancesTheCacheAndReportsIt(t *testing.T) {
	code, stdout, stderr := cli(t, testDeployment, []*fakenode.Node{scenario()}, "sync")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "synced: 3 records appended") || strings.Contains(stdout, "MATCH") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "cache: through block 6012 of 6012") {
		t.Errorf("no progress on stderr: %q", stderr)
	}
}

func TestRun_JSONCarriesTheSchemaAndTheVerdict(t *testing.T) {
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--json")

	if code != 0 {
		t.Fatalf("exit %d, want 0", code)
	}

	var doc struct {
		Schema  string `json:"schema"`
		Verdict string `json:"verdict"`
		Run     struct {
			DevWallet string `json:"devWallet"`
		} `json:"run"`
		Epochs []struct {
			ID         uint64 `json:"id"`
			Assertions []struct {
				Name   string `json:"name"`
				Status string `json:"status"`
			} `json:"assertions"`
		} `json:"epochs"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if doc.Schema != "nutz-verify-report/1" || doc.Verdict != "MATCH" || doc.Run.DevWallet != devWallet.String() {
		t.Errorf("top level = %+v", doc)
	}
	if len(doc.Epochs) != 1 || doc.Epochs[0].ID != 1000 || len(doc.Epochs[0].Assertions) != 4 {
		t.Errorf("epochs = %+v", doc.Epochs)
	}
}

func TestRun_RPCFailureIsIndeterminate(t *testing.T) {
	node := scenario()
	node.HTTPStatus = 503

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1000")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "INDETERMINATE: ") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRun_CacheFromAnotherHistoryNeedsFresh(t *testing.T) {
	// The same directory, first synced for a history starting at block 1, then opened by
	// a build that starts at block 0. The Cache refuses, exit 2, and names the remedy.
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	node := scenario()
	url := node.Serve(t)
	args := []string{"epoch", "1000", "--rpc", url, "--rate", "1000000"}

	var stdout bytes.Buffer
	if code := run(context.Background(), args, &stdout, io.Discard, testDeployment); code != 0 {
		t.Fatalf("first run: exit %d\n%s", code, stdout.String())
	}

	later := testDeployment
	later.TokenBlock = 0

	stdout.Reset()
	if code := run(context.Background(), args, &stdout, io.Discard, later); code != 2 {
		t.Fatalf("mismatched start: exit %d, want 2\n%s", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "--fresh") {
		t.Errorf("no remedy named:\n%s", stdout.String())
	}

	stdout.Reset()
	if code := run(context.Background(), append(args, "--fresh"), &stdout, io.Discard, later); code != 0 {
		t.Fatalf("--fresh: exit %d, want 0\n%s", code, stdout.String())
	}
}

func TestEpoch_ChainWalksEveryRootFromDeploy(t *testing.T) {
	// A second rooted Epoch after 1000: 1001 spends its own funding plus 1000's carryOut
	// [0 0 0 1 1] over the same Holders. The chain walk asserts 1000 with the deploy-time
	// Carry and 1001 with 1000's recomputed carryOut, and both sections are reported.
	node := scenario()
	node.Tips["safe"] = 6019
	root1001 := chain.Hash{0x10, 0x01} // wrong on purpose: this test is about the walk, not the tree
	node.RootPosted(6014, distributor, 1001, root1001, fakenode.Amounts(4, 0, 0, 0, 0), fakenode.Amounts(0, 0, 0, 1, 1))
	node.Ledgers[1001] = fakenode.Ledger{
		Root:         root1001,
		RootPostedAt: 6014 * 600,
		Funded:       fakenode.Amounts(4, 0, 0, 0, 0),
		Totals:       fakenode.Amounts(4, 0, 0, 0, 0),
	}

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1001", "--chain")

	if code != 1 {
		t.Fatalf("exit %d, want 1 (the 1001 Root is deliberately wrong)", code)
	}
	for _, want := range []string{
		"\nepoch 1000  window",
		"\n  carryIn  ok    [500 0 0 0 7] = nothing before deploy + the funding of 2 skipped Epochs",
		"\nepoch 1001  window",
		"\n  carryIn  ok    [0 0 0 1 1] = carryOut of epoch 1000",
		"\n  root     FAIL  ",
		"\nMISMATCH\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q", want)
		}
	}
	if n := strings.Count(stdout, "\n  root     "); n != 2 {
		t.Errorf("%d root lines, want one per rooted Epoch", n)
	}
}

func TestEpoch_CarryInFromThePreviousRootsRecompute(t *testing.T) {
	// Without --chain the walk starts at the previous rooted Epoch, recomputed with its own
	// posted carryIn, and 1001's carryIn is asserted against that carryOut.
	node := scenario()
	node.Tips["safe"] = 6019
	node.RootPosted(6014, distributor, 1001, chain.Hash{1}, fakenode.Amounts(4, 0, 0, 0, 0), fakenode.Amounts(0, 0, 0, 1, 2))
	node.Ledgers[1001] = fakenode.Ledger{Root: chain.Hash{1}, RootPostedAt: 6014 * 600, Funded: fakenode.Amounts(4, 0, 0, 0, 0), Totals: fakenode.Amounts(4, 0, 0, 0, 0)}

	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1001")

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "\n  carryIn  FAIL  token 4: posted 2, but carryOut of epoch 1000 leaves 1") {
		t.Errorf("stdout = %q", stdout)
	}
	if strings.Contains(stdout, "\nepoch 1000  window") {
		t.Errorf("the previous Epoch is not reported without --chain:\n%s", stdout)
	}
}

func writeBundle(t *testing.T, allocations, root string) string {
	t.Helper()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "allocations.json"), []byte(allocations), 0o644); err != nil {
		t.Fatal(err)
	}
	if root != "" {
		if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte(root+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

const publishedRows = `[
  {"account": "0xa1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", "amounts": ["1125", "0", "375", "0", "5"], "twab": "3", "multBps": 10000},
  ["0xb2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", ["375", "0", "%s", "0", "1"], "1", 10000]
]`

func TestArtifacts_DiffTheBundleAfterTheVerdict(t *testing.T) {
	// A faithful bundle, in both row spellings, agrees.
	dir := writeBundle(t, fmt.Sprintf(publishedRows, "125"), caseRoot.String())
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--artifacts", dir)
	if code != 0 {
		t.Fatalf("faithful: exit %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "agrees with the Recompute") {
		t.Errorf("stdout = %q", stdout)
	}

	// A corrupted bundle reports the first differing row — and the Verdict is still the
	// Recompute's own, because the bundle is compared, never consulted.
	dir = writeBundle(t, fmt.Sprintf(publishedRows, "120"), caseRoot.String())
	code, stdout, _ = cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--artifacts", dir)
	if code != 0 {
		t.Fatalf("corrupted rows: exit %d, want 0\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "DIFFERS: row 1 ("+bob.String()+"): amounts[2] published 120, recomputed 125") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "\nMATCH\n") {
		t.Errorf("the bundle moved the Verdict:\n%s", stdout)
	}

	// A wrong root.txt over right rows.
	dir = writeBundle(t, fmt.Sprintf(publishedRows, "125"), chain.Hash{0xff}.String())
	_, stdout, _ = cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--artifacts", dir)
	if !strings.Contains(stdout, "DIFFERS: root.txt is "+chain.Hash{0xff}.String()+", recomputed "+caseRoot.String()) {
		t.Errorf("stdout = %q", stdout)
	}

	// A bundle that cannot be read is said so, and the Verdict is untouched: an unreadable
	// bundle must not turn a MATCH — or a MISMATCH — into an exit 2.
	code, stdout, _ = cli(t, testDeployment, []*fakenode.Node{scenario()}, "epoch", "1000", "--artifacts", filepath.Join(dir, "missing"))
	if code != 0 {
		t.Errorf("missing bundle: exit %d, want 0", code)
	}
	if !strings.Contains(stdout, "NOT COMPARED: ") || !strings.Contains(stdout, "\nMATCH\n") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestArtifacts_CannotMaskAMismatch(t *testing.T) {
	// A bundle that agrees with a wrong Root is still diffed against the Recompute, and the
	// Recompute's own Verdict — MISMATCH, exit 1 — is what the run exits with.
	node := scenario()
	wrong := chain.Hash{0xbb}
	node.DropLastLog()
	node.RootPosted(6008, distributor, 1000, wrong, fakenode.Amounts(1500, 0, 500, 0, 7), fakenode.Amounts(500, 0, 0, 0, 7))
	l := node.Ledgers[1000]
	l.Root, l.Totals = wrong, fakenode.Amounts(1500, 0, 500, 0, 7)
	node.Ledgers[1000] = l

	dir := writeBundle(t, fmt.Sprintf(publishedRows, "125"), wrong.String())
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1000", "--artifacts", dir)

	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stdout, "DIFFERS: root.txt is "+wrong.String()) || !strings.Contains(stdout, "\nMISMATCH\n") {
		t.Errorf("stdout = %q", stdout)
	}

	// And an unreadable bundle over the same MISMATCH is still exit 1.
	code, _, _ = cli(t, testDeployment, []*fakenode.Node{node}, "epoch", "1000", "--artifacts", filepath.Join(dir, "missing"))
	if code != 1 {
		t.Errorf("missing bundle: exit %d, want 1", code)
	}
}

func TestRun_UnpinnedBuildRefusesToRun(t *testing.T) {
	// The shipped pinned Deployment has no addresses until launch. Exit 2, never a
	// confident answer about a zero address.
	code, stdout, _ := cli(t, epoch.Pinned(), []*fakenode.Node{scenario()}, "epoch", "1000")

	if code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(stdout, "INDETERMINATE: this build is not pinned") {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestRun_UsageErrorsExitTwo(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{},
		{"epoch"},
		{"epoch", "abc", "--rpc", "http://x"},
		{"epoch", "1", "--rpc", "http://x", "--finality", "soon"},
		{"epoch", "1"},
		{"sync", "--chain", "--rpc", "http://x"},
		{"verify", "--rpc", "http://x"},
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, io.Discard, &stderr, testDeployment); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%v: no usage on stderr", args)
		}
	}
}

func TestRun_HeaderEchoesTheRate(t *testing.T) {
	code, stdout, _ := cli(t, testDeployment, []*fakenode.Node{scenario()}, "sync")
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stdout, "at 1e+06 calls/s each") {
		t.Errorf("stdout = %q", stdout)
	}
}
