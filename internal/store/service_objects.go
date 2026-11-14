// File metadata (manifest mirror):
// Purpose: Preserves legacy service-object public values and necessary pure association or canonical-rule adapters through Operator aliases.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-14
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
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

var errInvalidServiceObjectAssociation = operatorModel.ErrInvalidServiceObjectAssociation

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
func validateServiceObjectFactRefs(v ServiceObjectContractAssociation) error {
	return operatorModel.ValidateServiceObjectFactRefs(v)
}
func canonicalRuleFactSHA256(v rulepack.Fact) (string, error) {
	return operatorModel.CanonicalRuleFactSHA256(v)
}
func validServiceObjectSHA256(v string) bool { return operatorModel.ValidServiceObjectSHA256(v) }
