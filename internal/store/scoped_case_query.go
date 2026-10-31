package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// queryCaseRows lets the existing row decoders serve both startup's complete
// load and case mutations. Each caller supplies its own fixed case predicate.
func queryCaseRows(ctx context.Context, q queryer, query, predicate, order string, caseID ...string) (pgx.Rows, error) {
	if len(caseID) == 0 {
		return q.Query(ctx, query+order)
	}
	return q.Query(ctx, query+" WHERE "+predicate+order, caseID[0])
}

// Scoped writes enforce the same column, payload and original-digest checks as
// the strict whole-state loader, while decoding only the selected documents.
func decodeScopedParsedRows(rows pgx.Rows, documents []Document) ([]ParsedDocument, error) {
	defer rows.Close()
	byID := make(map[string]Document, len(documents))
	for _, doc := range documents {
		byID[doc.ID] = doc
	}
	var out []ParsedDocument
	for rows.Next() {
		var row parsedRow
		if err := rows.Scan(&row.id, &row.selectionOrder, &row.documentID, &row.originalDigest, &row.parserID, &row.parserVersion, &row.configHash,
			&row.normalizationVersion, &row.normalizedDigest, &row.irDigest, &row.artifactDigest, &row.createdAt, &row.status, &row.payload); err != nil {
			return nil, err
		}
		parsed, err := row.decode(byID)
		if err != nil {
			return nil, err
		}
		out = append(out, parsed)
	}
	return out, rows.Err()
}
