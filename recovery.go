package arca

import (
	"encoding/hex"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	bip32 "github.com/tyler-smith/go-bip32"
	bip39 "github.com/tyler-smith/go-bip39"
	"golang.org/x/crypto/sha3"
)

// GenerateRecoveryKey generates a 12-word BIP-39 mnemonic and derives the
// Ethereum address at the standard BIP-44 path (m/44'/60'/0'/0/0). No network
// call is made; the mnemonic must be backed up by the user and is never sent to
// a server. Register the derived address with Arca.RegisterRecoveryKey.
//
// Memory-safety caveat: Go strings are immutable and garbage-collected, so the
// returned mnemonic cannot be reliably erased. For environments requiring
// guaranteed zeroization, generate the key in a secure enclave and register
// only the address.
func GenerateRecoveryKey() (GeneratedRecoveryKey, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return GeneratedRecoveryKey{}, err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return GeneratedRecoveryKey{}, err
	}
	address, err := deriveAddressFromMnemonic(mnemonic)
	if err != nil {
		return GeneratedRecoveryKey{}, err
	}
	return GeneratedRecoveryKey{Mnemonic: mnemonic, Address: address}, nil
}

// deriveAddressFromMnemonic derives the EIP-55 Ethereum address at the standard
// BIP-44 path (m/44'/60'/0'/0/0) for a BIP-39 mnemonic with an empty passphrase.
func deriveAddressFromMnemonic(mnemonic string) (string, error) {
	seed := bip39.NewSeed(mnemonic, "")
	master, err := bip32.NewMasterKey(seed)
	if err != nil {
		return "", err
	}
	path := []uint32{
		bip32.FirstHardenedChild + 44,
		bip32.FirstHardenedChild + 60,
		bip32.FirstHardenedChild + 0,
		0,
		0,
	}
	key := master
	for _, idx := range path {
		key, err = key.NewChildKey(idx)
		if err != nil {
			return "", err
		}
	}
	if len(key.Key) == 0 {
		return "", fmt.Errorf("arca: recovery key derivation produced no private key")
	}
	return privateKeyToAddress(key.Key), nil
}

func privateKeyToAddress(privKeyBytes []byte) string {
	priv := secp256k1.PrivKeyFromBytes(privKeyBytes)
	uncompressed := priv.PubKey().SerializeUncompressed() // 65 bytes: 0x04 || X || Y
	body := uncompressed[1:]
	h := keccak256(body)
	return checksumAddress(hex.EncodeToString(h[len(h)-20:]))
}

func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

// checksumAddress applies EIP-55 mixed-case checksum encoding. Input is a
// 40-char lowercase hex address (no 0x prefix).
func checksumAddress(addressHex string) string {
	hash := hex.EncodeToString(keccak256([]byte(addressHex)))
	out := []byte("0x")
	for i := 0; i < len(addressHex) && i < 40; i++ {
		c := addressHex[i]
		// uppercase the hex nibble when the corresponding hash nibble >= 8
		if hash[i] >= '8' && hash[i] <= '9' || (hash[i] >= 'a' && hash[i] <= 'f') {
			if c >= 'a' && c <= 'f' {
				c = c - 'a' + 'A'
			}
		}
		out = append(out, c)
	}
	return string(out)
}
