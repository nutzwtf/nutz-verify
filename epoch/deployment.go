package epoch

import (
	"errors"

	"github.com/nutzwtf/nutz-verify/chain"
)

// Deployment is every input a Recompute has that is not on the chain, pinned in the code
// rather than taken as a flag.
//
// ADR-0002 wants the unverifiable inputs shown rather than buried, and every run's header
// echoes these. They are constants and not flags because a flag anyone can vary is an input
// anyone can choose: a Verifier told where the Distributor is by the party whose Root it is
// checking has been told which evidence to look at. The Cache's start block is the sharpest
// case — two runs that disagree about it have different histories, and the later-starting
// one is silently missing balances (spec §9) — which is why cache.Options.Start comes from
// here and nowhere else.
type Deployment struct {
	ChainID uint64

	// Token is the NUTZ contract and Distributor the NutzDistributor. DevWallet is
	// engineering spec §3.5's DEV_WALLET, held flat at 1.00x whatever its streak; it is an
	// indexer-side constant that the contracts never see, which is why it has to be here.
	Token       chain.Address
	Distributor chain.Address
	DevWallet   chain.Address

	// PonsFactory is the Pons launch factory, whose launch record for Token names the
	// bonding curve that holds the unsold supply before graduation. The Engine excludes
	// that curve in every Epoch (ADR-0007): like the Dev wallet it is a rule the contracts
	// never see, unlike it the address is read from the chain rather than pinned here.
	PonsFactory chain.Address

	// TokenBlock is the block NUTZ was created in: where the Transfer history, and so the
	// Cache, begins. DistributorBlock is where the Distributor was deployed: where its
	// ExcludedAppended and RootPosted streams begin, and the constructor's ExcludedAppended
	// logs for the base list land there, so a read starting later would lose them.
	TokenBlock       uint64
	DistributorBlock uint64
}

// Pinned is chain 4663's deployment: what the binary the Signer runs and the indexer that
// links this module both verify against. A function and not a variable for the reason
// above — a value an importer could assign is a flag by another name.
//
// NUTZ has not launched (spec §4, §9: "NUTZ does not exist yet"), so there is nothing to
// pin yet. Every field a Recompute needs is left empty and Check refuses to run, with exit
// 2, until the launch runbook fills them in — a binary that ran against a zero address
// would report an Epoch with no transfers and no Root, confidently.
func Pinned() Deployment {
	return Deployment{
		ChainID:     4663,
		PonsFactory: ponsFactory4663,
	}
}

// ponsFactory4663 is PonsV2LaunchFactory on chain 4663 (engineering spec §2.4), the one
// address of the Deployment that exists before the Launch.
var ponsFactory4663 = chain.Address{
	0x7e, 0xd5, 0x98, 0xbc, 0xef, 0x8b, 0xd9, 0xed, 0xd8, 0xc9,
	0x7a, 0x19, 0x5c, 0x6d, 0x13, 0xf4, 0x08, 0x01, 0xec, 0x7e,
}

// ErrNotPinned is why an unpinned build cannot run. It is INDETERMINATE: no check happened.
var ErrNotPinned = errors.New("this build is not pinned to a deployment: NUTZ has not launched, " +
	"and epoch/deployment.go has no Token, Distributor and DEV_WALLET to verify against")

// Check refuses a Deployment that could not name a Distributor and token to read, or the
// factory whose launch record names the Token's curve.
func (d Deployment) Check() error {
	if d.ChainID == 0 || d.Token == (chain.Address{}) || d.Distributor == (chain.Address{}) ||
		d.DevWallet == (chain.Address{}) || d.PonsFactory == (chain.Address{}) {
		return ErrNotPinned
	}

	return nil
}
