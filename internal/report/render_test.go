package report_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/nutzwtf/nutz-verify/chain"
	"github.com/nutzwtf/nutz-verify/internal/report"
)

// -update rewrites the golden files from the current output. Review the diff: the text
// form is what a stranger reads inside a Dispute window, and the JSON form is the Signer's
// interface, whose schema version has to move with any change to its shape.
var update = flag.Bool("update", false, "rewrite the golden files")

func hexAddr(b byte) string {
	var a chain.Address
	for i := range a {
		a[i] = b
	}

	return a.String()
}

// sample is a run over one Epoch with everything filled in, so the golden covers every
// line the renderers know how to draw.
func sample() report.Report {
	recompute, posted := agreeing()
	assessed := report.Assess(recompute, posted)

	return report.Report{
		Schema:  report.SchemaVersion,
		Command: "epoch",
		Run: report.Run{
			ChainID:        4663,
			Distributor:    hexAddr(0x22),
			Token:          hexAddr(0x11),
			DevWallet:      hexAddr(0xdd),
			Finality:       "safe",
			Endpoints:      []string{"endpoint 1 (rpc.example)", "endpoint 2 (archive.example)"},
			CallsPerSecond: 15,
			Tip:            &report.Tip{Number: 41000, Hash: chain.Hash{0x41}.String(), Lag: 3},
			Cache: &report.Cache{
				Dir:      "/home/x/.cache/nutz-verify/4663-" + hexAddr(0x11),
				Records:  12345,
				Through:  40990,
				Appended: 17,
				Reorg:    &report.Reorg{Block: 40980, Dropped: 4},
				Repaired: "a record that fails its checksum at record 12000; kept 11990 records through block 40800 and dropped 10, which the next sync refetches",
			},
		},
		Verdict: assessed.Verdict,
		Epochs: []report.Epoch{{
			ID:       1000,
			Window:   report.Window{Start: 3600000, End: 3603600},
			EndBlock: &report.Block{Number: 40990, Hash: chain.Hash{0x40}.String()},
			Holders:  2,
			Recomputed: report.Recomputed{
				HasRoot:     true,
				Root:        rootA.String(),
				Totals:      report.Decimals(recompute.Totals),
				CarryOut:    report.Decimals(amounts(375, 0, 125, 1, 2)),
				TotalWeight: "40000",
				CarryIn:     report.Decimals(posted.CarryIn),
			},
			Posted: &report.PostedRoot{
				Root:    rootA.String(),
				Block:   41003,
				Totals:  report.Decimals(posted.Totals),
				Funded:  report.Decimals(posted.Funded),
				CarryIn: report.Decimals(posted.CarryIn),
			},
			Assertions: assessed.Assertions,
			Verdict:    assessed.Verdict,
		}},
		Artifacts: &report.Artifacts{
			Path: "/srv/nutz-artifacts/epochs/1000",
			Rows: 2,
			Root: rootA.String(),
			Diff: "row 1 (" + hexAddr(0xb2) + "): amounts[2] published 120, recomputed 125",
		},
	}
}

func indeterminate() report.Report {
	r := sample()
	r.Epochs = nil
	r.Artifacts = nil
	r.Run.Cache = nil
	r.Verdict = report.Indeterminate
	r.Reason = "chain: epoch 1000 closes at 3603600, and the safe head is block 41000 at 3603000"

	return r
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the golden file:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestWrite_Golden(t *testing.T) {
	t.Parallel()

	for name, r := range map[string]report.Report{
		"epoch-match.txt":         sample(),
		"epoch-indeterminate.txt": indeterminate(),
	} {
		var buf bytes.Buffer
		report.Write(&buf, r)
		golden(t, name, buf.Bytes())
	}
}

func TestWriteJSON_Golden(t *testing.T) {
	t.Parallel()

	for name, r := range map[string]report.Report{
		"epoch-match.json":         sample(),
		"epoch-indeterminate.json": indeterminate(),
	} {
		var buf bytes.Buffer
		if err := report.WriteJSON(&buf, r); err != nil {
			t.Fatal(err)
		}
		golden(t, name, buf.Bytes())

		// The Signer parses this. Whatever else changes, the document is one JSON object
		// carrying the schema version and the verdict at the top level.
		var top struct {
			Schema  string `json:"schema"`
			Verdict string `json:"verdict"`
		}
		if err := json.Unmarshal(buf.Bytes(), &top); err != nil {
			t.Fatal(err)
		}
		if top.Schema != report.SchemaVersion || top.Verdict != string(r.Verdict) {
			t.Errorf("%s: top level = %+v", name, top)
		}
	}
}
