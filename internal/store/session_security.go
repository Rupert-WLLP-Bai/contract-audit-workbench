package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// sessionLegacyCleanupSQL removes only tokens produced by the old newID("s")
// implementation. Stop old application instances before migrating: their
// sessions cannot be used after the cutover and users must sign in again.
// A single migration transaction temporarily removes FORCE so the table owner
// can clean every tenant. Its table lock prevents other sessions from observing
// that change, and FORCE is restored before the transaction ends.
const sessionLegacyCleanupSQL = `DO $$
BEGIN
	ALTER TABLE sessions NO FORCE ROW LEVEL SECURITY;
	DELETE FROM sessions WHERE token COLLATE "C" ~ '^s[0-9a-f]{16}$';
	ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
END $$`

func newSessionToken() (string, error) {
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return "", err
	}
	return "s_" + base64.RawURLEncoding.EncodeToString(entropy[:]), nil
}

// sessionTokenDigest is also applied to presented digest strings, so a value
// read from the database cannot itself be used as a bearer credential.
func sessionTokenDigest(token string) string {
	digest := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(digest[:])
}
