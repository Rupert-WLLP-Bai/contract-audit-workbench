package store

import (
	"context"

	"contractaudit/internal/docir"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

// authorizeFrozenDocumentConfiguration reconstructs the accepted parse identity
// from the persisted selection and the provider chosen by the gateway. The
// caller's identity is deliberately not an input to this comparison.
func authorizeFrozenDocumentConfiguration(ctx context.Context, tx pgx.Tx, scope model.DelegatedScope, providerIdentity string) error {
	var filename, media, parserID, parserVersion, configHash, normalization string
	err := tx.QueryRow(ctx, `SELECT d.filename,d.media_type,p.parser_id,p.parser_version,p.config_hash,p.normalization_version
		FROM review_attempt_parse_selections p
		JOIN documents d ON d.id=p.document_id AND d.case_id=$3 AND d.tenant_id=$4 AND d.org_id=$5 AND d.digest=p.original_digest
		WHERE p.attempt_id=$1 AND p.run_id=$2 AND p.document_id=$6 AND p.parsed_document_id=$7`,
		scope.AttemptID, scope.RunID, scope.CaseID, scope.TenantID, scope.OrgID, scope.DocumentID, scope.ParseSelectionID).
		Scan(&filename, &media, &parserID, &parserVersion, &configHash, &normalization)
	if err != nil {
		return ErrModelScopeUnavailable
	}
	var expectedParser string
	switch fileKind(filename, media) {
	case kindImage:
		expectedParser = "ocr"
	case kindPDF:
		expectedParser = "pdf"
	case kindDOCX:
		expectedParser = "docx"
	default:
		return ErrModelScopeUnavailable
	}
	if providerIdentity == "" || parserID != expectedParser || parserVersion == "" || normalization != docir.NormalizationVersion ||
		configHash != ocrConfigurationHash(parserID, parserVersion, normalization, providerIdentity) {
		return ErrModelConfigurationChanged
	}
	return nil
}
