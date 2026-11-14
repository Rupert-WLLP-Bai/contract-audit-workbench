// File metadata (manifest mirror):
// Purpose: Keeps existing Store Subject public DTOs and private payload names compatible through direct Subject-model aliases.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import subjectModel "contractaudit/internal/subject/model"

const (
	SubjectRoleCustomer        = subjectModel.SubjectRoleCustomer
	SubjectRoleSupplier        = subjectModel.SubjectRoleSupplier
	SubjectRoleContractor      = subjectModel.SubjectRoleContractor
	SubjectRoleServiceProvider = subjectModel.SubjectRoleServiceProvider
	SubjectRoleOther           = subjectModel.SubjectRoleOther
	syntheticSubjectSourceID   = subjectModel.SyntheticSubjectSourceID
)

type Subject = subjectModel.Subject
type SubjectIdentityClaimInput = subjectModel.SubjectIdentityClaimInput
type SubjectIdentityClaim = subjectModel.SubjectIdentityClaim
type SubjectAliasInput = subjectModel.SubjectAliasInput
type SubjectAlias = subjectModel.SubjectAlias
type SubjectVersion = subjectModel.SubjectVersion
type CaseSubjectRole = subjectModel.CaseSubjectRole
type CaseSubjectRoleVersion = subjectModel.CaseSubjectRoleVersion
type SubjectIdentityClaimProjection = subjectModel.SubjectIdentityClaimProjection
type TrustedPrincipalProjection = subjectModel.TrustedPrincipalProjection
type SubjectSummary = subjectModel.SubjectSummary
type SubjectVersionProjection = subjectModel.SubjectVersionProjection
type CaseSubjectBindingProjection = subjectModel.CaseSubjectBindingProjection
type LegacySubjectProfileProjection = subjectModel.LegacySubjectProfileProjection
type CaseSubjectListProjection = subjectModel.CaseSubjectListProjection
type SubjectChangeTarget = subjectModel.SubjectChangeTarget
type SubjectChangeRequest = subjectModel.SubjectChangeRequest
type PartyContextItem = subjectModel.PartyContextItem
type SubjectChangeDecision = subjectModel.SubjectChangeDecision
type ControlledSourceBasisRef = subjectModel.ControlledSourceBasisRef
type SubjectIdempotencyRecord = subjectModel.SubjectIdempotencyRecord
type SubjectSourceSnapshot = subjectModel.SubjectSourceSnapshot
type SubjectSourceRecord = subjectModel.SubjectSourceRecord
type SubjectSourceManifest = subjectModel.SubjectSourceManifest
type SubjectSourceDirectoryItem = subjectModel.SubjectSourceDirectoryItem
type SubjectSourceDirectoryResult = subjectModel.SubjectSourceDirectoryResult
type SubjectSourceSnapshotCreateInput = subjectModel.SubjectSourceSnapshotCreateInput
type SubjectSourceSnapshotResult = subjectModel.SubjectSourceSnapshotResult
type SubjectSourceVerifyInput = subjectModel.SubjectSourceVerifyInput
type SubjectSourceMatchedRecord = subjectModel.SubjectSourceMatchedRecord
type SubjectSourceVerifyResult = subjectModel.SubjectSourceVerifyResult
type SubjectSourceSnapshotListResult = subjectModel.SubjectSourceSnapshotListResult
type SubjectSourceSnapshotDetail = subjectModel.SubjectSourceSnapshotDetail
type CaseRolePatch = subjectModel.CaseRolePatch

type SubjectCreateInput = subjectModel.SubjectCreateInput
type LegacyBindingInput = subjectModel.LegacyBindingInput
type CaseSubjectCreateInput = subjectModel.CaseSubjectCreateInput
type CreateSubjectResult = subjectModel.CreateSubjectResult
type CreateCaseSubjectResult = subjectModel.CreateCaseSubjectResult
type BindExistingSubjectInput = subjectModel.BindExistingSubjectInput
type BindExistingSubjectResult = subjectModel.BindExistingSubjectResult
type SubjectListResult = subjectModel.SubjectListResult
type SubjectDetailResult = subjectModel.SubjectDetailResult
type SubjectChangeEventSummary = subjectModel.SubjectChangeEventSummary
type SubjectHistoryResult = subjectModel.SubjectHistoryResult
type SubjectCaseLinksResult = subjectModel.SubjectCaseLinksResult
type SubjectCaseLinkProjection = subjectModel.SubjectCaseLinkProjection

type SubjectChangeCreateInput = subjectModel.SubjectChangeCreateInput
type SubjectChangeCreateResult = subjectModel.SubjectChangeCreateResult
type SubjectChangeSummary = subjectModel.SubjectChangeSummary
type SubjectChangeListResult = subjectModel.SubjectChangeListResult
type SubjectChangeDetailResult = subjectModel.SubjectChangeDetailResult
type SubjectChangeDecisionInput = subjectModel.SubjectChangeDecisionInput
type SubjectChangeDecisionResult = subjectModel.SubjectChangeDecisionResult
type SubjectChangeDecisionResultWithReplay = subjectModel.SubjectChangeDecisionResultWithReplay
type SubjectChangeImpactResult = subjectModel.SubjectChangeImpactResult
type SubjectImpactCaseProjection = subjectModel.SubjectImpactCaseProjection
type subjectAfterPayload = subjectModel.SubjectAfterPayload
type bindingAfterPayload = subjectModel.BindingAfterPayload
