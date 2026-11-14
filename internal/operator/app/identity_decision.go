// File metadata (manifest mirror):
// Purpose: Keeps the approved identity-change capability private to the real mapping decision application and preserves frozen fact bytes in appended identity or association plans.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"bytes"
	"contractaudit/internal/operator/model"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type verifiedServiceObjectIdentityChange struct {
	decisionID             string
	expectedObjectVersion  int
	expectedAssociation    int
	beforePayloadSHA256    string
	afterPayloadSHA256     string
	effectiveFrom          time.Time
	basisRefs              json.RawMessage
	validatedByDecisionSvc bool
}

// planServiceObjectIdentityChange creates the next identity and association
// versions after an independently approved SOD decision. It deliberately
// carries forward the exact frozen fact bytes and digest from the prior
// association; it never consults a newer parser result.
func planServiceObjectIdentityChange(head model.ServiceObjectHead, current model.ServiceObjectVersion, currentAssociation model.ServiceObjectContractAssociation, proposed model.ServiceObjectVersion, approval verifiedServiceObjectIdentityChange, approverID string, now time.Time) (model.ServiceObjectAssociationPlan, error) {
	if head.ID == "" || head.ID != current.ServiceObjectID || head.CurrentVersion != current.Version ||
		head.CurrentVersion != approval.expectedObjectVersion || head.CurrentAssociationVersion != approval.expectedAssociation ||
		currentAssociation.ServiceObjectID != head.ID || currentAssociation.ObjectVersion != current.Version ||
		currentAssociation.AssociationVersion != head.CurrentAssociationVersion || head.CurrentAssociationVersion < 1 ||
		current.State != model.ServiceObjectConfirmed || strings.TrimSpace(approval.decisionID) == "" || !approval.validatedByDecisionSvc ||
		strings.TrimSpace(approverID) == "" || now.IsZero() ||
		len(currentAssociation.FactsCanonicalJSON) == 0 || !model.ValidServiceObjectSHA256(currentAssociation.FactsSHA256) {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	priorDigest := sha256.Sum256(currentAssociation.FactsCanonicalJSON)
	if hex.EncodeToString(priorDigest[:]) != currentAssociation.FactsSHA256 {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	canonicalFacts, canonicalFactsSHA, err := model.CanonicalServiceObjectFacts(currentAssociation.FactRefs)
	if err != nil || !bytes.Equal(canonicalFacts, currentAssociation.FactsCanonicalJSON) || canonicalFactsSHA != currentAssociation.FactsSHA256 {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	version, err := model.NormalizeServiceObjectVersion(proposed)
	if err != nil || version.ServiceObjectID != head.ID || (version.State != model.ServiceObjectConfirmed && version.State != model.ServiceObjectInactive) {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	beforeHash, err := model.CanonicalServiceObjectIdentitySHA256(current)
	if err != nil || beforeHash != current.PayloadSHA256 || beforeHash != approval.beforePayloadSHA256 {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	afterHash, err := model.CanonicalServiceObjectIdentitySHA256(version)
	if err != nil || afterHash != approval.afterPayloadSHA256 || !version.EffectiveFrom.UTC().Equal(approval.effectiveFrom) || !bytes.Equal(version.BasisRefs, approval.basisRefs) {
		return model.ServiceObjectAssociationPlan{}, model.ErrInvalidServiceObjectAssociation
	}
	version.Version = current.Version + 1
	version.ChangeDecisionID = strings.TrimSpace(approval.decisionID)
	version.CreatedBy = approverID
	version.CreatedAt = now.UTC()
	version.PayloadSHA256 = afterHash

	association := currentAssociation
	association.FactRefs = model.CloneServiceObjectFactRefs(currentAssociation.FactRefs)
	association.FactsCanonicalJSON = append([]byte(nil), currentAssociation.FactsCanonicalJSON...)
	association.AssociationVersion = currentAssociation.AssociationVersion + 1
	association.ObjectVersion = version.Version
	association.IdentityChangeDecisionID = strings.TrimSpace(approval.decisionID)
	association.ConfirmedBy = approverID
	association.ConfirmedAt = now.UTC()
	association.CreatedAt = now.UTC()
	association.PayloadSHA256 = ""

	head.CurrentVersion = version.Version
	head.CurrentAssociationVersion = association.AssociationVersion
	return model.ServiceObjectAssociationPlan{Head: head, ObjectVersion: version, Association: association}, nil
}
