package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/mr-tron/base58"
	"github.com/tez-capital/tezsign/keychain"
	"github.com/tez-capital/tezsign/signer"
)

var (
	pfxBLPubkey    = []byte{6, 149, 135, 204} // "BLpk" BLS12-381 public key (48 bytes)
	pfxBLSignature = []byte{40, 171, 64, 207} // "BLsig" BLS12-381 signature (96 bytes)
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

func decodeBlsPubkey(blsPubkey string) ([]byte, error) {
	pk, err := b58CheckDecode(pfxBLPubkey, blsPubkey)
	if err != nil {
		return nil, err
	}
	if len(pk) != 48 {
		return nil, fmt.Errorf("blsPubkey payload must be 48 bytes (got %d)", len(pk))
	}
	return pk, nil
}

func decodeBlsSig(blsSig string) ([]byte, error) {
	sig, err := b58CheckDecode(pfxBLSignature, blsSig)
	if err != nil {
		return nil, err
	}
	if len(sig) != 96 {
		return nil, fmt.Errorf("blsSig payload must be 96 bytes (got %d)", len(sig))
	}
	return sig, nil
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
	pub := "BLpk1vvYoUeVyjsZhdhtzuEEsUAbigzgvZ3Ms3v4MZoeinnJKRa3MKksHZgH7nYXFxSREebWo619"

	tests := []struct {
		name, payload, edsig string
	}{
		{
			"block",
			"117a06a77000a06dd417fc89ce97287862c59ff018f096be938c81454efc8bead42633ffff40429a17460000000068ea92180466ae1df25437b553f9d772aade2115aedbcd8720ce06a0975e13bc4ac1f008320000002100000001020000000400a06dd40000000000000004ffffffff00000004000000009a033180f02da06bd0a583fbfde72695562efefba5a9801a1ce2583496a04fb749f0d48f769c5a3453f9d14b5a61b8a9964709ce1c168ddbe61fc10c2bb3c136000000009aadd15cdae80000000a",
			"BLsigAFKtcAEknDFg9VtMPNFcQumdQjLa1Sk5x34ApUog5efkpVMRSiJzqPSsvyAZ2cGirXtsE45P67BfrRFw3eDAYY1rma1jxaJLWwkvsM1Et1EQHm1Q5EQbJxR6TzVnGctJrVWGTbn7M",
		},
		{
			"preattestation",
			"127a06a77040130177ce031f1a1c769c5437509bdc3bd5dd56e7ec5cf90e2a1c24eebcd02414011200a067be0000000001af791d701cd5526bad82ccb7f540c0591b64ebb48b4bf9e73d50585caf99c6",
			"BLsigBhXXeDfcrZkkup5BDPSxXzBY3YbGqTvvxFLGPRTK98uWKDvWFdF3xn96NygygZHXmBTWXthwaF1eHsWRqnqyw7ndBDG6w843qeBMetXiAhUV8GdQEEPRXSsZ9F9MwVNAmSXvKqW2M",
		},

		{
			"attestation",
			"137a06a77007507e2c5d933e80b0e40637244461d0b383e6689a8cebc7b4b11eaed736b7bb1502a200a063ec00000000aa1524d58f2e298833cec19aaea276ebe43b4fe12a71a256bf663113c34f4509",
			"BLsigAKUDSdREKNY2R5TN76hETAG8cFmB7XXLhMSrPxkU7VYM9J6Xi86NyyFPvNfDQbtoVfdnVkvQQAcNa5pyYSy8dK8JHjLrsCbg25SSLNTqsnBNMVP2jUhcQFJ1xJAUp3Yr1sWwuMLkt",
		},
	}

	pk, err := decodeBlsPubkey(pub)
	if err != nil {
		panic(err)
	}

	for _, t := range tests {

		raw, err := hex.DecodeString(t.payload)
		if err != nil {
			fmt.Printf("%s: bad hex: %v\n", t.name, err)
			continue
		}
		sig, err := decodeBlsSig(t.edsig)
		if err != nil {
			fmt.Printf("%s: bad blsSig: %v\n", t.name, err)
			continue
		}

		// ✅ Use your decoder to validate and get canonical signBytes
		knd, lvl, rnd, signBytes, err := keychain.DecodeAndValidateSignPayload(raw)
		if err != nil {
			fmt.Printf("%s: decode error: %v\n", t.name, err)
			continue
		}

		ok := signer.VerifyCompressed(pk, sig, signBytes)

		fmt.Printf("%s [%s level=%d round=%d] → valid=%v\n",
			t.name, kindLabel(knd), lvl, rnd, ok)
	}

	pkStr := "BLpk1wujJQAn3gFNzJ3VQ7mS56nE6DcfkdRBLatiq2zVAemkYN4sR7DU24Fmj4CbQBmeQ8i5k2Wk"
	popStr := "BLsigAmcxCj51JgUUuBBRdoeaucayWVUcwKzWvNdNjs9JkQ1eHoZyBPyeiMvTBckrT3DVz5wNGNsREmSu6sxFmVEfTHzN4Xss74CU2EY3sMb4sbkfBLyQDwhEE2jbnt5VyrGRdVZs6A2U5"

	// 1) Base58Check-decode with the right prefixes, verify checksum, size
	pk, err = decodeBlsPubkey(pkStr) // expects BLpk..., 48 bytes
	if err != nil {
		panic(fmt.Errorf("pubkey decode failed: %w", err))
	}
	pop, err := decodeBlsSig(popStr) // expects BLsig..., 96 bytes
	if err != nil {
		panic(fmt.Errorf("PoP decode failed: %w", err))
	}

	// 2) Verify PoP (uses DST TEZSIGN_BLS_POP_V1 internally)
	ok := signer.VerifyPoPCompressed(pk, pop)

	fmt.Printf("PoP valid = %v\n", ok)
}
