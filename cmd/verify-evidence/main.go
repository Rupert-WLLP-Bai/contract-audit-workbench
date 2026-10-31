// Command verify-evidence verifies a signed evidence ZIP without contacting the
// application. The trusted key or its independently obtained fingerprint is
// mandatory; a key embedded in the archive is never trusted on its own.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"contractaudit/internal/evidencebundle"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "verification failed:", err)
		os.Exit(1)
	}
}

func run() error {
	var keyFile, fingerprint string
	flag.StringVar(&keyFile, "public-key", "", "trusted Ed25519 PKIX public key PEM file")
	flag.StringVar(&fingerprint, "fingerprint", "", "trusted lowercase SHA-256 fingerprint of the DER SubjectPublicKeyInfo")
	flag.Parse()
	if flag.NArg() != 1 || keyFile == "" && fingerprint == "" {
		return errors.New("usage: verify-evidence [-public-key trusted.pem] [-fingerprint sha256] bundle.zip; at least one trust option is required")
	}
	var trusted []byte
	if keyFile != "" {
		key, err := evidencebundle.PublicKeyFromFile(keyFile)
		if err != nil {
			return err
		}
		trusted = key
	}
	file, err := os.Open(flag.Arg(0))
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	manifest, err := evidencebundle.Verify(file, info.Size(), trusted, fingerprint)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Status      string                  `json:"status"`
		Identity    evidencebundle.Identity `json:"identity"`
		Fingerprint string                  `json:"public_key_spki_sha256"`
		Entries     int                     `json:"verified_entries"`
	}{Status: "verified", Identity: manifest.Identity, Fingerprint: manifest.PublicKeySHA256, Entries: len(manifest.Entries)})
}
