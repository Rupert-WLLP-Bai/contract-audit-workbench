package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"contractaudit/internal/docversion"
)

var errObjectIntegrity = errors.New("stored original digest or size mismatch")

// ObjectStore stores private immutable blobs under Store-generated keys.
type ObjectStore interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}

// UseObjectStore installs the private object backend before serving requests.
func (s *Store) UseObjectStore(objects ObjectStore) {
	s.mu.Lock()
	s.objects = objects
	s.mu.Unlock()
}

func (s *Store) objectBackend() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects != nil {
		return "remote"
	}
	return "local"
}

func objectKey(tenantID, caseID, digest string) string {
	return tenantID + "/" + caseID + "/" + digest
}

func (s *Store) putObject(ctx context.Context, tenantID, caseID, digest string, body []byte) error {
	if _, err := docversion.ParseDigest(digest); err != nil {
		return err
	}
	s.mu.Lock()
	objects, dir := s.objects, s.dir
	s.mu.Unlock()
	if objects != nil {
		return objects.Put(ctx, objectKey(tenantID, caseID, digest), body)
	}
	return writeBlob(dir, digest, body)
}

func (s *Store) getObject(ctx context.Context, doc Document) ([]byte, error) {
	if _, err := docversion.ParseDigest(doc.Digest); err != nil {
		return nil, err
	}
	s.mu.Lock()
	objects, dir := s.objects, s.dir
	s.mu.Unlock()
	var body []byte
	var err error
	if doc.StorageBackend == "remote" {
		if objects == nil {
			return nil, errors.New("remote original backend is unavailable")
		}
		body, err = objects.Get(ctx, objectKey(doc.TenantID, doc.CaseID, doc.Digest))
	} else {
		body, err = readBlob(dir, doc.Digest)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != doc.Digest || int64(len(body)) != doc.Size {
		return nil, errObjectIntegrity
	}
	return body, nil
}
