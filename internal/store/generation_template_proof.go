package store

import (
	"context"
	"errors"

	"contractaudit/internal/accounts"
	executionmodel "contractaudit/internal/execution/model"
	"github.com/jackc/pgx/v5"
)

// attachGenerationTemplate reads only the Document-owned immutable generation
// for the already-authorized original. Legacy rows without a digest remain nil.
func (s *Store) attachGenerationTemplate(ctx context.Context, user accounts.User, doc *FrozenDocument) error {
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = setLocalScope(ctx, tx, userScope(user)); err != nil {
		return err
	}
	var id, version, bodyDigest string
	var digest *string
	err = tx.QueryRow(ctx, `SELECT template_id,template_version,digest,template_digest FROM business_generations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND document_id=$4`, doc.Document.TenantID, doc.Document.OrgID, doc.Document.CaseID, doc.Document.ID).Scan(&id, &version, &bodyDigest, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if bodyDigest != doc.Document.Digest || id == "" || version == "" {
		return errors.New("generated template provenance is invalid")
	}
	if digest != nil {
		if len(*digest) != 64 {
			return errors.New("generated template digest is invalid")
		}
		doc.Template = &executionmodel.FrozenTemplateRef{ID: id, Version: version, Digest: *digest}
	}
	return tx.Commit(ctx)
}

func bindGenerationTemplate(st *state, doc *FrozenDocument) error {
	for _, g := range st.Generations {
		if g.Record.DocumentID != doc.Document.ID || g.CaseID != doc.Document.CaseID {
			continue
		}
		if g.Record.Digest != doc.Document.Digest || g.Record.TemplateID == "" || g.Record.TemplateVersion == "" {
			return errors.New("generated template provenance is invalid")
		}
		if g.Record.TemplateDigest != nil {
			if len(*g.Record.TemplateDigest) != 64 {
				return errors.New("generated template digest is invalid")
			}
			doc.Template = &executionmodel.FrozenTemplateRef{ID: g.Record.TemplateID, Version: g.Record.TemplateVersion, Digest: *g.Record.TemplateDigest}
		}
		return nil
	}
	return nil
}
