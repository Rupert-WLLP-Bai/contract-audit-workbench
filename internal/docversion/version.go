// Package docversion identifies an immutable snapshot of an original file.
//
// The content digest is SHA-256 over the original bytes. A hash of normalized
// or parsed text is a different value and is not produced here.
package docversion

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

var (
	ErrEmptyID        = errors.New("docversion: empty id")
	ErrEmptySource    = errors.New("docversion: empty source origin")
	ErrInvalidDigest  = errors.New("docversion: invalid content digest")
	ErrDigestMismatch = errors.New("docversion: content digest mismatch")
)

// Digest is a lowercase hex SHA-256 of original file bytes.
type Digest string

func (d Digest) String() string { return string(d) }

// Hash returns the content digest of original file bytes.
// Nil and empty input are the same empty file.
func Hash(content []byte) Digest {
	sum := sha256.Sum256(content)
	return Digest(hex.EncodeToString(sum[:]))
}

// ParseDigest accepts a lowercase hex SHA-256 and nothing else.
func ParseDigest(s string) (Digest, error) {
	if len(s) != sha256.Size*2 {
		return "", ErrInvalidDigest
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", ErrInvalidDigest
		}
	}
	return Digest(s), nil
}

// Source is the file-source metadata recorded with a snapshot.
// Filename and MediaType do not participate in the content digest.
type Source struct {
	Origin    string
	Filename  string
	MediaType string
}

// Version is an immutable original-file snapshot.
// The caller supplies the stable id; identical bytes do not collapse two ids.
type Version struct {
	id     string
	digest Digest
	size   int64
	source Source
}

// New records a snapshot of content. The digest is derived from content;
// callers cannot supply a different one.
func New(id string, source Source, content []byte) (Version, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Version{}, ErrEmptyID
	}
	origin := strings.TrimSpace(source.Origin)
	if origin == "" {
		return Version{}, ErrEmptySource
	}
	source.Origin = origin
	return Version{
		id:     id,
		digest: Hash(content),
		size:   int64(len(content)),
		source: source,
	}, nil
}

func (v Version) ID() string     { return v.id }
func (v Version) Digest() Digest { return v.digest }
func (v Version) Size() int64    { return v.size }
func (v Version) Source() Source { return v.source }

// SameContent reports whether both snapshots were taken from identical bytes.
func (v Version) SameContent(other Version) bool {
	return v.digest != "" && v.digest == other.digest
}

// Verify checks that content is still the original bytes of this snapshot.
func (v Version) Verify(content []byte) error {
	if _, err := ParseDigest(string(v.digest)); err != nil {
		return ErrInvalidDigest
	}
	if int64(len(content)) != v.size || Hash(content) != v.digest {
		return ErrDigestMismatch
	}
	return nil
}
