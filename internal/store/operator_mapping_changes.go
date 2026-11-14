// File metadata (manifest mirror):
// Purpose: Preserves legacy mapping public aliases and necessary pure-rule compatibility forwarders to the Operator domain.
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
)

type OperatorMappingHead = operatorModel.OperatorMappingHead
type OperatorMappingVersion = operatorModel.OperatorMappingVersion
type OperatorMappingChangePatch = operatorModel.OperatorMappingChangePatch
type OperatorServiceIdentityPatch = operatorModel.OperatorServiceIdentityPatch
type OperatorMappingChangeCAS = operatorModel.OperatorMappingChangeCAS
type OperatorMappingChangeRequest = operatorModel.OperatorMappingChangeRequest
type OperatorMappingChangeCreateInput = operatorModel.OperatorMappingChangeCreateInput
type OperatorMappingChangeDecisionInput = operatorModel.OperatorMappingChangeDecisionInput
type OperatorMappingChangeDecisionResult = operatorModel.OperatorMappingChangeDecisionResult
type OperatorMappingChangeDecision = operatorModel.OperatorMappingChangeDecision
type OperatorMappingChangePlan = operatorModel.OperatorMappingChangePlan
type OperatorMappingChangePreview = operatorModel.OperatorMappingChangePreview

type operatorMappingCanonicalState = operatorModel.OperatorMappingCanonicalState
type operatorMappingApprovedAfter = operatorModel.OperatorMappingApprovedAfter
type operatorMappingIdentityAfter = operatorModel.OperatorMappingIdentityAfter
type operatorMappingAssociationAfter = operatorModel.OperatorMappingAssociationAfter
type operatorMappingChangeRecord = operatorModel.OperatorMappingChangeRecord

const (
	OperatorMappingReplace                   = operatorModel.OperatorMappingReplace
	OperatorMappingRetire                    = operatorModel.OperatorMappingRetire
	OperatorMappingReactivate                = operatorModel.OperatorMappingReactivate
	OperatorMappingCorrectIdentity           = operatorModel.OperatorMappingCorrectIdentity
	OperatorMappingCorrectIdentityAndMapping = operatorModel.OperatorMappingCorrectIdentityAndMapping
	OperatorMappingRetireServiceObject       = operatorModel.OperatorMappingRetireServiceObject
)

func PlanOperatorMappingChange(head OperatorMappingHead, current OperatorMappingVersion, cas OperatorMappingChangeCAS,
	patch OperatorMappingChangePatch, newBindingVersion string, actor string, now time.Time) (OperatorMappingChangePlan, error) {
	return operatorModel.PlanOperatorMappingChange(head, current, cas, patch, newBindingVersion, actor, now)
}

func operatorMappingApprovedDigest(mapping operatorMappingCanonicalState, identity *operatorMappingIdentityAfter, association *operatorMappingAssociationAfter) (string, error) {
	return operatorModel.OperatorMappingApprovedDigest(mapping, identity, association)
}

func canonicalOperatorMappingState(version OperatorMappingVersion) operatorMappingCanonicalState {
	return operatorModel.CanonicalOperatorMappingState(version)
}

func operatorMappingDecisionIndependent(request OperatorMappingChangeRequest, decider TrustedPrincipalProjection) bool {
	return operatorModel.OperatorMappingDecisionIndependent(request, decider)
}

func proposedOperatorIdentityVersion(serviceObjectID string, currentVersion int, patch OperatorServiceIdentityPatch, effectiveFrom time.Time) (ServiceObjectVersion, error) {
	return operatorModel.ProposedOperatorIdentityVersion(serviceObjectID, currentVersion, patch, effectiveFrom)
}
