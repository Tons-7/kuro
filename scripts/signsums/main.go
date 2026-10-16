// signsums signs a release's SHA256SUMS.txt with the offline ed25519 key, so an install can tell a release
// kuro's author published from one a stolen token did: go run ./scripts/signsums -key K -sums S -out S.sig
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	key := flag.String("key", "", "file holding the hex-encoded private key seed")
	sums := flag.String("sums", "", "the SHA256SUMS.txt to sign")
	out := flag.String("out", "", "where the 64-byte signature goes")
	flag.Parse()
	if *key == "" || *sums == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "signsums: -key, -sums and -out are all needed")
		os.Exit(2)
	}
	seedHex, err := os.ReadFile(*key)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signsums:", err)
		os.Exit(1)
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(seedHex)))
	if err != nil || len(seed) != ed25519.SeedSize {
		fmt.Fprintln(os.Stderr, "signsums: the key file does not hold a 32-byte hex seed")
		os.Exit(1)
	}
	body, err := os.ReadFile(*sums)
	if err != nil {
		fmt.Fprintln(os.Stderr, "signsums:", err)
		os.Exit(1)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if err := os.WriteFile(*out, ed25519.Sign(priv, body), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "signsums:", err)
		os.Exit(1)
	}
	fmt.Println("signed with public key", hex.EncodeToString(priv.Public().(ed25519.PublicKey)))
}
