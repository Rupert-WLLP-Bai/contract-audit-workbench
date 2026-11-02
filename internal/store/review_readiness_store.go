package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

// GetReviewReadiness projects both published review scopes without creating an
// attempt. StartReview revalidates authorization and dependencies at acceptance.
func (s *Store) GetReviewReadiness(ctx context.Context, user accounts.User, caseID, businessAt string) (ReviewReadiness, error) {
	ctx = commandContext(ctx, user)
	if ctx == nil {
		return ReviewReadiness{}, fault.Invalid("准备清单读取需要请求上下文")
	}
	if err := ctx.Err(); err != nil {
		return ReviewReadiness{}, err
	}
	if s == nil || s.pool == nil {
		return ReviewReadiness{}, fault.New(503, "store_unavailable", "案件准备清单暂不可用")
	}
	if s.serviceOwner != "" && s.serviceOwner != "execution" {
		return ReviewReadiness{}, fault.Forbidden("当前服务不能读取审查准备清单")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ReviewReadiness{}, err
	}
	var at time.Time
	businessAtProvided := strings.TrimSpace(businessAt) != ""
	if businessAtProvided {
		at, err = knowledgeTime(businessAt)
		if err != nil {
			return ReviewReadiness{}, err
		}
	}
	cutoffNow := time.Now().UTC().Truncate(time.Microsecond)

	tx, _, caseItem, err := s.beginCaseRead(ctx, current, caseID)
	if err != nil {
		return ReviewReadiness{}, err
	}
	defer tx.Rollback(ctx)
	st, err := loadReadinessCaseState(ctx, tx, caseItem)
	if err != nil {
		return ReviewReadiness{}, err
	}
	// Match acceptance: legacy ungoverned cases need an explicit business
	// instant only after a service object has been registered. Never substitute
	// the read clock for a missing business instant.
	businessAtRequired := isGovernedReviewCase(caseItem)
	if !businessAtRequired {
		if err := tx.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM service_objects WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3
		)`, caseItem.TenantID, caseItem.OwnerOrgID, caseID).Scan(&businessAtRequired); err != nil {
			return ReviewReadiness{}, err
		}
	}

	selectionVersion, selectionConfirmed := readinessMaterialSelection(st, caseID)
	materials, _ := currentReadinessMaterials(ctx, s, st, caseID, selectionConfirmed)
	canStartRole := accounts.CanStart(current.Role)
	canReview := canStartRole && authorizeCase(caseItem, current, access.Review)
	acceptanceBlockers := []ReviewReadinessBlocker{}
	checks := []ReviewReadinessCheck{}
	var rulePackProjection *ReviewReadinessRulePack
	var governanceRevision any
	var admissionErr error
	if canReview && isGovernedReviewCase(caseItem) && businessAtProvided {
		assessment, assessErr := assessGovernedReviewTx(ctx, tx, current, caseItem, at, cutoffNow, "acceptance_time", "")
		if assessment.Pack != nil {
			rulePackProjection = &ReviewReadinessRulePack{PackID: assessment.Pack.PackID, Version: assessment.Pack.Version, SnapshotDigest: assessment.Pack.SnapshotDigest}
		}
		if assessment.Knowledge != nil {
			governanceRevision = readinessGovernanceRevision(*assessment.Knowledge)
		}
		if assessErr != nil {
			admissionErr = assessErr
			if blocker, ok := readinessFaultBlocker(assessErr); ok {
				acceptanceBlockers = append(acceptanceBlockers, blocker)
			} else {
				return ReviewReadiness{}, assessErr
			}
		} else {
			checks = reviewReadinessRuleChecks(assessment.Pack, assessment.Knowledge, materials, caseItem)
		}
	} else if canStartRole && !authorizeCase(caseItem, current, access.Review) {
		acceptanceBlockers = append(acceptanceBlockers,
			readinessBlocker("review_permission_required", "blocked", "当前账号没有发起审查所需的案件权限", nil, "请由有案件审查权限的人员发起审查"))
	}

	scope := evaluateReviewReadiness(reviewReadinessInputs{
		CanRead: true, CanStartRole: canStartRole, Writable: !s.ReadOnly() && businessWritable(caseItem) == nil,
		SelectionConfirmed: selectionConfirmed, MinimumMaterials: 2, Materials: materials,
		BusinessAtProvided: businessAtProvided || !businessAtRequired,
		RulePack:           rulePackProjection, AcceptanceBlockers: acceptanceBlockers, Checks: checks,
	})
	primaryID := currentPrimaryDraftID(st, caseID)
	singleChecks := singleContractChecks(materials, primaryID)
	for _, check := range checks {
		check.Status = "not_executed"
		check.NextAction = "本范围不执行正式治理规则；补齐材料后发起完整审查"
		singleChecks = append(singleChecks, check)
	}
	singleScope := evaluateReviewReadiness(reviewReadinessInputs{
		Scope: ReviewScopeSingleContract, PrimaryDocumentID: primaryID,
		CanRead: true, CanStartRole: canStartRole, Writable: !s.ReadOnly() && businessWritable(caseItem) == nil,
		SelectionConfirmed: selectionConfirmed, MinimumMaterials: 1, Materials: materials,
		BusinessAtProvided: businessAtProvided || !businessAtRequired,
		RulePack:           rulePackProjection, AcceptanceBlockers: acceptanceBlockers, Checks: singleChecks,
	})
	singleScope.RuleSet = snapshotScope(applyReviewScope(RunSnapshot{}, ReviewScopeSingleContract))
	if admissionErr != nil && len(scope.Blockers) == 0 {
		return ReviewReadiness{}, admissionErr
	}
	readiness := ReviewReadiness{CaseID: caseID, CanStart: scope.CanStart,
		MaterialSelectionVersion: selectionVersion, Materials: materials, Scopes: []ReviewReadinessScope{scope, singleScope}}
	if businessAtProvided {
		businessAtValue := at
		readiness.BusinessAt = &businessAtValue
	}
	if readiness.Materials == nil {
		readiness.Materials = []ReviewReadinessMaterial{}
	}
	if readiness.Scopes == nil {
		readiness.Scopes = []ReviewReadinessScope{}
	}
	additional := struct {
		BusinessLine       string `json:"business_line"`
		Scenario           string `json:"scenario"`
		ContractType       string `json:"contract_type"`
		BusinessState      string `json:"business_status"`
		ReadOnly           bool   `json:"read_only"`
		BusinessAtSet      bool   `json:"business_at_set"`
		BusinessAtRequired bool   `json:"business_at_required"`
		Governance         any    `json:"governance,omitempty"`
	}{caseItem.BusinessLine, caseItem.Scenario, caseItem.ContractType, caseItem.BusinessStatus,
		s.ReadOnly(), businessAtProvided, businessAtRequired, governanceRevision}
	readiness.Revision, err = reviewReadinessRevision(reviewReadinessRevisionInput{
		CaseID: caseID, BusinessAt: at, CaseUpdatedAt: caseItem.UpdatedAt,
		MaterialSelectionVersion: selectionVersion, Materials: materials, Scope: scope, Scopes: readiness.Scopes, Additional: additional,
	})
	if err != nil {
		return ReviewReadiness{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ReviewReadiness{}, err
	}
	return readiness, nil
}

type governedReviewAssessment struct {
	Pack      *ActiveRulePack
	Knowledge *ReviewKnowledgeSnapshot
}

func readinessGovernanceRevision(knowledge ReviewKnowledgeSnapshot) any {
	return struct {
		Scope                 KnowledgeScope               `json:"scope"`
		AuthorizationRevision string                       `json:"authorization_revision"`
		Pack                  GovernedRulePackRef          `json:"pack"`
		RequiredPolicies      []GovernedKnowledgeSelection `json:"required_policies"`
		OptionalCandidates    []GovernedKnowledgeSelection `json:"optional_candidates"`
		Excluded              []GovernedKnowledgeExclusion `json:"excluded"`
		OptionalTruncated     bool                         `json:"optional_truncated"`
		LegalFingerprint      string                       `json:"legal_fingerprint,omitempty"`
	}{knowledge.Scope, knowledge.AuthorizationRevision, knowledge.RulePack, knowledge.RequiredPolicies,
		knowledge.OptionalCandidates, knowledge.Excluded, knowledge.OptionalTruncated, knowledge.LegalReuseFingerprint}
}

// assessGovernedReviewTx is shared by the read-only readiness endpoint and
// StartReview's acceptance transaction. It resolves the exact pack and
// governance snapshot for one case and business instant.
func assessGovernedReviewTx(ctx context.Context, tx pgx.Tx, user accounts.User, item Case, at, evidenceCutoff time.Time, cutoffMode, explicitCutoff string) (governedReviewAssessment, error) {
	if !isGovernedReviewCase(item) {
		return governedReviewAssessment{}, nil
	}
	pack, err := selectActiveRulePackTx(ctx, tx, item.TenantID, item.BusinessLine, item.Scenario, item.ContractType, at)
	if err != nil {
		return governedReviewAssessment{}, err
	}
	if pack == nil {
		return governedReviewAssessment{}, fault.Conflict("案件没有适用的已发布规则包")
	}
	if err := requireGovernedCapabilitiesForCase(item, pack.Snapshot); err != nil {
		return governedReviewAssessment{Pack: pack}, err
	}
	cutoff := evidenceCutoff.UTC().Truncate(time.Microsecond)
	knowledge, err := resolveGovernedKnowledgeTxWithEvidenceCutoff(ctx, tx, user, item.ID,
		at.UTC().Format(time.RFC3339Nano), acceptanceOptionalKnowledgeLimit, cutoff, cutoffMode, explicitCutoff)
	if err != nil {
		return governedReviewAssessment{Pack: pack}, err
	}
	if knowledge.RulePack != rulePackRef(pack) {
		return governedReviewAssessment{Pack: pack, Knowledge: &knowledge}, fault.Conflict("适用制度与规则包的记录不一致")
	}
	if isCloudServicesCase(item) {
		var snapshot RunSnapshot
		applyActiveRulePackIdentity(&snapshot, pack)
		if _, err := validateCloudPolicyBindings(snapshot, knowledge, item.TenantID); err != nil {
			return governedReviewAssessment{Pack: pack, Knowledge: &knowledge}, fault.Conflict("云服务治理制度绑定无效：" + err.Error())
		}
	}
	return governedReviewAssessment{Pack: pack, Knowledge: &knowledge}, nil
}

func readinessFaultBlocker(err error) (ReviewReadinessBlocker, bool) {
	var problem *fault.Error
	if !errors.As(err, &problem) || problem.Status < 400 || problem.Status >= 500 {
		return ReviewReadinessBlocker{}, false
	}
	code := "governed_dependencies_unavailable"
	if problem.Code == "conflict" || problem.Code == "invalid" {
		code = "governed_dependencies_incomplete"
	}
	return readinessBlocker(code, "blocked", problem.Message, []string{"published_rule_pack", "required_governed_knowledge"}, "补齐并发布适用规则与强制制度后重新检查"), true
}

func readinessMaterialSelection(st state, caseID string) (int64, bool) {
	for _, chain := range st.MaterialChains {
		if chain.CaseID == caseID {
			return chain.SelectionVersion, chain.LegacySelectionStatus != "pending_confirmation"
		}
	}
	return 0, true
}

func readinessMaterialsForFrozen(st state, docs []FrozenDocument) []ReviewReadinessMaterial {
	entityByDocument := make(map[string]MaterialEntity)
	for _, entity := range st.Materials {
		entityByDocument[entity.CurrentDocumentID] = entity
	}
	materials := make([]ReviewReadinessMaterial, 0, len(docs))
	for _, frozen := range docs {
		material := ReviewReadinessMaterial{IdentityKind: "legacy_document", Version: 0, DocumentID: frozen.Document.ID,
			Filename: frozen.Document.Filename, Role: frozen.Document.Role, Selected: true}
		if entity, ok := entityByDocument[frozen.Document.ID]; ok {
			material.IdentityKind, material.MaterialID, material.Version = "material_entity", entity.ID, entity.CurrentVersion
			material.Role, material.Purpose = entity.Role, entity.Purpose
		}
		parseStatus := "pending"
		if frozen.ParsedStatus == "failed" {
			parseStatus = "failed"
		} else if frozen.ParsedStatus == "ready" && frozen.Ready {
			parseStatus = "ready"
		} else if frozen.ParsedDocumentID != "" && !frozen.Ready {
			parseStatus = "failed"
		}
		material.Parse = ReviewReadinessParse{Status: parseStatus, ParsedDocumentID: frozen.ParsedDocumentID,
			ParserID: frozen.ParserID, ParserVersion: frozen.ParserVersion}
		materials = append(materials, material)
	}
	sort.Slice(materials, func(i, j int) bool {
		if materials[i].MaterialID != materials[j].MaterialID {
			return materials[i].MaterialID < materials[j].MaterialID
		}
		return materials[i].DocumentID < materials[j].DocumentID
	})
	return materials
}

func currentReadinessMaterials(ctx context.Context, s *Store, st state, caseID string, selectionConfirmed bool) ([]ReviewReadinessMaterial, []Document) {
	selected := selectedMaterialDocuments(st, caseID)
	if !selectionConfirmed {
		for _, chain := range st.MaterialChains {
			if chain.CaseID != caseID {
				continue
			}
			selected = st.documentsByIDs(caseID, chain.SelectedDocumentIDs)
			break
		}
	}
	entityByDocument := make(map[string]MaterialEntity)
	for _, entity := range st.Materials {
		if entity.CaseID == caseID {
			entityByDocument[entity.CurrentDocumentID] = entity
		}
	}
	models := s.currentModels()
	materials := make([]ReviewReadinessMaterial, 0, len(selected))
	for _, doc := range selected {
		item := ReviewReadinessMaterial{IdentityKind: "legacy_document", Version: 0, DocumentID: doc.ID,
			Filename: doc.Filename, Role: doc.Role, Selected: selectionConfirmed}
		if entity, ok := entityByDocument[doc.ID]; ok {
			item.IdentityKind, item.MaterialID, item.Version, item.Role, item.Purpose = "material_entity", entity.ID, entity.CurrentVersion, entity.Role, entity.Purpose
		} else {
			if purpose, err := resolveMaterialPurpose(doc.Role, ""); err == nil {
				item.Purpose = purpose
			}
		}
		parserID, parserVersion, configHash := parserConfigurationFor(doc, models)
		parsed, found := st.latestParsed(doc.ID, configHash)
		if !found {
			if prior, exists := st.latestParsed(doc.ID, ""); exists {
				item.Parse = ReviewReadinessParse{Status: "stale", ParsedDocumentID: prior.ID,
					ParserID: prior.ParserID, ParserVersion: prior.ParserVersion, NextAction: "reparse_material"}
			} else {
				item.Parse = ReviewReadinessParse{Status: "pending", ParserID: parserID,
					ParserVersion: parserVersion, NextAction: "wait_for_parse_or_start_review"}
			}
		} else {
			item.Parse = ReviewReadinessParse{Status: parsed.Status, ParsedDocumentID: parsed.ID,
				ParserID: parsed.ParserID, ParserVersion: parsed.ParserVersion}
			if parsed.Status == "ready" {
				if err := s.verifyParsedRecordContext(ctx, parsed, doc); err != nil {
					item.Parse.Status, item.Parse.NextAction = "failed", "reparse_material"
				} else {
					item.Parse.NextAction = "ready_for_review"
				}
			} else {
				item.Parse.Status, item.Parse.NextAction = "failed", "reparse_material"
			}
		}
		materials = append(materials, item)
	}
	sort.Slice(materials, func(i, j int) bool {
		if materials[i].MaterialID != materials[j].MaterialID {
			return materials[i].MaterialID < materials[j].MaterialID
		}
		return materials[i].DocumentID < materials[j].DocumentID
	})
	return materials, selected
}

func reviewReadinessRuleChecks(pack *ActiveRulePack, knowledge *ReviewKnowledgeSnapshot, materials []ReviewReadinessMaterial, caseItem Case) []ReviewReadinessCheck {
	if pack == nil {
		return []ReviewReadinessCheck{}
	}
	parsed, err := rulepack.Parse(pack.Snapshot)
	if err != nil {
		return []ReviewReadinessCheck{{RuleID: "rule_pack", Status: "not_executed", MissingDependencies: []string{"valid_rule_pack"}, NextAction: "联系治理管理员修复规则包"}}
	}
	gateByRule := map[string][]rulepack.AuthorityGate{}
	if knowledge != nil {
		gates, gateErr := rulepackAuthorityGates(knowledge)
		if gateErr == nil {
			for _, gate := range gates {
				gateByRule[gate.RuleID] = append(gateByRule[gate.RuleID], gate)
			}
		}
	}
	roleState := map[string]string{}
	for _, material := range materials {
		if !material.Selected {
			continue
		}
		previous, exists := roleState[material.Role]
		if !exists || (previous != "ready" && material.Parse.Status == "ready") {
			roleState[material.Role] = material.Parse.Status
		}
	}
	manifest := parsed.Manifest()
	checks := make([]ReviewReadinessCheck, 0, len(manifest.Rules))
	for _, rule := range manifest.Rules {
		if rule.BusinessScope.BusinessLine != caseItem.BusinessLine || rule.BusinessScope.Scenario != caseItem.Scenario || rule.BusinessScope.ContractType != caseItem.ContractType {
			continue
		}
		check := ReviewReadinessCheck{RuleID: rule.RuleID, Status: "ready", MissingDependencies: []string{}, NextAction: "start_review"}
		for _, role := range rule.RequiredFact.SourceRoles {
			status, ok := roleState[role]
			if !ok {
				check.MissingDependencies = append(check.MissingDependencies, "material_role:"+role)
				check.Status = "not_executed"
				continue
			}
			if status != "ready" {
				dependency := "parsed_material_role:" + role
				if status == "failed" || status == "stale" {
					dependency = "failed_or_stale_parse_role:" + role
					check.NextAction = "reparse_material"
				}
				check.MissingDependencies = append(check.MissingDependencies, dependency)
				if check.Status != "missing_basis" {
					check.Status = "not_executed"
				}
			}
		}
		gates := gateByRule[rule.RuleID]
		normativeSources := normativeSourceKeys(rule.AuthoritySources)
		gateState, missingGate := readinessAuthorityGateState(normativeSources, gates)
		if missingGate {
			check.MissingDependencies = append(check.MissingDependencies, "exact_legal_authority_gate")
		}
		if gateState == "authority_gap" || gateState == "facts_unknown" {
			for _, gate := range gates {
				if _, exact := normativeSources[gate.SourceID+"\x00"+gate.SourceVersion]; !exact {
					continue
				}
				if gate.ReferenceMissing || gate.Outcome == "unknown" {
					check.MissingDependencies = append(check.MissingDependencies, "legal_authority:"+gate.SourceID+"@"+gate.SourceVersion)
				}
			}
		}
		if gateState == "authority_gap" || gateState == "facts_unknown" {
			check.Status = "missing_basis"
			check.NextAction = "provide_missing_material_or_legal_basis"
		} else if gateState == "not_applicable" {
			check.Status, check.NextAction = "not_applicable", "no_action"
			check.MissingDependencies = []string{}
		} else if check.Status == "not_executed" || check.Status == "missing_basis" {
			if check.NextAction != "reparse_material" {
				check.NextAction = "provide_missing_material_or_legal_basis"
			}
		}
		sort.Strings(check.MissingDependencies)
		checks = append(checks, check)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].RuleID < checks[j].RuleID })
	return checks
}

func reviewAdmissionError(blockers []ReviewReadinessBlocker) error {
	for _, blocker := range blockers {
		switch blocker.Code {
		case "material_selection_pending":
			return fault.Conflict("历史材料集合待确认，请先确认本次审查材料")
		case "required_materials_missing", "primary_contract_required", "primary_contract_ambiguous":
			return fault.Invalid(blocker.Message)
		case "business_at_required":
			return fault.Invalid("必须提供带时区的业务时间")
		case "review_start_role_required", "review_permission_required":
			return fault.Forbidden(blocker.Message)
		case "case_not_writable":
			return fault.Conflict(blocker.Message)
		}
	}
	return nil
}

func normativeSourceKeys(sources []rulepack.AuthoritySource) map[string]struct{} {
	keys := make(map[string]struct{})
	for _, source := range sources {
		if source.Kind == "normative" {
			keys[source.ID+"\x00"+source.Version] = struct{}{}
		}
	}
	return keys
}

func readinessAuthorityGateState(sources map[string]struct{}, gates []rulepack.AuthorityGate) (string, bool) {
	if len(sources) == 0 {
		return "", false
	}
	matched := make(map[string]struct{}, len(gates))
	for _, gate := range gates {
		matched[gate.SourceID+"\x00"+gate.SourceVersion] = struct{}{}
	}
	for source := range sources {
		if _, ok := matched[source]; !ok {
			return "authority_gap", true
		}
	}
	unknown, notApplicable := false, false
	for _, gate := range gates {
		if _, exact := sources[gate.SourceID+"\x00"+gate.SourceVersion]; !exact {
			continue
		}
		if gate.ReferenceMissing {
			return "authority_gap", false
		}
		switch gate.Outcome {
		case "not_applicable":
			notApplicable = true
		case "unknown":
			unknown = true
		}
	}
	if notApplicable {
		return "not_applicable", false
	}
	if unknown {
		return "facts_unknown", false
	}
	return "applicable", false
}
