// File metadata (manifest mirror):
// Purpose: Bridges clause acceptance to authorized original byte reads and verifies command session identity, document size and digest.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"errors"
)

// ClauseOriginalReader reads through Document's currently authorized original
// endpoint. The session remains request-scoped and is never persisted.
type ClauseOriginalReader interface {
	ReadClauseOriginal(context.Context, string, string, string) ([]byte, error)
}
type clauseDocumentSessionKey struct{}

func WithClauseDocumentSession(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, clauseDocumentSessionKey{}, token)
}
func (s *Store) UseClauseOriginalReader(reader ClauseOriginalReader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clauseOriginalReader = reader
}
func (s *Store) readClauseOriginal(ctx context.Context, doc Document) ([]byte, error) {
	if s.serviceOwner == "" {
		return s.getObject(ctx, doc)
	}
	s.mu.Lock()
	reader := s.clauseOriginalReader
	s.mu.Unlock()
	token, _ := ctx.Value(clauseDocumentSessionKey{}).(string)
	if reader == nil || token == "" {
		return nil, errors.New("authorized document original reader unavailable")
	}
	if proof, ok := contextProof(ctx); ok && proof.digest != "" && proof.digest != sessionTokenDigest(token) {
		return nil, errors.New("document session does not match command authority")
	}
	body, err := reader.ReadClauseOriginal(ctx, doc.CaseID, doc.ID, token)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != doc.Size || sha256Bytes(body) != doc.Digest {
		return nil, errObjectIntegrity
	}
	return body, nil
}
