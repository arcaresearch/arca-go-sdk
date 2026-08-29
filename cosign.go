package arca

import (
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

// The EIP-712 domain the v7 kernel line verifies co-signatures against. Only
// chainId and verifyingContract vary; a proposal claiming a different name or
// version is talking about a kernel this SDK does not know how to hash for,
// and is refused rather than signed.
const (
	CosignDomainName    = "ArcaCustodyKernel"
	CosignDomainVersion = "2"
)

// CosignAction is the discriminator bound into the EIP-712 digest. It is what
// stops a signature collected for one action from authorizing another.
type CosignAction uint8

const (
	CosignActionTransferBetweenVenues        CosignAction = 11
	CosignActionTransferBetweenVenuesPreAuth CosignAction = 12
)

var (
	eip712DomainTypehash = keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))
	operatorActionTypehash = keccak256([]byte(
		"OperatorAction(uint8 actionType,bytes32 boundary,bytes32 paramsHash,uint256 nonce,uint256 deadline)"))
)

// OperatorActionTypehash is the EIP-712 struct typehash, exposed for
// cross-checking against the kernel's own constant.
func OperatorActionTypehash() string { return "0x" + hex.EncodeToString(operatorActionTypehash) }

// CosignDomainSeparator computes the EIP-712 domain separator for a kernel.
//
// Equal to the kernel's `eip712DomainSeparator()` view, derived locally so a
// signer never has to trust a server's answer for it.
func CosignDomainSeparator(chainID int64, kernelAddress string) (string, error) {
	addr, err := abiAddress(kernelAddress)
	if err != nil {
		return "", fmt.Errorf("arca: domain separator: %w", err)
	}
	var buf []byte
	buf = append(buf, eip712DomainTypehash...)
	buf = append(buf, keccak256([]byte(CosignDomainName))...)
	buf = append(buf, keccak256([]byte(CosignDomainVersion))...)
	buf = append(buf, abiUint(big.NewInt(chainID))...)
	buf = append(buf, addr...)
	return "0x" + hex.EncodeToString(keccak256(buf)), nil
}

// OperatorAction is a co-signable kernel action.
type OperatorAction struct {
	// ChainID and KernelAddress fix the EIP-712 domain.
	ChainID       int64
	KernelAddress string
	Action        CosignAction
	// Boundary is the boundary id as a 32-byte hex word (the `boundaryKey`
	// a proposal returns, not the `bnd_…` id).
	Boundary string
	// ParamsHash is keccak256 over the action's ABI-encoded parameters.
	ParamsHash string
	// Nonce is decimal. Unordered on the v7 line: any unused value works,
	// and each is single-use.
	Nonce string
	// Deadline is unix seconds.
	Deadline int64
}

// OperatorActionDigest computes the EIP-712 digest a co-sign key must sign.
//
// Equal to the kernel's `hashOperatorAction(...)` view. Deriving it locally is
// the difference between signing a hop you verified and signing whatever hash
// a server handed you.
func OperatorActionDigest(a OperatorAction) (string, error) {
	boundary, err := abiBytes32(a.Boundary)
	if err != nil {
		return "", fmt.Errorf("arca: operator action digest: boundary: %w", err)
	}
	paramsHash, err := abiBytes32(a.ParamsHash)
	if err != nil {
		return "", fmt.Errorf("arca: operator action digest: paramsHash: %w", err)
	}
	nonce, err := abiDecimal(a.Nonce)
	if err != nil {
		return "", fmt.Errorf("arca: operator action digest: nonce: %w", err)
	}
	separator, err := CosignDomainSeparator(a.ChainID, a.KernelAddress)
	if err != nil {
		return "", err
	}
	ds, err := abiBytes32(separator)
	if err != nil {
		return "", err
	}

	var structBuf []byte
	structBuf = append(structBuf, operatorActionTypehash...)
	structBuf = append(structBuf, abiUint(big.NewInt(int64(a.Action)))...)
	structBuf = append(structBuf, boundary...)
	structBuf = append(structBuf, paramsHash...)
	structBuf = append(structBuf, nonce...)
	structBuf = append(structBuf, abiUint(big.NewInt(a.Deadline))...)
	structHash := keccak256(structBuf)

	var buf []byte
	buf = append(buf, 0x19, 0x01)
	buf = append(buf, ds...)
	buf = append(buf, structHash...)
	return "0x" + hex.EncodeToString(keccak256(buf)), nil
}

// TransferBetweenVenuesParams is the preimage of a venue hop's paramsHash.
//
// Every field is inside the signature. Redirecting any of them — a different
// destination venue, a different sub-account, a larger amount — produces a
// different digest, so a signature collected for one hop cannot move value
// anywhere else.
type TransferBetweenVenuesParams struct {
	// FromVenue and ToVenue are venue CONTRACT addresses, not arca paths.
	FromVenue string
	// FromBoundary is the debited boundary (the one that signs), ToBoundary
	// the credited one. Equal for a same-boundary rebalance.
	FromBoundary string
	ToBoundary   string
	ToVenue      string
	// ToVenueAccountID is the destination venue sub-account, a 32-byte word.
	ToVenueAccountID string
	Token            string
	// Amount is the uint256 the kernel moves, in token base units — the
	// proposal's `amountRaw`, NOT its human-decimal `amount`. For the
	// pre-auth action this is the ceiling rather than an exact amount; the
	// preimage is identical either way, and the action discriminator in the
	// digest is what separates them.
	Amount string
	// Ref is the correlation word, derived from the realm and operation path.
	Ref string
}

// TransferBetweenVenuesParamsHash computes the venue hop's paramsHash.
//
// Matches the kernel's `keccak256(abi.encode(...))` over the same fields, and
// is shared by the exact (action 11) and capped pre-auth (action 12) forms.
func TransferBetweenVenuesParamsHash(p TransferBetweenVenuesParams) (string, error) {
	fromVenue, err := abiAddress(p.FromVenue)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: fromVenue: %w", err)
	}
	fromBoundary, err := abiBytes32(p.FromBoundary)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: fromBoundary: %w", err)
	}
	toBoundary, err := abiBytes32(p.ToBoundary)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: toBoundary: %w", err)
	}
	toVenue, err := abiAddress(p.ToVenue)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: toVenue: %w", err)
	}
	toVenueAccount, err := abiBytes32(p.ToVenueAccountID)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: toVenueAccountId: %w", err)
	}
	token, err := abiAddress(p.Token)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: token: %w", err)
	}
	amount, err := abiDecimal(p.Amount)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: amount: %w", err)
	}
	ref, err := abiBytes32(p.Ref)
	if err != nil {
		return "", fmt.Errorf("arca: hop paramsHash: ref: %w", err)
	}

	var buf []byte
	for _, word := range [][]byte{
		fromVenue, fromBoundary, toBoundary, toVenue, toVenueAccount, token, amount, ref,
	} {
		buf = append(buf, word...)
	}
	return "0x" + hex.EncodeToString(keccak256(buf)), nil
}

// Verify re-derives the proposal's paramsHash and digest from its own
// semantic fields and reports any disagreement with the values the server
// returned.
//
// This is what makes a co-signature meaningful. Without it a signer is
// attesting to a 32-byte number it cannot read; with it, a server that
// returned a digest for a different destination, a different amount, or a
// different kernel is caught before the key is ever used.
//
// HopVenues calls this automatically before invoking its signer, so the
// common path is verified by default. Call it directly when you drive
// ProposeVenueHop yourself.
func (p VenueHopProposal) Verify() error {
	if p.Domain.Name != CosignDomainName || p.Domain.Version != CosignDomainVersion {
		return fmt.Errorf(
			"arca: venue hop proposal is for EIP-712 domain %q version %q, but this SDK derives %q version %q — "+
				"the kernel's signing contract has moved and this SDK cannot verify what it would be signing",
			p.Domain.Name, p.Domain.Version, CosignDomainName, CosignDomainVersion)
	}
	if p.Action != uint8(CosignActionTransferBetweenVenues) &&
		p.Action != uint8(CosignActionTransferBetweenVenuesPreAuth) {
		return fmt.Errorf("arca: venue hop proposal carries action %d, want %d or %d",
			p.Action, CosignActionTransferBetweenVenues, CosignActionTransferBetweenVenuesPreAuth)
	}

	paramsHash, err := TransferBetweenVenuesParamsHash(TransferBetweenVenuesParams{
		FromVenue:        p.FromVenue,
		FromBoundary:     p.BoundaryKey,
		ToBoundary:       p.TargetBoundaryKey,
		ToVenue:          p.ToVenue,
		ToVenueAccountID: p.ToVenueAccountKey,
		Token:            p.Token,
		Amount:           p.AmountRaw,
		Ref:              p.Ref,
	})
	if err != nil {
		return err
	}
	if !hexEqual(paramsHash, p.ParamsHash) {
		return fmt.Errorf(
			"arca: venue hop paramsHash mismatch: server returned %s, the returned parameters hash to %s — "+
				"do not sign this proposal", p.ParamsHash, paramsHash)
	}

	digest, err := OperatorActionDigest(OperatorAction{
		ChainID:       p.Domain.ChainID,
		KernelAddress: p.Domain.VerifyingContract,
		Action:        CosignAction(p.Action),
		Boundary:      p.BoundaryKey,
		ParamsHash:    paramsHash,
		Nonce:         p.Nonce,
		Deadline:      p.Deadline,
	})
	if err != nil {
		return err
	}
	if !hexEqual(digest, p.Digest) {
		return fmt.Errorf(
			"arca: venue hop digest mismatch: server returned %s, the returned parameters digest to %s — "+
				"do not sign this proposal", p.Digest, digest)
	}
	return nil
}

// SignCosignDigest signs a 32-byte EIP-712 digest with a secp256k1 co-sign
// key, returning the 0x-prefixed 65-byte [r ‖ s ‖ v] signature (v ∈ {27,28})
// that `ecrecover` expects. The signature is canonical low-s, satisfying EIP-2.
//
// Present because a Go caller is usually a server that already holds the key.
// Where the key lives in an HSM or on a user's device, sign the digest there
// and hand the result to HopVenues instead — the SDK never needs to see it.
func SignCosignDigest(privateKey string, digest string) (string, error) {
	keyBytes, err := decodeHex(privateKey)
	if err != nil {
		return "", fmt.Errorf("arca: sign digest: private key: %w", err)
	}
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("arca: sign digest: private key must be 32 bytes, got %d", len(keyBytes))
	}
	digestBytes, err := abiBytes32(digest)
	if err != nil {
		return "", fmt.Errorf("arca: sign digest: %w", err)
	}

	priv := secp256k1.PrivKeyFromBytes(keyBytes)
	// Compact layout is [v ‖ r ‖ s] with v = 27 + recovery id for an
	// uncompressed key; Ethereum wants the same bytes as [r ‖ s ‖ v].
	compact := ecdsa.SignCompact(priv, digestBytes, false)
	if len(compact) != 65 {
		return "", fmt.Errorf("arca: sign digest: unexpected signature length %d", len(compact))
	}
	out := make([]byte, 65)
	copy(out[0:64], compact[1:65])
	out[64] = compact[0]
	return "0x" + hex.EncodeToString(out), nil
}

// SignVenueHop verifies a proposal and signs its digest, the two halves that
// belong together. Suitable as a HopVenues signer when the key is in process:
//
//	Sign: func(ctx context.Context, digest string, p arca.VenueHopProposal) (string, error) {
//	    return arca.SignVenueHop(key, p)
//	}
func SignVenueHop(privateKey string, p VenueHopProposal) (string, error) {
	if err := p.Verify(); err != nil {
		return "", err
	}
	return SignCosignDigest(privateKey, p.Digest)
}

// --- ABI word encoding ---

func abiUint(v *big.Int) []byte {
	out := make([]byte, 32)
	v.FillBytes(out)
	return out
}

func abiDecimal(s string) ([]byte, error) {
	v, ok := new(big.Int).SetString(strings.TrimSpace(s), 10)
	if !ok {
		return nil, fmt.Errorf("%q is not a decimal integer", s)
	}
	if v.Sign() < 0 {
		return nil, fmt.Errorf("%q is negative", s)
	}
	if v.BitLen() > 256 {
		return nil, fmt.Errorf("%q overflows uint256", s)
	}
	return abiUint(v), nil
}

func abiAddress(s string) ([]byte, error) {
	raw, err := decodeHex(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != 20 {
		return nil, fmt.Errorf("address must be 20 bytes, got %d", len(raw))
	}
	out := make([]byte, 32)
	copy(out[12:], raw)
	return out, nil
}

func abiBytes32(s string) ([]byte, error) {
	raw, err := decodeHex(s)
	if err != nil {
		return nil, err
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("value must be 32 bytes, got %d", len(raw))
	}
	return raw, nil
}

func decodeHex(s string) ([]byte, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	if trimmed == "" {
		return nil, fmt.Errorf("empty hex value")
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid hex %q", s)
	}
	return raw, nil
}

func hexEqual(a, b string) bool {
	return strings.EqualFold(
		strings.TrimPrefix(strings.TrimPrefix(a, "0x"), "0X"),
		strings.TrimPrefix(strings.TrimPrefix(b, "0x"), "0X"),
	)
}
