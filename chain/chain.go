// Package chain reads everything a Recompute needs from Ethereum JSON-RPC: NUTZ Transfer
// logs, block headers, the Distributor's ledger and Root, and the ExcludedAppended stream
// the Excluded set is rebuilt from.
//
// It is the only package that talks to a node, and it hand-rolls both the JSON-RPC and the
// ABI decoding on net/http and encoding/json (ADR-0004). That is affordable because the
// surface is four shapes fixed at compile time — Transfer (2 indexed + 1 data word),
// RootPosted (2 indexed + 11 static data words), ExcludedAppended (1 indexed), and the one
// dynamic return, excluded() -> address[] — plus ledger() and block headers. A general ABI
// decoder would be a runtime decoder for arbitrary contracts, paid for six times.
//
// Owning the decoding means owning the decoding bugs, so every shape here is asserted
// against a real node by the anvil harness in anvil_test.go rather than against a second
// implementation of our own.
//
// # Every error here is INDETERMINATE
//
// ADR-0002 makes Verdicts three-valued and "2 is not 0": the warm Signer signs only on
// MATCH, so a read that could not be completed must never look like a read that agreed.
// Nothing in this package returns a partial or best-effort answer — an endpoint that errors,
// a response that does not decode, two endpoints that disagree and an Epoch that has not
// closed at the requested finality are all errors, and the caller reports exit 2 for them.
package chain

import (
	"fmt"
	"math/big"
)

// TokenCount is the number of reward tokens a ledger carries: SPY, NVDA, MU, SPCX, USDG.
const TokenCount = 5

// Amounts is one value per reward token, in each token's own decimals.
type Amounts [TokenCount]*big.Int

// Address is a 20-byte Ethereum address.
type Address [20]byte

// Hash is a 32-byte keccak-256 digest: a block hash, a Root, or a topic.
type Hash [32]byte

// Kind is the Distributor's ledger kind. The two share a code path and separate books.
type Kind uint8

// The ledger kinds, matching the contract's enum ordinals — they are what the RootPosted
// topic carries and what a ledger() call is keyed by.
const (
	KindEpoch Kind = 0
	KindDraw  Kind = 1
)

func (k Kind) String() string {
	switch k {
	case KindEpoch:
		return "epoch"
	case KindDraw:
		return "draw"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// Block is the part of a block header a Recompute needs. ParentHash is carried for the
// Cache's reorg detection (spec §9) rather than for anything here.
type Block struct {
	Number     uint64
	Hash       Hash
	ParentHash Hash
	Timestamp  int64
}

// Site is where a log sits on chain: which block, where in that block, and when the block
// was. Every event below embeds one, so a caller reads t.BlockNumber as before.
//
// Timestamp is the odd one out: it is not in the log. A log carries no time of its own and
// the rules weigh Holders by seconds, so the Reader fills it in from the block header once
// the logs are decoded. Keeping the four together is also what lets one helper stamp all
// three event types, and it is the shape ticket 04's Cache record has.
type Site struct {
	BlockNumber uint64
	BlockHash   Hash
	LogIndex    uint64
	Timestamp   int64
}

// Transfer is one NUTZ Transfer(from, to, value) log.
type Transfer struct {
	Site
	From  Address
	To    Address
	Value *big.Int
}

// Exclusion is one ExcludedAppended(account) log: an address whose NUTZ balance counts as
// zero for every rule, from the Epoch containing this block onward.
type Exclusion struct {
	Site
	Account Address
}

// RootPosted is one RootPosted(kind, id, root, totals, carryIn) log. It is the only public
// source of carryIn — ledger() reports funded, totals and the Root, but the Carry a Root was
// computed against is not stored per period, so Assertion 4 of ADR-0002 reads it from here.
type RootPosted struct {
	Site
	Kind    Kind
	ID      uint64
	Root    Hash
	Totals  Amounts
	CarryIn Amounts
}

// Ledger is the Distributor's book for one Epoch or Draw, as ledger(kind, id) returns it.
// RootPostedAt is zero exactly when no Root is posted, including after a Void.
type Ledger struct {
	Root         Hash
	RootPostedAt int64
	Skipped      bool
	Funded       Amounts
	Totals       Amounts
	Claimed      Amounts
}

// HasRoot reports whether a Root stands for this period. The contract uses rootPostedAt as
// the sentinel rather than a zero Root, because a Void clears the timestamp.
func (l Ledger) HasRoot() bool {
	return l.RootPostedAt != 0
}
