package chain

import (
	"errors"
	"fmt"
	"math"
	"math/big"
)

// selectorSize is the four leading calldata bytes that name a function.
const selectorSize = 4

// The two view functions the Verifier calls. Like the topic0 hashes these are derived from
// the signature rather than written as literals, and for the same reason: a selector built
// from a drifted signature does not fail, it hits the fallback and returns empty data.
var (
	selectorLedger           = selectorOf("ledger(uint8,uint256)")
	selectorExcluded         = selectorOf("excluded()")
	selectorGetLaunchedToken = selectorOf("getLaunchedToken(address)")
)

func selectorOf(signature string) [selectorSize]byte {
	var out [selectorSize]byte
	digest := keccak256([]byte(signature))
	copy(out[:], digest[:selectorSize])

	return out
}

// encodeLedgerCall writes ledger(Kind, uint256) calldata. Both arguments are static, so this
// is the selector and two words.
func encodeLedgerCall(kind Kind, id uint64) []byte {
	out := make([]byte, selectorSize+2*wordSize)
	copy(out, selectorLedger[:])
	out[selectorSize+wordSize-1] = byte(kind)
	new(big.Int).SetUint64(id).FillBytes(out[selectorSize+wordSize:])

	return out
}

// ledgerWords is what ledger() returns: root, rootPostedAt, skipped, funded[5], totals[5],
// claimed[5].
//
// Every component of the Ledger struct is static, so the tuple is static: it comes back
// inline, in declaration order, with no head offset and no dynamic tail. That is the whole
// reason this decoder can be twenty lines instead of a general one.
const ledgerWords = 3 + 3*TokenCount

// decodeLedger reads the Distributor's book for one period.
func decodeLedger(data []byte) (Ledger, error) {
	if len(data) != ledgerWords*wordSize {
		// eth_call against an address with no code returns empty data, and an all-zero
		// Ledger decoded from it reads as "funded nothing, posted no Root" — a confident
		// answer about a Distributor we never reached.
		return Ledger{}, fmt.Errorf("chain: ledger(): returned %d bytes, and the Ledger struct is %d",
			len(data), ledgerWords*wordSize)
	}

	skipped, err := wordBool(dataWord(data, 2))
	if err != nil {
		return Ledger{}, fmt.Errorf("chain: ledger(): skipped %w", err)
	}

	postedAt, err := wordUint64(dataWord(data, 1))
	if err != nil || postedAt > math.MaxInt64 {
		return Ledger{}, fmt.Errorf("chain: ledger(): rootPostedAt is %#x, which is not a unix second",
			dataWord(data, 1))
	}

	ledger := Ledger{RootPostedAt: int64(postedAt), Skipped: skipped}
	copy(ledger.Root[:], dataWord(data, 0))

	for i := range ledger.Funded {
		ledger.Funded[i] = wordUint256(dataWord(data, 3+i))
		ledger.Totals[i] = wordUint256(dataWord(data, 3+TokenCount+i))
		ledger.Claimed[i] = wordUint256(dataWord(data, 3+2*TokenCount+i))
	}

	return ledger, nil
}

// encodeGetLaunchedToken is the Pons factory's getLaunchedToken(token) call.
func encodeGetLaunchedToken(token Address) []byte {
	out := make([]byte, selectorSize+wordSize)
	copy(out, selectorGetLaunchedToken[:])
	copy(out[selectorSize+wordSize-addressSize:], token[:])

	return out
}

// launchedTokenWords is IPonsV2LaunchFactory.LaunchedToken: fifteen static fields, so the
// struct returns as fifteen words in ABI order with no offsets. The Verifier reads two of
// them: curve, the second, and exists, the last.
const launchedTokenWords = 15

// decodeLaunchedToken reads the factory's launch record for the Token and returns its
// bonding curve. A record with exists == false, or one whose curve is the zero address, is an
// error and never an empty answer: it means the pinned Token was not launched through this
// factory, and an Engine that shrugged would count the curve's supply as a Holder's.
func decodeLaunchedToken(data []byte) (Address, error) {
	if len(data) != launchedTokenWords*wordSize {
		return Address{}, fmt.Errorf("chain: getLaunchedToken(): returned %d bytes, and the LaunchedToken struct is %d",
			len(data), launchedTokenWords*wordSize)
	}

	exists, err := wordBool(dataWord(data, launchedTokenWords-1))
	if err != nil {
		return Address{}, fmt.Errorf("chain: getLaunchedToken(): exists %w", err)
	}
	if !exists {
		return Address{}, errors.New("chain: getLaunchedToken(): the factory has no launch record for the Token; " +
			"it was not launched through this Pons factory")
	}

	curve, err := wordAddress(dataWord(data, 1))
	if err != nil {
		return Address{}, fmt.Errorf("chain: getLaunchedToken(): curve %w", err)
	}
	if curve == (Address{}) {
		return Address{}, errors.New("chain: getLaunchedToken(): the launch record names no curve")
	}

	return curve, nil
}

// decodeAddressArray reads abi.encode(address[]) — the one dynamic shape the Verifier
// decodes, and the return of excluded().
//
// A dynamic return is a head offset, a length, then the elements. The offset is checked
// rather than followed: for a single return value Solidity always writes 0x20, and an offset
// that says otherwise is a response we are not reading correctly rather than a layout to
// accommodate. Nothing here is ever asked to decode a return it did not expect.
func decodeAddressArray(data []byte) ([]Address, error) {
	const header = 2 * wordSize

	if len(data) < header {
		return nil, fmt.Errorf("chain: address[]: returned %d bytes, and the header alone is %d",
			len(data), header)
	}

	offset, err := wordUint64(dataWord(data, 0))
	if err != nil || offset != wordSize {
		return nil, fmt.Errorf("chain: address[]: head offset is %#x, and a single dynamic return is 0x20",
			dataWord(data, 0))
	}

	length, err := wordUint64(dataWord(data, 1))
	if err != nil {
		return nil, fmt.Errorf("chain: address[]: length %w", err)
	}
	// Bound the length before it is multiplied out: the word count comes off the wire, and
	// header + length*32 is exactly the product that wraps on a hostile value.
	if length > uint64((len(data)-header)/wordSize) {
		return nil, fmt.Errorf("chain: address[]: claims %d entries, and %d bytes were returned",
			length, len(data))
	}
	if want := header + int(length)*wordSize; len(data) != want {
		return nil, fmt.Errorf("chain: address[]: claims %d entries, which needs %d bytes of %d returned",
			length, want, len(data))
	}

	out := make([]Address, 0, length)
	for i := range int(length) {
		a, err := wordAddress(dataWord(data, 2+i))
		if err != nil {
			return nil, fmt.Errorf("chain: address[]: entry %d %w", i, err)
		}

		out = append(out, a)
	}

	return out, nil
}
