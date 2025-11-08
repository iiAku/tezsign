package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"

	"github.com/tez-capital/tezsign/keychain"
)

// ---- Tezos Ed25519 prefixes ----
var (
	pfxEdpk  = []byte{13, 15, 37, 217}      // edpk (32)
	pfxEdsig = []byte{9, 245, 205, 134, 18} // edsig (64)
)

// ---- Base58Check (double-SHA256) ----
func doubleSHA256(b []byte) []byte {
	h1 := sha256.Sum256(b)
	h2 := sha256.Sum256(h1[:])
	return h2[:]
}
func b58CheckDecode(prefix []byte, s string) ([]byte, error) {
	decoded, err := base58.Decode(s)
	if err != nil {
		return nil, err
	}
	min := len(prefix) + 4
	if len(decoded) < min {
		return nil, fmt.Errorf("too short: %d", len(decoded))
	}
	for i := range prefix {
		if decoded[i] != prefix[i] {
			return nil, fmt.Errorf("prefix mismatch")
		}
	}
	payload := decoded[len(prefix) : len(decoded)-4]
	check := decoded[len(decoded)-4:]
	exp := doubleSHA256(append(prefix, payload...))[:4]
	for i := 0; i < 4; i++ {
		if check[i] != exp[i] {
			return nil, errors.New("bad checksum")
		}
	}
	return payload, nil
}

func decodeEdpk(edpk string) (ed25519.PublicKey, error) {
	pk, err := b58CheckDecode(pfxEdpk, edpk)
	if err != nil {
		return nil, err
	}
	if len(pk) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("edpk payload must be 32 bytes (got %d)", len(pk))
	}
	return ed25519.PublicKey(pk), nil
}

func decodeEdsig(edsig string) ([]byte, error) {
	sig, err := b58CheckDecode(pfxEdsig, edsig)
	if err != nil {
		return nil, err
	}
	if len(sig) != ed25519.SignatureSize {
		return nil, fmt.Errorf("edsig payload must be 64 bytes (got %d)", len(sig))
	}
	return sig, nil
}

// Tezos consensus signatures are over BLAKE2b-32(rawBytes)
func tezosDigest(raw []byte) []byte {
	h, _ := blake2b.New(32, nil)
	h.Write(raw)
	return h.Sum(nil)
}

func kindLabel(k keychain.SIGN_KIND) string {
	switch k {
	case keychain.BLOCK:
		return "BLOCK 0x11"
	case keychain.PREATTESTATION:
		return "PREATTESTATION 0x12"
	case keychain.ATTESTATION:
		return "ATTESTATION 0x13"
	default:
		return fmt.Sprintf("UNKNOWN 0x%02x", byte(k))
	}
}

func main() {
	// Your inputs
	pub := "edpkvV1nBq2gjQAuCHY5uNp8h2vBPvEzHWPrczySuKLkyRhwKZD4kz"

	tests := []struct {
		name, payload, edsig string
	}{
		{
			"block",
			"117a06a77000a06dd417fc89ce97287862c59ff018f096be938c81454efc8bead42633ffff40429a17460000000068ea92180466ae1df25437b553f9d772aade2115aedbcd8720ce06a0975e13bc4ac1f008320000002100000001020000000400a06dd40000000000000004ffffffff00000004000000009a033180f02da06bd0a583fbfde72695562efefba5a9801a1ce2583496a04fb749f0d48f769c5a3453f9d14b5a61b8a9964709ce1c168ddbe61fc10c2bb3c136000000009aadd15cdae80000000a",
			"edsigtr682GRbJQ7P8oUWtExRMsFpLidSzpJECRV3DmduyZqdJFtLfPfSXwog9fx5GUskPSi67EsPKYmp5SmT3ryCfH8qEZ49wt",
		},
		{
			"preattestation",
			"127a06a77040130177ce031f1a1c769c5437509bdc3bd5dd56e7ec5cf90e2a1c24eebcd02414011200a067be0000000001af791d701cd5526bad82ccb7f540c0591b64ebb48b4bf9e73d50585caf99c6",
			"edsigtw3VwcmY7eDsA9ZUspZGU3As5oZmBFRmYKgitHb57tCuw4cziGC5rSckmYT4WtTkh3fj56DAjvSL35JXnGyKZCs64BkLqr",
		},

		{
			"attestation",
			"137a06a77007507e2c5d933e80b0e40637244461d0b383e6689a8cebc7b4b11eaed736b7bb1502a200a063ec00000000aa1524d58f2e298833cec19aaea276ebe43b4fe12a71a256bf663113c34f4509",
			"edsigu4Y9iLLXwMQehizsFs5XpW9ed9M1JEk8MqU7mGLQf7gh8biCDgus9TBNPR7WRnfcRcyCcCH2RuCZa9eGexEj543GnKWcG5",
		},
	}

	pk, err := decodeEdpk(pub)
	if err != nil {
		panic(err)
	}

	for _, t := range tests {
		raw, err := hex.DecodeString(t.payload)
		if err != nil {
			fmt.Printf("%s: bad hex: %v\n", t.name, err)
			continue
		}
		sig, err := decodeEdsig(t.edsig)
		if err != nil {
			fmt.Printf("%s: bad edsig: %v\n", t.name, err)
			continue
		}

		// ✅ Use your decoder to validate and get canonical signBytes
		knd, lvl, rnd, signBytes, err := keychain.DecodeAndValidateSignPayload(raw)
		if err != nil {
			fmt.Printf("%s: decode error: %v\n", t.name, err)
			continue
		}

		digest := tezosDigest(signBytes)
		ok := ed25519.Verify(pk, digest, sig)

		fmt.Printf("%s [%s level=%d round=%d] → valid=%v\n",
			t.name, kindLabel(knd), lvl, rnd, ok)
	}
}
