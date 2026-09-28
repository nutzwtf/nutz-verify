package epoch_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/epoch"
)

// A Reader for one pair of contracts and a Deployment pinning another would read one
// history against another's ledger. New refuses the pair rather than reporting on nothing.
func TestNew_RefusesAReaderForAnotherDeployment(t *testing.T) {
	t.Parallel()

	c := loadCase(t, filepath.Join(caseDir, "funding-plus-carry.json"))
	node, dep, _ := chainOf(t, c)

	other := dep
	other.Token = repeatAddr(0x99)

	reader, err := chain.New(chain.Config{
		Endpoints:   []string{node.Serve(t)},
		Token:       other.Token,
		Distributor: other.Distributor,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := epoch.New(reader, chain.Tip{Finality: chain.Safe}, t.TempDir(), dep, epoch.Options{}); err == nil {
		t.Fatal("New accepted a Reader for another token")
	}

	// The factory too: a Reader asking another factory for the curve would exclude another
	// launch's curve, or none (ADR-0007).
	reader, err = chain.New(chain.Config{
		Endpoints:   []string{node.Serve(t)},
		Token:       dep.Token,
		Distributor: dep.Distributor,
		PonsFactory: repeatAddr(0x98),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := epoch.New(reader, chain.Tip{Finality: chain.Safe}, t.TempDir(), dep, epoch.Options{}); err == nil {
		t.Fatal("New accepted a Reader for another Pons factory")
	}

	if _, err := epoch.New(reader, chain.Tip{Finality: chain.Safe}, t.TempDir(), epoch.Pinned(), epoch.Options{}); !errors.Is(err, epoch.ErrNotPinned) {
		t.Fatalf("New with the unpinned Deployment: %v, want ErrNotPinned", err)
	}
}

// The Ledger only advances, and an Engine is one tip; asking for an earlier Epoch after a
// later one replays the retained history rather than refusing, and gets the same answer a
// fresh Engine would.
func TestCompute_AnEarlierEpochAfterALaterOne(t *testing.T) {
	t.Parallel()

	c := loadCase(t, filepath.Join(caseDir, "funding-plus-carry.json"))
	node, dep, _ := chainOf(t, c)
	ctx := context.Background()

	eng := newEngine(t, node, dep)
	if _, err := eng.Compute(ctx, c.EpochID); err != nil {
		t.Fatalf("Compute(%d): %v", c.EpochID, err)
	}
	earlier, err := eng.Compute(ctx, c.EpochID-1)
	if err != nil {
		t.Fatalf("Compute(%d) after %d: %v", c.EpochID-1, c.EpochID, err)
	}

	fresh, err := newEngine(t, node, dep).Compute(ctx, c.EpochID-1)
	if err != nil {
		t.Fatalf("fresh Compute(%d): %v", c.EpochID-1, err)
	}

	if earlier.HasRoot != fresh.HasRoot || earlier.Root != fresh.Root || len(earlier.Allocations) != len(fresh.Allocations) {
		t.Errorf("a replayed Compute differs from a fresh one:\n got HasRoot=%v Root=%s holders=%d\nwant HasRoot=%v Root=%s holders=%d",
			earlier.HasRoot, earlier.Root, len(earlier.Allocations), fresh.HasRoot, fresh.Root, len(fresh.Allocations))
	}
	for t2 := range earlier.Totals {
		if earlier.Totals[t2].Cmp(fresh.Totals[t2]) != 0 {
			t.Errorf("Totals[%d] = %s replayed, %s fresh", t2, earlier.Totals[t2], fresh.Totals[t2])
		}
	}
}

// Sync runs once per Engine and Compute's implicit call returns the same report, so a
// caller that let Compute sync can still read what happened to the Cache.
func TestSync_RunsOnceAndIsReported(t *testing.T) {
	t.Parallel()

	c := loadCase(t, filepath.Join(caseDir, "funding-plus-carry.json"))
	node, dep, _ := chainOf(t, c)
	ctx := context.Background()

	eng := newEngine(t, node, dep)
	if _, ok := eng.Synced(); ok {
		t.Fatal("Synced() reports a sync before one ran")
	}

	if _, err := eng.Compute(ctx, c.EpochID); err != nil {
		t.Fatal(err)
	}

	first, ok := eng.Synced()
	if !ok {
		t.Fatal("Synced() reports nothing after Compute")
	}
	if first.Appended != int64(len(c.Transfers)) {
		t.Errorf("Appended = %d, want the Case's %d transfers", first.Appended, len(c.Transfers))
	}

	again, err := eng.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Errorf("a second Sync reported %+v, the first %+v", again, first)
	}
}

// ADR-0007: the Token's bonding curve is Excluded in every Epoch, read from the factory's
// launch record. A factory that has no record for the Token is an error and never an empty
// set: an Engine that shrugged would count the curve's supply as a Holder's.
func TestCompute_RefusesATokenTheFactoryNeverLaunched(t *testing.T) {
	t.Parallel()

	c := loadCase(t, filepath.Join(caseDir, "funding-plus-carry.json"))
	node, dep, _ := chainOf(t, c)
	delete(node.Curves, dep.Token) // the factory stands, the record does not

	eng := newEngine(t, node, dep)
	_, err := eng.Compute(context.Background(), c.EpochID)
	if err == nil || !strings.Contains(err.Error(), "no launch record") {
		t.Fatalf("Compute = %v, want the missing launch record", err)
	}
}

// The curve is in Result.Excluded and so in the exclusion set hash from the first Epoch,
// whatever the ExcludedAppended stream says.
func TestCompute_TheCurveIsInEveryEpochsExcludedSet(t *testing.T) {
	t.Parallel()

	c := loadCase(t, filepath.Join(caseDir, "funding-plus-carry.json"))
	node, dep, _ := chainOf(t, c)

	eng := newEngine(t, node, dep)
	result, err := eng.Compute(context.Background(), c.EpochID)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(result.Excluded, caseCurve) {
		t.Errorf("Excluded = %v, lacks the curve %s", result.Excluded, caseCurve)
	}
	without := slices.DeleteFunc(slices.Clone(result.Excluded), func(a chain.Address) bool { return a == caseCurve })
	if chain.ExcludedSet(without).Hash() == result.Excluded.Hash() {
		t.Error("the exclusion set hash does not see the curve")
	}
}
