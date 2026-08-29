package arca

import "testing"

// Golden vectors from sdk/typescript/src/fixtures/cosign-vectors.json, the
// cross-SDK contract for the co-signed OperatorAction wire format. That
// fixture is pinned against the authoritative kernel by
// backend/contracts/test/v7/CosignVectors.t.sol, so agreeing with it here is
// agreeing with the chain.
//
// A failure means one of two very different things. If the fixture was
// regenerated, these constants are stale — update them. If it was not, this
// SDK derives a digest the kernel would reject, and every signature it
// verifies is worthless.
const (
	vecChainID       = int64(998)
	vecKernel        = "0x1111111111111111111111111111111111111111"
	vecDomainSep     = "0xce2ffc6a26f978eacc195d1d9872aff6655c30e93d4a50ae143c2c7332a92d77"
	vecTypehash      = "0x72c47eb99437aa8c5d7633e14eb25453c9518b3cb628a9db653ca36935b792ea"
	vecSignerKey     = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
	vecSignerAddress = "0x70997970C51812dc3A010C7d01b50e0d17dc79C8"

	vecHopFromVenue        = "0x000000000000000000000000000000000000beef"
	vecHopBoundary         = "0x00000000000000000000000000000000000000000000000000000000000000b0"
	vecHopToBoundary       = "0x00000000000000000000000000000000000000000000000000000000000000b1"
	vecHopToVenue          = "0x000000000000000000000000000000000000feed"
	vecHopToVenueAccountID = "0x0000000000000000000000000000000000000000000000000000000000000009"
	vecHopToken            = "0xb88339CB7199b77E23DB6E890353E22632Ba630f"
	vecHopAmount           = "75000000"
	vecHopRef              = "0xcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	vecHopParamsHash       = "0x760217cbdcb92a92a352c5d3253ea65800224dc1ba88ed2cf4aeafe54a735037"

	vecHopNonce     = "12"
	vecHopDeadline  = int64(1900000000)
	vecHopDigest    = "0x684ecc5abcf75bf9258a26a74095027b1be24568ae4d9dd5faa545382ae68581"
	vecHopSignature = "0x6bbfd607b7a81a7107c026f3e45642fd178e0ee0fa3eb13f2e625fe91d218ef1" +
		"6d1dee1165a9d2474ac8085b24f56a0203148d7c93b979ba712ece7939127b0f1c"

	vecPreAuthNonce  = "13"
	vecPreAuthDigest = "0x70be367f6c3880a63b8e89d845af1899f4f71e7050886c8f7b8caad1025b49cc"
)

func goldenHopParams() TransferBetweenVenuesParams {
	return TransferBetweenVenuesParams{
		FromVenue:        vecHopFromVenue,
		FromBoundary:     vecHopBoundary,
		ToBoundary:       vecHopToBoundary,
		ToVenue:          vecHopToVenue,
		ToVenueAccountID: vecHopToVenueAccountID,
		Token:            vecHopToken,
		Amount:           vecHopAmount,
		Ref:              vecHopRef,
	}
}

func goldenProposal() VenueHopProposal {
	return VenueHopProposal{
		Action:            uint8(CosignActionTransferBetweenVenues),
		BoundaryKey:       vecHopBoundary,
		TargetBoundaryKey: vecHopToBoundary,
		FromVenue:         vecHopFromVenue,
		ToVenue:           vecHopToVenue,
		ToVenueAccountKey: vecHopToVenueAccountID,
		Token:             vecHopToken,
		Domain: CosignDomain{
			Name:              CosignDomainName,
			Version:           CosignDomainVersion,
			ChainID:           vecChainID,
			VerifyingContract: vecKernel,
		},
		Amount:     "75",
		AmountRaw:  vecHopAmount,
		Ref:        vecHopRef,
		Nonce:      vecHopNonce,
		Deadline:   vecHopDeadline,
		ParamsHash: vecHopParamsHash,
		Digest:     vecHopDigest,
	}
}

func TestOperatorActionTypehash_MatchesGoldenVector(t *testing.T) {
	if got := OperatorActionTypehash(); !hexEqual(got, vecTypehash) {
		t.Fatalf("OperatorActionTypehash() = %s, want %s", got, vecTypehash)
	}
}

func TestCosignDomainSeparator_MatchesGoldenVector(t *testing.T) {
	got, err := CosignDomainSeparator(vecChainID, vecKernel)
	if err != nil {
		t.Fatalf("CosignDomainSeparator: %v", err)
	}
	if !hexEqual(got, vecDomainSep) {
		t.Fatalf("CosignDomainSeparator = %s, want %s", got, vecDomainSep)
	}
}

func TestTransferBetweenVenuesParamsHash_MatchesGoldenVector(t *testing.T) {
	got, err := TransferBetweenVenuesParamsHash(goldenHopParams())
	if err != nil {
		t.Fatalf("TransferBetweenVenuesParamsHash: %v", err)
	}
	if !hexEqual(got, vecHopParamsHash) {
		t.Fatalf("paramsHash = %s, want %s", got, vecHopParamsHash)
	}
}

// The exact and capped forms share one preimage; only the action
// discriminator inside the EIP-712 digest separates them. If that ever
// stopped being true, a signature authorizing a $75 ceiling would also
// authorize an exact $75 move (and vice versa).
func TestTransferBetweenVenues_ExactAndPreAuthShareParamsHashButNotDigest(t *testing.T) {
	paramsHash, err := TransferBetweenVenuesParamsHash(goldenHopParams())
	if err != nil {
		t.Fatalf("paramsHash: %v", err)
	}

	exact, err := OperatorActionDigest(OperatorAction{
		ChainID: vecChainID, KernelAddress: vecKernel,
		Action: CosignActionTransferBetweenVenues, Boundary: vecHopBoundary,
		ParamsHash: paramsHash, Nonce: vecHopNonce, Deadline: vecHopDeadline,
	})
	if err != nil {
		t.Fatalf("exact digest: %v", err)
	}
	if !hexEqual(exact, vecHopDigest) {
		t.Fatalf("exact digest = %s, want %s", exact, vecHopDigest)
	}

	preAuth, err := OperatorActionDigest(OperatorAction{
		ChainID: vecChainID, KernelAddress: vecKernel,
		Action: CosignActionTransferBetweenVenuesPreAuth, Boundary: vecHopBoundary,
		ParamsHash: paramsHash, Nonce: vecPreAuthNonce, Deadline: vecHopDeadline,
	})
	if err != nil {
		t.Fatalf("pre-auth digest: %v", err)
	}
	if !hexEqual(preAuth, vecPreAuthDigest) {
		t.Fatalf("pre-auth digest = %s, want %s", preAuth, vecPreAuthDigest)
	}
	if hexEqual(exact, preAuth) {
		t.Fatal("exact and pre-auth digests are equal; the action discriminator is not bound")
	}
}

func TestSignCosignDigest_MatchesGoldenVector(t *testing.T) {
	got, err := SignCosignDigest(vecSignerKey, vecHopDigest)
	if err != nil {
		t.Fatalf("SignCosignDigest: %v", err)
	}
	if !hexEqual(got, vecHopSignature) {
		t.Fatalf("signature = %s, want %s", got, vecHopSignature)
	}
}

func TestSignVenueHop_VerifiesBeforeSigning(t *testing.T) {
	got, err := SignVenueHop(vecSignerKey, goldenProposal())
	if err != nil {
		t.Fatalf("SignVenueHop: %v", err)
	}
	if !hexEqual(got, vecHopSignature) {
		t.Fatalf("signature = %s, want %s", got, vecHopSignature)
	}

	tampered := goldenProposal()
	tampered.ToVenue = "0x000000000000000000000000000000000000dead"
	if _, err := SignVenueHop(vecSignerKey, tampered); err == nil {
		t.Fatal("SignVenueHop signed a proposal whose destination did not match its paramsHash")
	}
}

func TestVenueHopProposal_Verify_AcceptsTheGoldenProposal(t *testing.T) {
	if err := goldenProposal().Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// Every field of the preimage must move the paramsHash. A field that does not
// is a field an attacker can change after the signature is collected.
func TestTransferBetweenVenuesParamsHash_BindsEveryField(t *testing.T) {
	base, err := TransferBetweenVenuesParamsHash(goldenHopParams())
	if err != nil {
		t.Fatalf("base: %v", err)
	}

	mutations := map[string]func(*TransferBetweenVenuesParams){
		"fromVenue":        func(p *TransferBetweenVenuesParams) { p.FromVenue = "0x000000000000000000000000000000000000bee0" },
		"fromBoundary":     func(p *TransferBetweenVenuesParams) { p.FromBoundary = vecHopToBoundary },
		"toBoundary":       func(p *TransferBetweenVenuesParams) { p.ToBoundary = vecHopBoundary },
		"toVenue":          func(p *TransferBetweenVenuesParams) { p.ToVenue = "0x000000000000000000000000000000000000fee0" },
		"toVenueAccountId": func(p *TransferBetweenVenuesParams) { p.ToVenueAccountID = vecHopRef },
		"token":            func(p *TransferBetweenVenuesParams) { p.Token = vecKernel },
		"amount":           func(p *TransferBetweenVenuesParams) { p.Amount = "75000001" },
		"ref":              func(p *TransferBetweenVenuesParams) { p.Ref = vecHopToVenueAccountID },
	}
	for field, mutate := range mutations {
		params := goldenHopParams()
		mutate(&params)
		got, err := TransferBetweenVenuesParamsHash(params)
		if err != nil {
			t.Fatalf("%s: %v", field, err)
		}
		if hexEqual(got, base) {
			t.Fatalf("changing %s left the paramsHash unchanged; it is not bound into the signature", field)
		}
	}
}

// A proposal is only verifiable against a domain this SDK knows how to hash
// for. Silently accepting an unknown one would turn Verify into decoration.
func TestVenueHopProposal_Verify_RefusesAnUnknownDomain(t *testing.T) {
	p := goldenProposal()
	p.Domain.Version = "3"
	if err := p.Verify(); err == nil {
		t.Fatal("Verify accepted a proposal for an EIP-712 domain version it cannot derive")
	}
}

func TestVenueHopProposal_Verify_CatchesATamperedDigest(t *testing.T) {
	p := goldenProposal()
	p.Digest = vecPreAuthDigest
	if err := p.Verify(); err == nil {
		t.Fatal("Verify accepted a digest that does not match the proposal's own parameters")
	}
}

func TestVenueHopProposal_Verify_CatchesARaisedAmount(t *testing.T) {
	// The paramsHash and digest are the server's originals; only the amount
	// the caller is being shown has moved. This is the attack Verify exists
	// to catch: a signature for more than the user was told.
	p := goldenProposal()
	p.AmountRaw = "750000000"
	if err := p.Verify(); err == nil {
		t.Fatal("Verify accepted an amount that does not hash to the proposal's paramsHash")
	}
}
