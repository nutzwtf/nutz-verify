package chain

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// TestAnvil drives a real node through the real Distributor and asserts every decoder in
// this package against what came back. One node, many subtests: bringing the world up costs
// a fork and forty transactions, and none of the assertions mutate it.
func TestAnvil(t *testing.T) {
	w := buildWorld(t)

	reader, err := New(Config{
		Endpoints:   []string{w.rpc},
		Token:       w.nutz,
		Distributor: w.distributor,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()

	head, err := reader.Head(ctx, Latest)
	if err != nil {
		t.Fatalf("Head = %v", err)
	}

	t.Run("chain id", func(t *testing.T) {
		got, err := reader.ChainID(ctx)
		if err != nil {
			t.Fatalf("ChainID = %v", err)
		}
		if got != harnessChainID {
			t.Errorf("ChainID = %d, want %d", got, harnessChainID)
		}
	})

	t.Run("every finality tag is served", func(t *testing.T) {
		// A node that does not serve safe or finalized answers null, and spec §4's default
		// is safe. Finding that out here beats finding it out in a Dispute window.
		var previous uint64
		for _, level := range []Finality{Finalized, Safe, Latest} {
			tip, err := reader.Head(ctx, level)
			if err != nil {
				t.Fatalf("Head(%s) = %v", level, err)
			}
			if tip.Block.Number < previous {
				t.Errorf("%s head is block %d, below the previous level's %d",
					level, tip.Block.Number, previous)
			}

			previous = tip.Block.Number
		}
	})

	t.Run("Transfer logs", func(t *testing.T) {
		got, err := reader.Transfers(ctx, w.nutzBlock, head.Block.Number)
		if err != nil {
			t.Fatalf("Transfers = %v", err)
		}
		if len(got) != len(w.transfers) {
			t.Fatalf("got %d transfers, want %d: %+v", len(got), len(w.transfers), got)
		}

		for i, want := range w.transfers {
			switch {
			case got[i].From != want.from:
				t.Errorf("transfer %d from %s, want %s", i, got[i].From, want.from)
			case got[i].To != want.to:
				t.Errorf("transfer %d to %s, want %s", i, got[i].To, want.to)
			case got[i].Value.Cmp(big.NewInt(want.value)) != 0:
				t.Errorf("transfer %d value %s, want %d", i, got[i].Value, want.value)
			case got[i].Timestamp != want.at:
				t.Errorf("transfer %d at %d, want %d", i, got[i].Timestamp, want.at)
			}
		}
	})

	t.Run("ExcludedAppended logs", func(t *testing.T) {
		// Three: two the constructor emitted for the base list, and one executed append.
		// The base entries being in the stream at all is the nutz-contracts change of
		// 2026-09-14 that makes the log stream self-sufficient (ADR-0002).
		got, err := reader.Exclusions(ctx, w.distributorBlock, head.Block.Number)
		if err != nil {
			t.Fatalf("Exclusions = %v", err)
		}
		if len(got) != 3 {
			t.Fatalf("got %d exclusions, want 3: %+v", len(got), got)
		}

		if got[0].Account != w.dead || got[1].Account != w.excludedBase {
			t.Errorf("base entries = %s, %s, want %s, %s",
				got[0].Account, got[1].Account, w.dead, w.excludedBase)
		}
		if got[0].BlockNumber != w.distributorBlock || got[1].BlockNumber != w.distributorBlock {
			t.Errorf("base entries are not in the constructor's block %d", w.distributorBlock)
		}
		if got[2].Account != w.carol || got[2].Timestamp != w.carolAt {
			t.Errorf("late append = %s at %d, want %s at %d",
				got[2].Account, got[2].Timestamp, w.carol, w.carolAt)
		}
	})

	t.Run("the Excluded set as of an Epoch", func(t *testing.T) {
		exclusions, err := reader.Exclusions(ctx, w.distributorBlock, head.Block.Number)
		if err != nil {
			t.Fatalf("Exclusions = %v", err)
		}

		// Carol's append landed at minute 30 of Epoch B. Spec §5 puts her outside Epoch A
		// entirely and inside the whole of Epoch B — not from her block onward.
		earlier := ExcludedAsOf(w.epochA, exclusions)
		if earlier.Contains(w.carol) {
			t.Errorf("epoch %d's set contains an address appended %d epochs later",
				w.epochA, w.epochB-w.epochA)
		}
		if !earlier.Contains(w.dead) || !earlier.Contains(w.excludedBase) {
			t.Errorf("epoch %d's set is missing a base entry: %v", w.epochA, earlier)
		}

		later := ExcludedAsOf(w.epochB, exclusions)
		if !later.Contains(w.carol) {
			t.Errorf("epoch %d's set does not contain the address appended inside it: %v", w.epochB, later)
		}
		if !slices.IsSortedFunc(later, compareAddresses) {
			t.Errorf("the set is not ascending: %v", later)
		}

		// The one place the reconstruction can be checked against the contract's own answer.
		// excluded() is never an input to a Recompute (ADR-0002) — it returns today's list —
		// but today's list is exactly the current Epoch's set.
		live, err := reader.Excluded(ctx, head.Block.Number)
		if err != nil {
			t.Fatalf("Excluded = %v", err)
		}

		current := ExcludedAsOf(uint64(head.Block.Timestamp)/twab.EpochSeconds, exclusions)
		slices.SortFunc(live, compareAddresses)

		if !slices.Equal([]Address(current), live) {
			t.Errorf("the log stream rebuilds %v, and excluded() holds %v", current, live)
		}
	})

	t.Run("the exclusion set hash", func(t *testing.T) {
		exclusions, err := reader.Exclusions(ctx, w.distributorBlock, head.Block.Number)
		if err != nil {
			t.Fatalf("Exclusions = %v", err)
		}

		set := ExcludedAsOf(w.epochB, exclusions)

		// Packed independently and hashed by foundry, so engineering spec §4.5's hash is
		// pinned against something that is not this package.
		packed := make([]byte, 0, len(set)*addressSize)
		for _, a := range set {
			packed = append(packed, a[:]...)
		}

		want := w.cast("keccak", hexBytes(packed))
		if got := set.Hash().String(); got != strings.TrimSpace(want) {
			t.Errorf("exclusion set hash = %s, want %s", got, want)
		}
	})

	t.Run("RootPosted logs", func(t *testing.T) {
		got, err := reader.Roots(ctx, w.distributorBlock, head.Block.Number)
		if err != nil {
			t.Fatalf("Roots = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d Roots, want 1: %+v", len(got), got)
		}

		posted := got[0]
		switch {
		case posted.Kind != KindEpoch:
			t.Errorf("kind = %s, want epoch", posted.Kind)
		case posted.ID != w.epochA:
			t.Errorf("id = %d, want %d", posted.ID, w.epochA)
		case posted.Root != w.root:
			t.Errorf("root = %s, want %s", posted.Root, w.root)
		}

		assertAmounts(t, "totals", posted.Totals, w.totals)

		// The Carry the Root was computed against, and the only place it is published: it is
		// what Epoch A-1's funding rolled into when the first Root skipped it.
		assertAmounts(t, "carryIn", posted.CarryIn, w.carryIn)
	})

	t.Run("ledger for the rooted Epoch", func(t *testing.T) {
		got, err := reader.Ledger(ctx, KindEpoch, w.epochA, head.Block.Number)
		if err != nil {
			t.Fatalf("Ledger = %v", err)
		}
		if !got.HasRoot() || got.Root != w.root {
			t.Errorf("root = %s, posted at %d", got.Root, got.RootPostedAt)
		}
		if got.Skipped {
			t.Error("skipped = true for an Epoch that has a Root")
		}

		assertAmounts(t, "funded", got.Funded, w.funded)
		assertAmounts(t, "totals", got.Totals, w.totals)
		assertAmounts(t, "claimed", got.Claimed, amountsOf(0, 0, 0, 0, 0))
	})

	t.Run("ledger for the skipped Epoch", func(t *testing.T) {
		// Funded, passed over, its funding rolled into Carry: engineering spec §3.3's Skipped
		// mechanism, and the one value that exercises the bool word in the tuple.
		got, err := reader.Ledger(ctx, KindEpoch, w.epochA-1, head.Block.Number)
		if err != nil {
			t.Fatalf("Ledger = %v", err)
		}
		if !got.Skipped {
			t.Error("skipped = false for the Epoch the Root passed over")
		}
		if got.HasRoot() {
			t.Errorf("a skipped Epoch carries a Root posted at %d", got.RootPostedAt)
		}

		assertAmounts(t, "funded", got.Funded, w.carryIn)
	})

	t.Run("ledger for an Epoch that has nothing", func(t *testing.T) {
		// Eighteen zero words, which must decode as "no Root" rather than fail.
		got, err := reader.Ledger(ctx, KindEpoch, w.epochB, head.Block.Number)
		if err != nil {
			t.Fatalf("Ledger = %v", err)
		}
		if got.HasRoot() || got.Skipped {
			t.Errorf("an untouched Epoch = %+v", got)
		}
	})

	t.Run("a ledger read is pinned to its block", func(t *testing.T) {
		// The Root was posted after Epoch A closed, so the same call at Epoch A's end block
		// answers "no Root". This is why Ledger has no "latest" form: a Recompute that read
		// at the tip would compare against a different answer every time it ran.
		tip, err := reader.Head(ctx, Latest)
		if err != nil {
			t.Fatal(err)
		}

		endBlock, err := reader.EndBlock(ctx, w.epochA, tip)
		if err != nil {
			t.Fatalf("EndBlock = %v", err)
		}

		atEnd, err := reader.Ledger(ctx, KindEpoch, w.epochA, endBlock.Number)
		if err != nil {
			t.Fatalf("Ledger at the end block = %v", err)
		}
		if atEnd.HasRoot() {
			t.Error("the Root is already posted at the Epoch's end block")
		}
	})

	t.Run("the end block is the last before the boundary", func(t *testing.T) {
		tip, err := reader.Head(ctx, Latest)
		if err != nil {
			t.Fatal(err)
		}

		got, err := reader.EndBlock(ctx, w.epochA, tip)
		if err != nil {
			t.Fatalf("EndBlock = %v", err)
		}

		boundary := twab.WindowOf(w.epochA).End
		if got.Timestamp >= boundary {
			t.Errorf("end block %d is at %d, which is not before %d", got.Number, got.Timestamp, boundary)
		}

		next, err := reader.BlockByNumber(ctx, got.Number+1)
		if err != nil {
			t.Fatalf("BlockByNumber = %v", err)
		}
		if next.Timestamp < boundary {
			t.Errorf("block %d is at %d, still before %d, so it was the end block",
				next.Number, next.Timestamp, boundary)
		}
	})

	t.Run("an Epoch the chain has not reached", func(t *testing.T) {
		tip, err := reader.Head(ctx, Latest)
		if err != nil {
			t.Fatal(err)
		}

		var open *EpochNotClosed
		if _, err := reader.EndBlock(ctx, w.epochB+1000, tip); !errors.As(err, &open) {
			t.Fatalf("EndBlock = %v, want EpochNotClosed", err)
		}
	})

	t.Run("two endpoints over one node agree", func(t *testing.T) {
		// Not a tautology worth much on its own, but it exercises the concurrent path and
		// the canonical comparison against real responses: a comparison that spuriously
		// disagreed would make every multi-endpoint run INDETERMINATE.
		crossChecked, err := New(Config{
			Endpoints:   []string{w.rpc, w.rpc},
			Token:       w.nutz,
			Distributor: w.distributor,
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := crossChecked.Transfers(ctx, w.nutzBlock, head.Block.Number); err != nil {
			t.Errorf("Transfers = %v", err)
		}
		if _, err := crossChecked.Ledger(ctx, KindEpoch, w.epochA, head.Block.Number); err != nil {
			t.Errorf("Ledger = %v", err)
		}
		if _, err := crossChecked.Head(ctx, Safe); err != nil {
			t.Errorf("Head = %v", err)
		}
	})

	t.Run("an endpoint that is not a node", func(t *testing.T) {
		// Half of ADR-0002's cross-check is that an endpoint we cannot reach fails the read
		// rather than being dropped in favour of the one that answered.
		mixed, err := New(Config{
			Endpoints:   []string{w.rpc, "http://127.0.0.1:1"},
			Token:       w.nutz,
			Distributor: w.distributor,
		})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := mixed.Head(context.WithoutCancel(ctx), Latest); err == nil {
			t.Error("Head = nil error, want the unreachable endpoint to fail the read")
		}
	})
}

func compareAddresses(a, b Address) int {
	return strings.Compare(string(a[:]), string(b[:]))
}

// ---------------------------------------------------------------- the world

type wantTransfer struct {
	at       int64
	from, to Address
	value    int64
}

// world is the chain this harness builds, and what it expects the Verifier to read back.
type world struct {
	*anvil

	nutz             Address
	distributor      Address
	nutzBlock        uint64
	distributorBlock uint64

	// epochA is funded, rooted, and preceded by a funded Epoch the Root skips. epochB is 49
	// Epochs later — past the Distributor's 48-hour timelock — and is where the late
	// exclusion lands, at minute 30.
	epochA uint64
	epochB uint64

	dead, excludedBase, carol Address
	alice, bob                Address

	root    Hash
	totals  Amounts
	funded  Amounts
	carryIn Amounts

	transfers []wantTransfer
	carolAt   int64
}

// The harness amounts. carryIn is what Epoch A-1's funding becomes once the first Root skips
// it, which is what makes RootPosted's second five-word group non-zero.
const (
	skippedFunding = 1000
	epochFunding   = 5000
	totalsBase     = 4000
)

func buildWorld(t *testing.T) *world {
	node := startAnvil(t)

	w := &world{
		anvil:        node,
		dead:         node.address("0x000000000000000000000000000000000000dEaD"),
		excludedBase: node.address("0x00000000000000000000000000000000000000E1"),
		carol:        node.address("0x00000000000000000000000000000000000000c3"),
		alice:        node.accounts[1],
		bob:          node.accounts[2],
		root:         repeatHash(0x7e),
	}

	for i := range w.totals {
		w.totals[i] = big.NewInt(totalsBase + int64(i))
		w.funded[i] = big.NewInt(epochFunding)
		w.carryIn[i] = big.NewInt(skippedFunding)
	}

	// Start the timeline on a clean Epoch boundary ahead of whatever the fork left us at, so
	// every block below sits exactly where the assertions say it does.
	base := ((node.latestTimestamp() / twab.EpochSeconds) + 2) * twab.EpochSeconds
	w.epochA = uint64(base / twab.EpochSeconds)
	w.epochB = w.epochA + 49 // past TIMELOCK, and far enough that Epoch A cannot contain it

	mock := node.bytecode("MockERC20")
	nutzReceipt := node.deployReceipt(mock, "constructor(string,string)", "NUTZ", "NUTZ")
	w.nutz, w.nutzBlock = node.address(nutzReceipt.ContractAddress), node.blockOf(nutzReceipt)

	var tokens [TokenCount]Address
	for i, symbol := range []string{"SPY", "NVDA", "MU", "SPCX", "USDG"} {
		tokens[i] = node.deploy(mock, "constructor(string,string)", symbol, symbol)
	}

	// The Distributor lands in Epoch A-1, so its constructor sets rootedThrough to A-2 and
	// the first Root for A skips exactly one funded Epoch.
	node.warp(base - twab.EpochSeconds + 10)
	distributorReceipt := node.deployReceipt(node.bytecode("NutzDistributor"),
		"constructor(address[3],address,address,address[5],uint256,uint256,uint256,uint256,address[])",
		addressList(node.accounts[1], node.accounts[2], node.accounts[3]), // Signers
		node.accounts[4].String(), // Keeper
		node.accounts[0].String(), // Converter: an EOA, so it can fund
		addressList(tokens[:]...),
		"100000", "40000", "1000000000", "10000000000",
		addressList(w.dead, w.excludedBase))
	w.distributor, w.distributorBlock = node.address(distributorReceipt.ContractAddress), node.blockOf(distributorReceipt)

	w.fundFrom(tokens, base)
	w.proposeCarol()
	w.moveNutz(base)
	w.postRootForEpochA(base)
	w.appendCarol()

	return w
}

// fundFrom stocks the Converter EOA and funds Epoch A-1 and Epoch A. Approving once for both
// keeps this to ten transactions rather than twenty.
//
// The Distributor refuses to fund an Epoch that has not opened yet, so each funding happens
// from inside its own Epoch — which is also how the Converter's Sweep does it.
func (w *world) fundFrom(tokens [TokenCount]Address, base int64) {
	total := fmt.Sprint(skippedFunding + epochFunding)

	for _, token := range tokens {
		w.send(token, "mint(address,uint256)", w.accounts[0].String(), total)
		w.send(token, "approve(address,uint256)", w.distributor.String(), total)
	}

	w.send(w.distributor, "notifyEpochFunding(uint256,uint256[5],uint256)",
		fmt.Sprint(w.epochA-1), repeatArg(skippedFunding), "0")

	w.warp(base + 10)
	w.send(w.distributor, "notifyEpochFunding(uint256,uint256[5],uint256)",
		fmt.Sprint(w.epochA), repeatArg(epochFunding), "0")
}

// proposeCarol schedules the exclusion. It cannot execute for 48 hours, which is why the
// Epoch it lands in is 49 Epochs after the rooted one.
func (w *world) proposeCarol() {
	typed := w.typedData("AppendExcluded",
		`{"name":"account","type":"address"},{"name":"nonce","type":"uint256"}`,
		fmt.Sprintf(`"account":"%s","nonce":"%s"`, w.carol, w.nonce()))

	w.send(w.distributor, "proposeExclusion(address,bytes,bytes)",
		w.carol.String(), w.signTyped(anvilKeys[1], typed), w.signTyped(anvilKeys[2], typed))
}

// moveNutz produces the Transfer logs: two mints and a wallet-to-wallet send, all inside
// Epoch A and each in its own block at a timestamp the assertions know.
func (w *world) moveNutz(base int64) {
	mints := []struct {
		at    int64
		to    Address
		value int64
	}{
		{base + 100, w.alice, 1_000_000},
		{base + 200, w.bob, 500_000},
	}

	for _, mint := range mints {
		w.warp(mint.at)
		w.send(w.nutz, "mint(address,uint256)", mint.to.String(), fmt.Sprint(mint.value))
		w.transfers = append(w.transfers, wantTransfer{at: mint.at, to: mint.to, value: mint.value})
	}

	w.warp(base + 2000)
	w.sendFrom(anvilKeys[1], w.nutz, "transfer(address,uint256)", w.bob.String(), "250000")
	w.transfers = append(w.transfers,
		wantTransfer{at: base + 2000, from: w.alice, to: w.bob, value: 250_000})
}

// postRootForEpochA closes Epoch A and posts its Root, 2-of-3 over EIP-712 typed data.
func (w *world) postRootForEpochA(base int64) {
	w.warp(base + twab.EpochSeconds + 10)
	w.mine()

	totals := fmt.Sprintf("[%d,%d,%d,%d,%d]",
		totalsBase, totalsBase+1, totalsBase+2, totalsBase+3, totalsBase+4)

	typed := w.typedData("PostRoot",
		`{"name":"kind","type":"uint8"},{"name":"id","type":"uint256"},{"name":"root","type":"bytes32"},`+
			`{"name":"totals","type":"uint256[5]"},{"name":"nonce","type":"uint256"}`,
		fmt.Sprintf(`"kind":0,"id":%d,"root":"%s","totals":["%d","%d","%d","%d","%d"],"nonce":"%s"`,
			w.epochA, w.root, totalsBase, totalsBase+1, totalsBase+2, totalsBase+3, totalsBase+4, w.nonce()))

	w.send(w.distributor, "postRoot(uint8,uint256,bytes32,uint256[5],bytes,bytes)",
		"0", fmt.Sprint(w.epochA), w.root.String(), totals,
		w.signTyped(anvilKeys[1], typed), w.signTyped(anvilKeys[3], typed))
}

// appendCarol executes the scheduled exclusion at minute 30 of Epoch B — mid-Epoch on
// purpose, because the rule under test is that it covers the whole of that Epoch anyway.
func (w *world) appendCarol() {
	w.carolAt = int64(w.epochB)*twab.EpochSeconds + 1800

	w.warp(w.carolAt)
	w.send(w.distributor, "executeExclusion(address)", w.carol.String())

	// Close Epoch B so it can be asked about at all.
	w.warp(int64(w.epochB+1)*twab.EpochSeconds + 10)
	w.mine()
}

// typedData builds the EIP-712 document the Distributor's 2-of-3 verifies against.
func (w *world) typedData(primary, fields, message string) string {
	return fmt.Sprintf(`{"types":{"EIP712Domain":[`+
		`{"name":"name","type":"string"},{"name":"version","type":"string"},`+
		`{"name":"chainId","type":"uint256"},{"name":"verifyingContract","type":"address"}],`+
		`"%s":[%s]},"primaryType":"%s",`+
		`"domain":{"name":"NutzDistributor","version":"1","chainId":%d,"verifyingContract":"%s"},`+
		`"message":{%s}}`,
		primary, fields, primary, harnessChainID, w.distributor, message)
}

// nonce is the Distributor's global signing nonce, which every 2-of-3 digest carries.
func (w *world) nonce() string {
	raw := w.cast("call", "--rpc-url", w.rpc, w.distributor.String(), "nonce()(uint256)")

	return strings.Fields(raw)[0]
}

func addressList(addrs ...Address) string {
	parts := make([]string, 0, len(addrs))
	for _, a := range addrs {
		parts = append(parts, a.String())
	}

	return "[" + strings.Join(parts, ",") + "]"
}

func repeatArg(value int64) string {
	return fmt.Sprintf("[%d,%d,%d,%d,%d]", value, value, value, value, value)
}
