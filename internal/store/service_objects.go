// File metadata (manifest mirror):
// Purpose: ServiceObjectHead contains only identity and version pointers. The selected immutable version is the single authority for object type and state. Related `store` operations include serviceObjectCandidatesSHA256, PlanServiceObjectAssociation, PlanServiceObjectIdentityChange, canonicalServiceObjectIdentitySHA256.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-10-31
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	operatorModel "contractaudit/internal/operator/model"
	"contractaudit/internal/rulepack"
)

type ServiceObjectType = operatorModel.ServiceObjectType
type ServiceObjectState = operatorModel.ServiceObjectState
type ServiceObjectHead = operatorModel.ServiceObjectHead
type ServiceObjectVersion = operatorModel.ServiceObjectVersion
type ServiceObjectAlias = operatorModel.ServiceObjectAlias
type ServiceObjectFactRef = operatorModel.ServiceObjectFactRef
type ServiceObjectContractAssociation = operatorModel.ServiceObjectContractAssociation
type ServiceObjectAssociationCandidate = operatorModel.ServiceObjectAssociationCandidate
type ServiceObjectAssociationPlan = operatorModel.ServiceObjectAssociationPlan
type ServiceObjectAssociationPreviewInput = operatorModel.ServiceObjectAssociationPreviewInput
type ServiceObjectAssociationConfirmInput = operatorModel.ServiceObjectAssociationConfirmInput
type ServiceObjectCreateInput = operatorModel.ServiceObjectCreateInput
type ServiceObjectMutationResult = operatorModel.ServiceObjectMutationResult
type ServiceObjectIdentityHistory = operatorModel.ServiceObjectIdentityHistory
type ServiceObjectAssociationPin = operatorModel.ServiceObjectAssociationPin
type ServiceObjectSummary = operatorModel.ServiceObjectSummary
type ServiceObjectMappingPin = operatorModel.ServiceObjectMappingPin
type ServiceObjectDetail = operatorModel.ServiceObjectDetail
type ServiceObjectListResult = operatorModel.ServiceObjectListResult
type ServiceObjectVersionPage = operatorModel.ServiceObjectVersionPage
type ServiceObjectAssociationPage = operatorModel.ServiceObjectAssociationPage
type ServiceObjectAssociationOption = operatorModel.ServiceObjectAssociationOption
type ServiceObjectAssociationPreview = operatorModel.ServiceObjectAssociationPreview
type serviceObjectCandidateDigestPin = operatorModel.ServiceObjectCandidateDigestPin
type serviceObjectCandidateDigestInput = operatorModel.ServiceObjectCandidateDigestInput

const (
	ServiceObjectDedicatedLine       = operatorModel.ServiceObjectDedicatedLine
	ServiceObjectService             = operatorModel.ServiceObjectService
	ServiceObjectPendingConfirmation = operatorModel.ServiceObjectPendingConfirmation
	ServiceObjectConfirmed           = operatorModel.ServiceObjectConfirmed
	ServiceObjectInactive            = operatorModel.ServiceObjectInactive
)

// verifiedServiceObjectIdentityChange is produced only after the store's
// decision service has loaded an approved SOD decision and verified its
// before-version CAS and complete after-payload hash in the same transaction.
// The planner receives this trusted pin instead of accepting an arbitrary
// decision ID from an HTTP request.
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

var errInvalidServiceObjectAssociation = operatorModel.ErrInvalidServiceObjectAssociation

// PlanServiceObjectIdentityChange creates the next identity and association
// versions after an independently approved SOD decision. It deliberately
// carries forward the exact frozen fact bytes and digest from the prior
// association; it never consults a newer parser result.
func PlanServiceObjectIdentityChange(head ServiceObjectHead, current ServiceObjectVersion, currentAssociation ServiceObjectContractAssociation, proposed ServiceObjectVersion, approval verifiedServiceObjectIdentityChange, approverID string, now time.Time) (ServiceObjectAssociationPlan, error) {
	if head.ID == "" || head.ID != current.ServiceObjectID || head.CurrentVersion != current.Version ||
		head.CurrentVersion != approval.expectedObjectVersion || head.CurrentAssociationVersion != approval.expectedAssociation ||
		currentAssociation.ServiceObjectID != head.ID || currentAssociation.ObjectVersion != current.Version ||
		currentAssociation.AssociationVersion != head.CurrentAssociationVersion || head.CurrentAssociationVersion < 1 ||
		current.State != ServiceObjectConfirmed || strings.TrimSpace(approval.decisionID) == "" || !approval.validatedByDecisionSvc ||
		strings.TrimSpace(approverID) == "" || now.IsZero() ||
		len(currentAssociation.FactsCanonicalJSON) == 0 || !validServiceObjectSHA256(currentAssociation.FactsSHA256) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	priorDigest := sha256.Sum256(currentAssociation.FactsCanonicalJSON)
	if hex.EncodeToString(priorDigest[:]) != currentAssociation.FactsSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	canonicalFacts, canonicalFactsSHA, err := canonicalServiceObjectFacts(currentAssociation.FactRefs)
	if err != nil || !bytes.Equal(canonicalFacts, currentAssociation.FactsCanonicalJSON) || canonicalFactsSHA != currentAssociation.FactsSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	version, err := normalizeServiceObjectVersion(proposed)
	if err != nil || version.ServiceObjectID != head.ID || (version.State != ServiceObjectConfirmed && version.State != ServiceObjectInactive) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	beforeHash, err := canonicalServiceObjectIdentitySHA256(current)
	if err != nil || beforeHash != current.PayloadSHA256 || beforeHash != approval.beforePayloadSHA256 {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	afterHash, err := canonicalServiceObjectIdentitySHA256(version)
	if err != nil || afterHash != approval.afterPayloadSHA256 || !version.EffectiveFrom.UTC().Equal(approval.effectiveFrom) || !bytes.Equal(version.BasisRefs, approval.basisRefs) {
		return ServiceObjectAssociationPlan{}, errInvalidServiceObjectAssociation
	}
	version.Version = current.Version + 1
	version.ChangeDecisionID = strings.TrimSpace(approval.decisionID)
	version.CreatedBy = approverID
	version.CreatedAt = now.UTC()
	version.PayloadSHA256 = afterHash

	association := currentAssociation
	association.FactRefs = cloneServiceObjectFactRefs(currentAssociation.FactRefs)
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
	return ServiceObjectAssociationPlan{Head: head, ObjectVersion: version, Association: association}, nil
}

func serviceObjectCandidatesSHA256(v serviceObjectCandidateDigestInput) (string, error) {
	return operatorModel.ServiceObjectCandidatesSHA256(v)
}
func PlanServiceObjectAssociation(h ServiceObjectHead, v ServiceObjectVersion, c ServiceObjectAssociationCandidate, a string, t time.Time) (ServiceObjectAssociationPlan, error) {
	return operatorModel.PlanServiceObjectAssociation(h, v, c, a, t)
}
func canonicalServiceObjectIdentitySHA256(v ServiceObjectVersion) (string, error) {
	return operatorModel.CanonicalServiceObjectIdentitySHA256(v)
}
func canonicalServiceObjectFacts(v []ServiceObjectFactRef) ([]byte, string, error) {
	return operatorModel.CanonicalServiceObjectFacts(v)
}
func normalizeServiceObjectVersion(v ServiceObjectVersion) (ServiceObjectVersion, error) {
	return operatorModel.NormalizeServiceObjectVersion(v)
}
func validateServiceObjectFactRefs(v ServiceObjectContractAssociation) error {
	return operatorModel.ValidateServiceObjectFactRefs(v)
}
func canonicalRuleFactSHA256(v rulepack.Fact) (string, error) {
	return operatorModel.CanonicalRuleFactSHA256(v)
}
func cloneServiceObjectFactRefs(v []ServiceObjectFactRef) []ServiceObjectFactRef {
	return operatorModel.CloneServiceObjectFactRefs(v)
}
func validServiceObjectSHA256(v string) bool { return operatorModel.ValidServiceObjectSHA256(v) }
