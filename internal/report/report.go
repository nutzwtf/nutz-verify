package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/nutzwtf/nutz-verify/chain"
)

// SchemaVersion names the shape of the --json document. It is the Signer's interface:
// the Signer parses this document to decide whether to sign, so any change to the shape —
// a renamed field, a moved one, a changed meaning — is a breaking change and moves this
// version. Adding a field is not.
const SchemaVersion = "nutz-verify-report/1"

// Report is one run of the Verifier: what it was pointed at, what it computed, and the
// Verdict. Both output formats are rendered from it.
type Report struct {
	Schema  string `json:"schema"`
	Command string `json:"command"` // epoch, latest, sync
	Run     Run    `json:"run"`

	// Verdict is the run's, and Reason is why when it is INDETERMINATE: the error that
	// stopped the check, in its own words. Empty for a sync, which has no Verdict.
	Verdict Verdict `json:"verdict,omitempty"`
	Reason  string  `json:"reason,omitempty"`

	// Epochs is the Epoch checked — one, or with --chain every rooted Epoch from deploy
	// in order, the requested one last. Empty when the run stopped before recomputing.
	Epochs []Epoch `json:"epochs,omitempty"`

	// Artifacts is the --artifacts comparison, present only when the flag was given.
	Artifacts *Artifacts `json:"artifacts,omitempty"`
}

// Run is the header: every input a Recompute has that is not derivable from the chain,
// shown rather than buried (ADR-0002), and the endpoints it trusted.
type Run struct {
	ChainID     uint64 `json:"chainId"`
	Distributor string `json:"distributor"`
	Token       string `json:"token"`
	DevWallet   string `json:"devWallet"`
	Finality    string `json:"finality"`

	// Endpoints are named by position and host, never by URL: a provider URL's path is
	// routinely an API key, and this document gets pasted into issues.
	Endpoints      []string `json:"endpoints"`
	CallsPerSecond float64  `json:"callsPerSecond"`

	Tip   *Tip   `json:"tip,omitempty"`
	Cache *Cache `json:"cache,omitempty"`
}

// Tip is the block history was read at, reconciled across endpoints. Lag is how many
// blocks the slowest endpoint was behind the fastest, which is why a recent Epoch can read
// as not yet closed for no visible reason.
type Tip struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
	Lag    uint64 `json:"lag"`
}

// Cache is what the Cache held and what this run did to it.
type Cache struct {
	Dir     string `json:"dir"`
	Records int64  `json:"records"`
	Through uint64 `json:"through"` // the newest block a held record is from; 0 when empty

	Appended int64  `json:"appended"`
	Reorg    *Reorg `json:"reorg,omitempty"`

	// Repaired is what Open had to do to the file, in cache.Repair's words. Printed so
	// that disk damage never reads as a wrong Root.
	Repaired string `json:"repaired,omitempty"`
}

// Reorg is history the chain took back and the Cache dropped before reading the new branch.
type Reorg struct {
	Block   uint64 `json:"block"`
	Dropped int64  `json:"dropped"`
}

// Epoch is one Recompute and its comparison.
type Epoch struct {
	ID     uint64 `json:"id"`
	Window Window `json:"window"`

	// EndBlock is the last block of the Epoch. Only the Epoch a run was asked about has
	// it: finding one is a binary search over headers, and the Epochs --chain walks on
	// the way there do not pay for it.
	EndBlock *Block `json:"endBlock,omitempty"`

	// Holders is how many rows the tree holds, after the omission rule.
	Holders int `json:"holders"`

	Recomputed Recomputed  `json:"recomputed"`
	Posted     *PostedRoot `json:"posted,omitempty"` // nil when no Root is posted
	Skipped    bool        `json:"skipped,omitempty"`

	Assertions []Assertion `json:"assertions,omitempty"`
	Verdict    Verdict     `json:"verdict"`
	Reason     string      `json:"reason,omitempty"`
}

// Window is the Epoch's half-open range [Start, End) in unix seconds.
type Window struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// Block is a block by number and hash.
type Block struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
}

// Recomputed is the Verifier's own answer. Every integer is a decimal string: a uint256
// does not survive a JSON number.
type Recomputed struct {
	HasRoot     bool     `json:"hasRoot"`
	Root        string   `json:"root,omitempty"`
	Totals      []string `json:"totals"`
	CarryOut    []string `json:"carryOut"`
	TotalWeight string   `json:"totalWeight"`

	// CarryIn is the Carry the Recompute was run with: the posted carryIn when a Root is
	// posted, the expected one otherwise. Assertion 4 compares the two.
	CarryIn []string `json:"carryIn"`
}

// PostedRoot is what the chain committed to.
type PostedRoot struct {
	Root    string   `json:"root"`
	Block   uint64   `json:"block"` // where RootPosted landed
	Totals  []string `json:"totals"`
	Funded  []string `json:"funded"`
	CarryIn []string `json:"carryIn"`
}

// Artifacts is the --artifacts comparison: a published bundle diffed against the
// Recompute after the fact. It never influences a Verdict — not even by failing to load,
// which is Error rather than INDETERMINATE, because a bundle that could not be read must
// not be able to hide a MISMATCH behind an exit 2. Diff is empty when the bundle agrees.
type Artifacts struct {
	Path  string `json:"path"`
	Rows  int    `json:"rows"`
	Root  string `json:"root,omitempty"` // root.txt, when present
	Diff  string `json:"diff,omitempty"`
	Error string `json:"error,omitempty"`
}

// Decimals is Amounts as decimal strings, in token order. A nil is "?": a bug made
// visible rather than a zero.
func Decimals(amounts chain.Amounts) []string {
	out := make([]string, 0, len(amounts))
	for _, a := range amounts {
		if a == nil {
			out = append(out, "?")

			continue
		}

		out = append(out, a.String())
	}

	return out
}

// WriteJSON renders the Report as the --json document, one object, newline-terminated.
func WriteJSON(w io.Writer, r Report) error {
	r.Schema = SchemaVersion

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false) // this is not HTML, and "<=" should read as "<="

	return encoder.Encode(r)
}

// Write renders the Report for a person: the header, then each Epoch with its four
// Assertion lines, then the Verdict on a line of its own at the end.
func Write(w io.Writer, r Report) {
	p := printer{w: w}
	p.header(r)

	for _, e := range r.Epochs {
		p.epoch(e)
	}

	if r.Artifacts != nil {
		p.artifacts(*r.Artifacts)
	}

	if r.Verdict == "" {
		return
	}

	p.line("")
	if r.Reason != "" {
		p.line("%s: %s", r.Verdict, r.Reason)

		return
	}

	p.line("%s", r.Verdict)
}

type printer struct {
	w io.Writer
}

func (p printer) line(format string, args ...any) {
	fmt.Fprintf(p.w, format+"\n", args...)
}

// field is one header line: a label in a fixed column, then the value.
func (p printer) field(label, format string, args ...any) {
	p.line("  %-13s%s", label, fmt.Sprintf(format, args...))
}

func (p printer) header(r Report) {
	p.line("nutz-verify %s", r.Command)
	p.field("chain", "%d", r.Run.ChainID)
	p.field("distributor", "%s", r.Run.Distributor)
	p.field("token", "%s", r.Run.Token)
	p.field("dev wallet", "%s", r.Run.DevWallet)

	if r.Run.Tip != nil {
		p.field("finality", "%s: block %d %s, endpoints within %d blocks of each other",
			r.Run.Finality, r.Run.Tip.Number, r.Run.Tip.Hash, r.Run.Tip.Lag)
	} else {
		p.field("finality", "%s", r.Run.Finality)
	}

	p.field("endpoints", "%s, at %g calls/s each", strings.Join(r.Run.Endpoints, ", "), r.Run.CallsPerSecond)

	if c := r.Run.Cache; c != nil {
		p.cache(*c)
	}
}

func (p printer) cache(c Cache) {
	held := "empty"
	if c.Records > 0 {
		held = fmt.Sprintf("%d records through block %d", c.Records, c.Through)
	}
	p.field("cache", "%s: %s", c.Dir, held)

	if c.Repaired != "" {
		p.field("", "repaired: %s", c.Repaired)
	}
	if c.Reorg != nil {
		p.field("", "reorg: dropped %d records from block %d on", c.Reorg.Dropped, c.Reorg.Block)
	}
	if c.Appended > 0 {
		p.field("", "synced: %d records appended", c.Appended)
	}
}

func (p printer) epoch(e Epoch) {
	p.line("")
	if e.EndBlock != nil {
		p.line("epoch %d  window [%d, %d)  end block %d %s", e.ID, e.Window.Start, e.Window.End, e.EndBlock.Number, e.EndBlock.Hash)
	} else {
		p.line("epoch %d  window [%d, %d)", e.ID, e.Window.Start, e.Window.End)
	}
	p.field("holders", "%d in the tree, W = %s", e.Holders, e.Recomputed.TotalWeight)

	if e.Recomputed.HasRoot {
		p.field("recomputed", "root %s  totals %s  carryOut %s", e.Recomputed.Root,
			list(e.Recomputed.Totals), list(e.Recomputed.CarryOut))
	} else {
		p.field("recomputed", "no Root: no allocation survives  carryOut %s", list(e.Recomputed.CarryOut))
	}

	switch {
	case e.Posted != nil:
		p.field("posted", "root %s at block %d  totals %s  funded %s  carryIn %s", e.Posted.Root, e.Posted.Block,
			list(e.Posted.Totals), list(e.Posted.Funded), list(e.Posted.CarryIn))
	case e.Skipped:
		p.field("posted", "no Root: skipped by a later Root")
	default:
		p.field("posted", "no Root")
	}

	for _, a := range e.Assertions {
		p.line("  %-9s%-6s%s", a.Name, status(a.Status), a.Detail)
	}

	if e.Reason != "" {
		p.field("verdict", "%s: %s", e.Verdict, e.Reason)
	}
}

func (p printer) artifacts(a Artifacts) {
	p.line("")
	if a.Error != "" {
		p.line("artifacts %s  NOT COMPARED: %s", a.Path, a.Error)

		return
	}

	root := "no root.txt"
	if a.Root != "" {
		root = "root.txt " + a.Root
	}
	p.line("artifacts %s  %d rows, %s", a.Path, a.Rows, root)

	if a.Diff == "" {
		p.field("", "agrees with the Recompute")
	} else {
		p.field("", "DIFFERS: %s", a.Diff)
	}
}

// status is how a Status reads on a terminal. FAIL in capitals so it is what the eye
// lands on in a wall of ok.
func status(s Status) string {
	switch s {
	case Pass:
		return "ok"
	case Fail:
		return "FAIL"
	default:
		return string(s)
	}
}

func list(values []string) string {
	return "[" + strings.Join(values, " ") + "]"
}
