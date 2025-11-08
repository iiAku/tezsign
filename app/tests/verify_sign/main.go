package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/tez-capital/tezsign/signer"
)

func main() {
	// ----- Generate and verify -----
	// 1) Keygen
	fmt.Println("----- Generate and verify -----")
	secretKey, pubkeyBytes, blPubkey := signer.GenerateRandomKey()
	fmt.Println("BLPubkey:", blPubkey)

	// 2) tz4
	tz4, err := signer.Tz4FromBLPubkeyBytes(pubkeyBytes)
	if err != nil {
		log.Fatalf("tz4: %v", err)
	}
	fmt.Println("tz4:", tz4)

	// 3) Sign
	msg := []byte("hello-tezos") // hex: 68656c6c6f2d74657a6f73
	sigBytes, blSig := signer.SignCompressed(secretKey, msg)
	fmt.Println("BLSig:", blSig)
	fmt.Println("Sig(hex):", hex.EncodeToString(sigBytes))

	// 4) Verify + Aggregate
	ok := signer.VerifyCompressed(pubkeyBytes, sigBytes, msg)
	fmt.Println("verify:", ok)

	agg, err := signer.AggregateCompressed([][]byte{sigBytes, sigBytes})
	if err != nil {
		fmt.Fprintln(os.Stderr, "aggregate:", err)
		os.Exit(1)
	}
	fmt.Println("aggSig(hex):", hex.EncodeToString(agg))

	// ----- Compare with octez-client -----
	// ./octez-client -E https://mainnet.api.tez.ie show address bls1 -S
	// copy unencrypted:BLsk...
	fmt.Println("----------------------------------")
	fmt.Println("Importing BLSecretKey: BLsk3QwjBXavZAS4N7xvM3cmb1AMsLf6Fh9GBmo45x9vJv1NUjUJZi")
	secretKey_2, err := signer.ImportBLSecretKey("BLsk3QwjBXavZAS4N7xvM3cmb1AMsLf6Fh9GBmo45x9vJv1NUjUJZi")
	if err != nil {
		log.Fatalf("ImportBLSecretKey: %v", err)
	}

	// 2) Derive pubkey + encodings; compute tz4
	pubkeyBytes_2, blPubkey_2 := signer.PublicKeyFromSecret(secretKey_2)
	tz4_2, err := signer.Tz4FromBLPubkeyBytes(pubkeyBytes_2)
	if err != nil {
		log.Fatalf("tz4_2: %v", err)
	}
	fmt.Println("BLPubkey_2:", blPubkey_2)
	fmt.Println("tz4_2 :", tz4_2)

	// 3) Sign exact same bytes you’ll sign in Octez
	sigBytes_2, blSig_2 := signer.SignCompressed(secretKey_2, msg)
	fmt.Println("BLSig_2:", blSig_2)
	fmt.Println("Sig_2 (hex):", hex.EncodeToString(sigBytes_2))

	// 4) Self-verify (compressed pk+sig) — should be true
	ok_2 := signer.VerifyCompressed(pubkeyBytes_2, sigBytes_2, msg)
	fmt.Println("verify_2:", ok_2)

}
