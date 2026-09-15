package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/nutzwtf/nutz-verify/internal/alloc"
	"github.com/nutzwtf/nutz-verify/internal/chain"
	"github.com/nutzwtf/nutz-verify/internal/report"
	"github.com/nutzwtf/nutz-verify/internal/twab"
)

// The files of a published epochs/<id>/ bundle this compares (engineering spec §4.5).
// input.json and tree.json are not read: the first would be an input, and the second is
// implied by the Root.
const (
	allocationsFile = "allocations.json"
	rootFile        = "root.txt"
)

// artifactRow is one published Allocation. Spec §4.5 writes it as the tuple
// [account, amounts[5], twab, multBps]; the Cases write the same four fields as an
// object. Both are accepted, and every integer may be a decimal string or a number.
type artifactRow struct {
	Account chain.Address
	Amounts chain.Amounts
	TWAB    *big.Int
	MultBps int64
}

func (r *artifactRow) UnmarshalJSON(data []byte) error {
	var (
		account string
		amounts [chain.TokenCount]decimal
		twab    decimal
		multBps decimal
	)

	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("[")) {
		tuple := []any{&account, &amounts, &twab, &multBps}
		if err := json.Unmarshal(data, &tuple); err != nil {
			return err
		}
		if len(tuple) != 4 {
			return fmt.Errorf("a row has %d elements, and [account, amounts, twab, multBps] is 4", len(tuple))
		}
	} else {
		var object struct {
			Account string                    `json:"account"`
			Amounts [chain.TokenCount]decimal `json:"amounts"`
			TWAB    decimal                   `json:"twab"`
			MultBps decimal                   `json:"multBps"`
		}
		if err := json.Unmarshal(data, &object); err != nil {
			return err
		}
		account, amounts, twab, multBps = object.Account, object.Amounts, object.TWAB, object.MultBps
	}

	parsed, err := chain.ParseAddress(account)
	if err != nil {
		return fmt.Errorf("account %q %w", account, err)
	}

	r.Account = parsed
	for t := range amounts {
		r.Amounts[t] = amounts[t].value()
	}
	r.TWAB = twab.value()
	if !multBps.value().IsInt64() {
		return fmt.Errorf("multBps %s is not a bps value", multBps.value())
	}
	r.MultBps = multBps.value().Int64()

	return nil
}

// decimal is an integer written as a JSON string or, tolerated, a JSON number.
type decimal struct{ n *big.Int }

func (d *decimal) UnmarshalJSON(data []byte) error {
	text := strings.Trim(strings.TrimSpace(string(data)), `"`)

	n, ok := new(big.Int).SetString(text, 10)
	if !ok {
		return fmt.Errorf("%s is not a decimal integer", data)
	}
	d.n = n

	return nil
}

// value is the integer, zero when the field was absent. Absent is not the same as zero
// for a comparison, but a nil here would only trade a wrong diff for a panic.
func (d decimal) value() *big.Int {
	if d.n == nil {
		return new(big.Int)
	}

	return d.n
}

// compareArtifacts reads the bundle at path and reports the first row at which it
// disagrees with the Recompute, then root.txt against the recomputed Root.
//
// The bundle is loaded after the Recompute is done and handed nothing but its Result,
// which is what "structurally incapable of influencing the Recompute" means here: no
// block range, no funded totals, nothing from the bundle reaches a computation. A bundle
// that cannot be read is reported as such in its own section; it does not return an
// error, because an error would become INDETERMINATE and let a bad bundle hide a
// MISMATCH behind an exit 2.
func compareArtifacts(path string, result alloc.Result) report.Artifacts {
	out, err := diffBundle(path, result)
	if err != nil {
		return report.Artifacts{Path: path, Error: err.Error()}
	}

	return out
}

func diffBundle(path string, result alloc.Result) (report.Artifacts, error) {
	info, err := os.Stat(path)
	if err != nil {
		return report.Artifacts{}, err
	}

	dir := path
	if !info.IsDir() {
		dir = filepath.Dir(path)
	}

	rows, err := readAllocations(filepath.Join(dir, allocationsFile))
	if err != nil {
		return report.Artifacts{}, err
	}

	out := report.Artifacts{Path: dir, Rows: len(rows), Diff: diffRows(rows, result.Allocations)}

	root, present, err := readRoot(filepath.Join(dir, rootFile))
	if err != nil {
		return report.Artifacts{}, err
	}
	if present {
		out.Root = root.String()
		if out.Diff == "" {
			out.Diff = diffRoot(root, result)
		}
	}

	return out, nil
}

func readAllocations(path string) ([]artifactRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rows []artifactRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	return rows, nil
}

// readRoot reads root.txt, which is optional: a bundle without one is diffed by rows alone.
func readRoot(path string) (chain.Hash, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return chain.Hash{}, false, nil
	}
	if err != nil {
		return chain.Hash{}, false, err
	}

	text := strings.TrimSpace(string(data))
	raw, err := hex.DecodeString(strings.TrimPrefix(text, "0x"))
	if err != nil || len(raw) != len(chain.Hash{}) {
		return chain.Hash{}, false, fmt.Errorf("%s: %q is not a 32-byte hex hash", path, text)
	}

	var h chain.Hash
	copy(h[:], raw)

	return h, true, nil
}

// diffRows is the first difference between the published rows and the recomputed
// Allocations, both ascending by address, or "" when they agree.
func diffRows(published []artifactRow, recomputed []alloc.Allocation) string {
	for i := range max(len(published), len(recomputed)) {
		if i >= len(published) {
			return fmt.Sprintf("row %d: the bundle ends after %d rows, and the Recompute has %d (next is %s)",
				i, len(published), len(recomputed), chain.Address(recomputed[i].Account))
		}
		if i >= len(recomputed) {
			return fmt.Sprintf("row %d (%s): the Recompute ends after %d rows, and the bundle has %d",
				i, published[i].Account, len(recomputed), len(published))
		}

		p, r := published[i], recomputed[i]
		if twab.Address(p.Account) != r.Account {
			return fmt.Sprintf("row %d: published account %s, recomputed %s", i, p.Account, chain.Address(r.Account))
		}
		for t := range p.Amounts {
			if p.Amounts[t].Cmp(r.Amounts[t]) != 0 {
				return fmt.Sprintf("row %d (%s): amounts[%d] published %s, recomputed %s",
					i, p.Account, t, p.Amounts[t], r.Amounts[t])
			}
		}
		if p.TWAB.Cmp(r.TWAB) != 0 {
			return fmt.Sprintf("row %d (%s): twab published %s, recomputed %s", i, p.Account, p.TWAB, r.TWAB)
		}
		if p.MultBps != r.MultBps {
			return fmt.Sprintf("row %d (%s): multBps published %d, recomputed %d", i, p.Account, p.MultBps, r.MultBps)
		}
	}

	return ""
}

func diffRoot(published chain.Hash, result alloc.Result) string {
	switch {
	case !result.HasRoot:
		return fmt.Sprintf("root.txt is %s, and the Recompute posts no Root", published)
	case published != chain.Hash(result.Root):
		return fmt.Sprintf("root.txt is %s, recomputed %s", published, chain.Hash(result.Root))
	default:
		return ""
	}
}
