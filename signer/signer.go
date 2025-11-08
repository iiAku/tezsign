package signer

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"github.com/mr-tron/base58"
	"golang.org/x/crypto/blake2b"

	blst "github.com/supranational/blst/bindings/go"
)

// ---- Tezos Base58 prefixes (bytes) ----
var (
	pfxBLPubkey    = []byte{6, 149, 135, 204} // "BLpk" BLS12-381 public key (48 bytes)
	pfxBLSignature = []byte{40, 171, 64, 207} // "BLsig" BLS12-381 signature (96 bytes)
	pfxTz4         = []byte{6, 161, 166}      // "tz4"  BLS12-381 public key hash (20 bytes)
	pfxBLSecretKey = []byte{3, 150, 192, 40}  // "BLsk" BLS12-381 secret key (32 bytes, LE)
)

var (
	errPubkeyNot48Bytes             = errors.New("pubkey must be 48-byte G1 compressed")
	errBadSigEncoding               = errors.New("bad signature encoding")
	errSigNot96Bytes                = errors.New("signature must be 96-byte G2 compressed")
	errBLSecretKeyTooShort          = errors.New("BLSecretKey too short")
	errBadBLskPrefix                = errors.New("bad BLsk prefix")
	errBLSecretKeyPayloadNot32Bytes = errors.New("BLSecretKey payload must be 32 bytes")
	errScalarInvalid                = errors.New("invalid scalar")
)

// ---- Domain Separation ----
var (
	// CFRG MinPk ciphersuite (Octez uses signature-in-G2 / pubkey-in-G1)
	dstMinPk = []byte("BLS_SIG_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")
	// DST for Proof of Possession
	dstTezPop = []byte("BLS_POP_BLS12381G2_XMD:SHA-256_SSWU_RO_POP_")

	signature = new(Signature)
)

// MinPk (minimal public key size) type aliases
type PublicKey = blst.P1Affine
type Signature = blst.P2Affine
type AggregateSignature = blst.P2Aggregate
type AggregatePublicKey = blst.P1Aggregate

type SecretKey = blst.SecretKey

// GenerateRandomKey -> (secretKey, pubkeyBytes[48], BLpubkey = BLpk...)
func GenerateRandomKey() (*blst.SecretKey, []byte, string) {
	var ikm [32]byte
	_, _ = rand.Read(ikm[:])

	secretKey := blst.KeyGen(ikm[:])
	pubkeyBytes, blPubkey := PublicKeyFromSecret(secretKey)

	return secretKey, pubkeyBytes, blPubkey
}

// SignCompressed -> (sigBytes[96], BLsig...)
//
//go:inline
func SignCompressed(secretKey *blst.SecretKey, msg []byte) ([]byte, string) {
	sig := signature.Sign(secretKey, msg, dstMinPk)
	sigBytes := sig.Compress() // 96 bytes
	return sigBytes, b58CheckEncode(pfxBLSignature, sigBytes)
}

// SignPoPCompressed signs the 48-byte compressed pubkey bytes using a PoP DST.
// Returns (sigBytes[96], BLsig...).
func SignPoPCompressed(secretKey *blst.SecretKey, pubkeyBytes []byte) ([]byte, string, error) {
	if len(pubkeyBytes) != blst.BLST_P1_COMPRESS_BYTES {
		return nil, "", errPubkeyNot48Bytes
	}
	sig := signature.Sign(secretKey, pubkeyBytes, dstTezPop)
	sigBytes := sig.Compress()
	return sigBytes, b58CheckEncode(pfxBLSignature, sigBytes), nil
}

// VerifyCompressed checks a single (pk, sig, msg).
func VerifyCompressed(pubkeyBytes, sigBytes, msg []byte) bool {
	var pubkey PublicKey
	if pubkey.Uncompress(pubkeyBytes) == nil {
		return false
	}
	var sig Signature
	if sig.Uncompress(sigBytes) == nil {
		return false
	}
	return sig.Verify(true, &pubkey, true, msg, dstMinPk)
}

// VerifyPoPCompressed checks PoP over pubkeyBytes.
func VerifyPoPCompressed(pubkeyBytes, popSigBytes []byte) bool {
	var pk PublicKey
	if pk.Uncompress(pubkeyBytes) == nil {
		return false
	}
	var sig Signature
	if sig.Uncompress(popSigBytes) == nil {
		return false
	}
	return sig.Verify(true, &pk, true, pubkeyBytes, dstTezPop)
}

// FastAggregateVerifyCompressed checks many pubkeys signing the same msg.
func FastAggregateVerifyCompressed(pubkeyList [][]byte, aggSigBytes, msg []byte) bool {
	pubkeys := make([]*PublicKey, 0, len(pubkeyList))
	for _, b := range pubkeyList {
		var pubkey PublicKey
		if pubkey.Uncompress(b) == nil {
			return false
		}
		pubkeyCopy := pubkey
		pubkeys = append(pubkeys, &pubkeyCopy)
	}
	var sig Signature
	if sig.Uncompress(aggSigBytes) == nil {
		return false
	}
	return sig.FastAggregateVerify(true, pubkeys, msg, dstMinPk)
}

// AggregateCompressed aggregates multiple signatures (same msg).
func AggregateCompressed(sigList [][]byte) ([]byte, error) {
	agg := new(AggregateSignature)

	tmp := make([]*Signature, 0, len(sigList))
	for _, b := range sigList {
		var sig Signature
		if sig.Uncompress(b) == nil {
			return nil, errBadSigEncoding
		}
		sigCopy := sig
		tmp = append(tmp, &sigCopy)
	}
	agg.Aggregate(tmp, true)
	return agg.ToAffine().Compress(), nil
}

// Tz4FromBLPubkeyBytes computes the tz4 address from a 48-byte G1 compressed key.
func Tz4FromBLPubkeyBytes(pubkeyBytes []byte) (string, error) {
	if len(pubkeyBytes) != blst.BLST_P1_COMPRESS_BYTES { // 48
		return "", errPubkeyNot48Bytes
	}
	// tz4 = Base58Check( pfxTz4 || blake2b-160(pkBytes) )
	h, _ := blake2b.New(20, nil)
	_, _ = h.Write(pubkeyBytes)
	return b58CheckEncode(pfxTz4, h.Sum(nil)), nil
}

func EncodeBLPubkey(pubkeyBytes []byte) (string, error) {
	if len(pubkeyBytes) != blst.BLST_P1_COMPRESS_BYTES {
		return "", errPubkeyNot48Bytes
	}
	return b58CheckEncode(pfxBLPubkey, pubkeyBytes), nil
}
func EncodeBLSignature(sigBytes []byte) (string, error) {
	if len(sigBytes) != blst.BLST_P2_COMPRESS_BYTES {
		return "", errSigNot96Bytes
	}
	return b58CheckEncode(pfxBLSignature, sigBytes), nil
}

// Base58Check(prefix || payload || doubleSHA256(prefix||payload)[0:4])
//
//go:inline
func b58CheckEncode(prefix, payload []byte) string {
	n := len(prefix) + len(payload)
	buf := make([]byte, n+4)
	copy(buf, prefix)
	copy(buf[len(prefix):], payload)

	sum1 := sha256.Sum256(buf[:n])
	sum2 := sha256.Sum256(sum1[:])
	copy(buf[n:], sum2[:4])

	return base58.Encode(buf)
}

// Export our SecretKey as BLsk (LE payload)
func EncodeBLSecretKey(secretKey *blst.SecretKey) string {
	le := secretKey.ToLEndian() // 32 bytes little-endian scalar
	return b58CheckEncode(pfxBLSecretKey, le)
}

// Import BLSecretKey -> *blst.SecretKey
func ImportBLSecretKey(blSecretKey string) (*blst.SecretKey, error) {
	raw, err := base58.Decode(blSecretKey)
	if err != nil {
		return nil, err
	}
	if len(raw) < 4+len(pfxBLSecretKey) { // prefix + payload + 4-byte checksum
		return nil, errBLSecretKeyTooShort
	}
	n := len(raw) - 4 // drop checksum; Base58Check verified by client on import
	// naive check: prefix match (optionally verify checksum yourself)
	for i := range pfxBLSecretKey {
		if raw[i] != pfxBLSecretKey[i] {
			return nil, errBadBLskPrefix
		}
	}
	le := raw[len(pfxBLSecretKey):n]
	if len(le) != 32 {
		return nil, errBLSecretKeyPayloadNot32Bytes
	}
	var sk blst.SecretKey
	if sk.FromLEndian(le) == nil { // FromLEndian exists on Scalar alias
		return nil, errScalarInvalid
	}
	// optional: runtime.SetFinalizer(&sk, func(s *blst.SecretKey){ s.Zeroize() })
	return &sk, nil
}

// Convenience: derive compressed G1 pubkey (48) and BLpubkey string from a SecretKey
func PublicKeyFromSecret(secretKey *blst.SecretKey) ([]byte, string) {
	pubkey := new(PublicKey).From(secretKey)
	pubkeyBytes := pubkey.Compress()
	return pubkeyBytes, b58CheckEncode(pfxBLPubkey, pubkeyBytes)
}
