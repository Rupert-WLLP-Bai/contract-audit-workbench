package store

import (
	"context"
	contractapp "contractaudit/internal/contract/app"
	"encoding/json"
	"errors"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	contract "contractaudit/internal/contract/model"
	contractpostgres "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	platformpostgres "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

func freshClauseUser(current accounts.User, item Case, action access.Action) (accounts.User, error) {
	fresh, err := trustedUser(current)
	if err != nil || (action == access.Decide && !accounts.CanDecide(fresh.Role)) || !authorizeCase(item, fresh, action) {
		return accounts.User{}, fault.NotFound("当前授权不能读取条款建议")
	}
	return fresh, nil
}

// CreateClauseProposal pins a suggestion to the requested frozen attempt.
func (s *Store) CreateClauseProposal(ctx context.Context, user accounts.User, input contract.ClauseProposalCreate) (contract.ClauseProposal, error) {
	ctx = commandContext(ctx, user)
	if s.serviceOwner != "" && s.serviceOwner != "core" {
		return contract.ClauseProposal{}, fault.Forbidden("当前服务不能创建条款建议")
	}
	currentUser, err := s.remediationDecisionUser(user, "只读模式下不能创建条款建议", "当前角色不能创建条款建议")
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if err := contract.ValidateClauseProposalCreate(input); err != nil {
		return contract.ClauseProposal{}, fault.Invalid(err.Error())
	}
	if err := contract.ValidateCLMIdempotencyKey(input.IdempotencyKey); err != nil {
		return contract.ClauseProposal{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	digest := sha256Bytes(raw)
	tx, current, item, err := s.beginExperienceCase(ctx, currentUser, input.CaseID, access.Decide)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	defer tx.Rollback(context.Background())
	if err := businessWritable(item); err != nil {
		return contract.ClauseProposal{}, err
	}
	repo := contractpostgres.NewClauseProposals(platformpostgres.Bind(tx))
	if err := repo.LockReceipt(ctx, item.TenantID, current.ID, "clm.clause_proposal.create", input.IdempotencyKey); err != nil {
		return contract.ClauseProposal{}, err
	}
	id, old, found, err := repo.FindCreate(ctx, item.TenantID, current.ID, input.IdempotencyKey)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if found {
		if old != digest {
			return contract.ClauseProposal{}, fault.Conflict("Idempotency-Key已用于不同请求载荷")
		}
		p, err := repo.LoadCreated(ctx, item.TenantID, item.OwnerOrgID, item.ID, id)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		if _, err := freshClauseUser(current, item, access.Decide); err != nil {
			return contract.ClauseProposal{}, err
		}
		if err = tx.Commit(ctx); err != nil {
			return contract.ClauseProposal{}, err
		}
		return p, nil
	}
	var documents string
	var inputHash string
	var knowledge []byte
	var pack contract.ClauseRulePackRef
	err = tx.QueryRow(ctx, `SELECT i.input_hash,i.documents,s.rule_pack_id,s.rule_pack_version,s.rule_pack_hash,s.rule_pack_digest,s.rule_pack_compiler_ref,s.governed_knowledge FROM review_runs r JOIN review_attempts a ON a.tenant_id=r.tenant_id AND a.org_id=r.org_id AND a.run_id=r.id JOIN review_attempt_inputs i ON i.tenant_id=a.tenant_id AND i.org_id=a.org_id AND i.run_id=r.id AND i.attempt_id=a.id AND i.input_hash=a.input_hash JOIN review_run_snapshots s ON s.tenant_id=r.tenant_id AND s.org_id=r.org_id AND s.run_id=r.id WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5 AND a.history_kind='frozen' AND a.input_hash<>''`, item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID).Scan(&inputHash, &documents, &pack.ID, &pack.Version, &pack.ContentHash, &pack.SnapshotDigest, &pack.CompilerRef, &knowledge)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.ClauseProposal{}, fault.NotFound("没有找到对应的冻结执行记录")
	}
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	if input.FindingID != "" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM findings WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5 AND id=$6 UNION ALL SELECT 1 FROM manual_finding_records WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND run_id=$4 AND attempt_id=$5 AND id=$6)`, item.TenantID, item.OwnerOrgID, item.ID, input.RunID, input.AttemptID, input.FindingID).Scan(&exists)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		if !exists {
			return contract.ClauseProposal{}, fault.Invalid("发现不属于此冻结执行")
		}
	}
	if input.RemediationID != "" {
		var exists bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_remediations WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4 AND run_id=$5 AND attempt_id=$6 AND ($7='' OR $7=ANY(finding_ids)))`, item.TenantID, item.OwnerOrgID, item.ID, input.RemediationID, input.RunID, input.AttemptID, input.FindingID).Scan(&exists)
		if err != nil {
			return contract.ClauseProposal{}, err
		}
		if !exists {
			return contract.ClauseProposal{}, fault.Invalid("整改与该案件或发现不匹配")
		}
	}
	id, err = newID("clause_proposal_")
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	p := contract.ClauseProposal{ID: id, CreatedBy: current.ID, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), CaseID: item.ID, RunID: input.RunID, AttemptID: input.AttemptID, InputHash: inputHash, FindingID: input.FindingID, Evidence: input.Evidence, ProposedText: input.ProposedText, Reason: input.Reason, RemediationID: input.RemediationID, Status: "pending", Version: 1, History: []contract.ClauseProposalHistory{}, UnavailableReason: "缺少可核验的冻结原文引用"}
	if pack.ID != "" {
		p.RulePack = &pack
	}
	if json.Valid(knowledge) {
		var snapshot struct {
			Adopted *struct {
				Ref contract.ClausePlaybookRef `json:"ref"`
			} `json:"adopted_playbook"`
		}
		if json.Unmarshal(knowledge, &snapshot) == nil && snapshot.Adopted != nil {
			p.Playbook = &snapshot.Adopted.Ref
		}
	}
	var frozen []FrozenDocument
	if err = json.Unmarshal([]byte(documents), &frozen); err != nil {
		return contract.ClauseProposal{}, err
	}
	if ref := input.Evidence; ref != nil && ref.Start >= 0 && ref.End > ref.Start && ref.Quote != "" {
		e := ExperienceEvidenceRef{DocumentID: ref.DocumentID, ParsedDocumentID: ref.ParsedDocumentID, NormalizedDigest: ref.NormalizedDigest, Start: ref.Start, End: ref.End, Quote: ref.Quote}
		if verifyFeedbackEvidence(ctx, tx, item, input.RunID, input.AttemptID, frozen, e) == nil {
			for _, f := range frozen {
				if f.Document.ID == ref.DocumentID {
					r := []rune(f.Text)
					start := ref.Start - 120
					if start < 0 {
						start = 0
					}
					end := ref.End + 120
					if end > len(r) {
						end = len(r)
					}
					p.OriginalText = ref.Quote
					p.BeforeContext = string(r[start:ref.Start])
					p.AfterContext = string(r[ref.End:end])
					p.EvidenceVerified = true
					p.UnavailableReason = ""
					break
				}
			}
		}
	}
	if err := repo.Create(ctx, item.TenantID, item.OwnerOrgID, current.ID, input.IdempotencyKey, digest, p); err != nil {
		return contract.ClauseProposal{}, err
	}
	if _, err := freshClauseUser(current, item, access.Decide); err != nil {
		return contract.ClauseProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.ClauseProposal{}, err
	}
	return p, nil
}

func (s *Store) ListClauseProposals(ctx context.Context, user accounts.User, caseID string) ([]contract.ClauseProposal, error) {
	ctx = commandContext(ctx, user)
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Read)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(context.Background())
	repo := contractpostgres.NewClauseProposals(platformpostgres.Bind(tx))
	ids, err := repo.ListIDs(ctx, item.TenantID, item.OwnerOrgID, item.ID)
	if err != nil {
		return nil, err
	}
	result := make([]contract.ClauseProposal, 0, len(ids))
	for _, id := range ids {
		p, err := repo.LoadProposal(ctx, item.TenantID, item.OwnerOrgID, item.ID, id, false)
		if err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	if _, err := freshClauseUser(current, item, access.Read); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func frozenClauseText(ctx context.Context, tx pgx.Tx, item Case, proposal contract.ClauseProposal) (string, error) {
	if proposal.Evidence == nil || !proposal.EvidenceVerified {
		return "", fault.Conflict("缺少可核验的冻结原文引用")
	}
	var documents, hash string
	err := tx.QueryRow(ctx, `SELECT i.input_hash,i.documents FROM review_attempt_inputs i JOIN review_attempts a ON a.tenant_id=i.tenant_id AND a.org_id=i.org_id AND a.run_id=i.run_id AND a.id=i.attempt_id AND a.input_hash=i.input_hash JOIN review_runs r ON r.tenant_id=a.tenant_id AND r.org_id=a.org_id AND r.id=a.run_id WHERE r.tenant_id=$1 AND r.org_id=$2 AND r.case_id=$3 AND r.id=$4 AND a.id=$5 AND a.history_kind='frozen'`, item.TenantID, item.OwnerOrgID, item.ID, proposal.RunID, proposal.AttemptID).Scan(&hash, &documents)
	if err != nil {
		return "", err
	}
	if hash != proposal.InputHash {
		return "", fault.Conflict("冻结输入摘要已变化")
	}
	var frozen []FrozenDocument
	if err = json.Unmarshal([]byte(documents), &frozen); err != nil {
		return "", err
	}
	ref := proposal.Evidence
	if ref.Start < 0 || ref.End <= ref.Start {
		return "", fault.Conflict("冻结引用范围无效")
	}
	e := ExperienceEvidenceRef{DocumentID: ref.DocumentID, ParsedDocumentID: ref.ParsedDocumentID, NormalizedDigest: ref.NormalizedDigest, Start: ref.Start, End: ref.End, Quote: ref.Quote}
	if err := verifyFeedbackEvidence(ctx, tx, item, proposal.RunID, proposal.AttemptID, frozen, e); err != nil {
		return "", err
	}
	for _, source := range frozen {
		if source.Document.ID == ref.DocumentID {
			return source.Text, nil
		}
	}
	return "", fault.Conflict("冻结原文不可用")
}

func (s *Store) DerivedClauseText(ctx context.Context, user accounts.User, caseID, proposalID string, expectedVersion int64, editedText *string) (string, error) {
	ctx = commandContext(ctx, user)
	tx, current, item, err := s.beginExperienceCase(ctx, user, caseID, access.Decide)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(context.Background())
	repo := contractpostgres.NewClauseProposals(platformpostgres.Bind(tx))
	proposal, err := repo.LoadProposal(ctx, item.TenantID, item.OwnerOrgID, item.ID, proposalID, false)
	if err != nil {
		return "", err
	}
	text, err := frozenClauseText(ctx, tx, item, proposal)
	if err != nil {
		return "", err
	}
	derived, err := contract.DerivedClauseText(proposal, expectedVersion, text, editedText)
	if err != nil {
		return "", fault.Conflict(err.Error())
	}
	if _, err := freshClauseUser(current, item, access.Decide); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return derived, nil
}

type clauseAcceptanceBridge struct {
	store *Store
	tx    pgx.Tx
	user  accounts.User
	item  Case
}

func (b clauseAcceptanceBridge) AuthorizeDecision(ctx context.Context, proposalID string, input contract.ClauseProposalDecision) error {
	if !accounts.CanDecide(b.user.Role) || !authorizeCase(b.item, b.user, access.Decide) {
		return fault.Forbidden("当前授权不能处理条款建议")
	}
	if err := businessWritable(b.item); err != nil {
		return err
	}
	if input.Action != "reject" {
		record, err := loadCLMContract(ctx, b.tx, b.item.TenantID, input.ContractID)
		if err != nil {
			return err
		}
		if !authorizeContractRead(ctx, b.tx, record, b.user) {
			return fault.NotFound("没有找到对应记录")
		}
		if !authorizeContract(record, b.user, access.Manage) {
			return fault.Forbidden("当前授权不能修改合同")
		}
	}
	return nil
}
func (b clauseAcceptanceBridge) FrozenText(ctx context.Context, p contract.ClauseProposal) (string, error) {
	return frozenClauseText(ctx, b.tx, b.item, p)
}
func (b clauseAcceptanceBridge) MaterialText(ctx context.Context, p contract.ClauseProposal, input contract.ClauseProposalDecision) (string, error) {
	var previous, digest, payload, parsedID, originalDigest, artifact, normalized string
	var selectionOrder int64
	var media, backend, parserID, parserVersion, configHash, normalization, createdAt, status string
	var irDigest *string
	var size int64
	err := b.tx.QueryRow(ctx, `SELECT mv.previous_document_id,d.digest,p.payload,p.id,p.original_digest,p.artifact_digest,p.normalized_digest,p.selection_order,d.media_type,d.storage_backend,d.size,p.parser_id,p.parser_version,p.config_hash,p.normalization_version,p.created_at,p.status,p.ir_digest FROM material_versions mv JOIN documents d ON d.tenant_id=mv.tenant_id AND d.org_id=mv.org_id AND d.case_id=mv.case_id AND d.id=mv.document_id JOIN LATERAL (SELECT payload,status,id,original_digest,artifact_digest,normalized_digest,selection_order,parser_id,parser_version,config_hash,normalization_version,created_at,ir_digest FROM parsed_documents WHERE tenant_id=mv.tenant_id AND org_id=mv.org_id AND document_id=mv.document_id ORDER BY selection_order DESC,seq ASC LIMIT 1) p ON p.status='ready' WHERE mv.tenant_id=$1 AND mv.org_id=$2 AND mv.case_id=$3 AND mv.material_id=$4 AND mv.document_id=$5 AND EXISTS(SELECT 1 FROM material_versions old WHERE old.tenant_id=mv.tenant_id AND old.org_id=mv.org_id AND old.case_id=mv.case_id AND old.material_id=mv.material_id AND old.document_id=mv.previous_document_id)`, b.item.TenantID, b.item.OwnerOrgID, b.item.ID, input.MaterialID, input.DocumentID).Scan(&previous, &digest, &payload, &parsedID, &originalDigest, &artifact, &normalized, &selectionOrder, &media, &backend, &size, &parserID, &parserVersion, &configHash, &normalization, &createdAt, &status, &irDigest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fault.Conflict("上传材料尚未可靠解析或不属于此案件")
	}
	if err != nil {
		return "", err
	}
	if previous != p.Evidence.DocumentID || digest != input.SHA256 {
		return "", fault.Conflict("上传材料与原材料或摘要不匹配")
	}
	parsed, err := parsePayload(payload)
	if err != nil {
		return "", err
	}
	if parsed.Status != "ready" || status != parsed.Status || parsed.ParserID != parserID || parsed.ParserVersion != parserVersion || parsed.ConfigHash != configHash || parsed.NormalizationVersion != normalization || parsed.CreatedAt != createdAt || irDigest == nil || parsed.IRDigest != *irDigest || parsed.ID != parsedID || parsed.OriginalDigest != originalDigest || originalDigest != digest || parsed.ArtifactDigest != artifact || parsed.NormalizedDigest != normalized || parsed.SelectionOrder != selectionOrder || verifyParsed(parsed, Document{ID: input.DocumentID, Digest: digest}) != nil {
		return "", fault.Conflict("上传材料解析结果不可核验")
	}
	if media != "text/plain" {
		return "", fault.Conflict("修改稿必须是派生纯文本")
	}
	bytes, err := b.store.readClauseOriginal(ctx, Document{ID: input.DocumentID, TenantID: b.item.TenantID, OrgID: b.item.OwnerOrgID, CaseID: b.item.ID, Digest: digest, Size: size, StorageBackend: backend})
	if err != nil {
		var problem *fault.Error
		if errors.As(err, &problem) {
			return "", problem
		}
		if errors.Is(err, errObjectIntegrity) {
			return "", fault.Conflict("修改稿原始字节无法核验")
		}
		return "", fault.New(503, "document_unavailable", "材料服务暂不可用")
	}
	if string(bytes) != parsed.Text {
		return "", fault.Conflict("修改稿原始字节与解析全文不一致")
	}
	return string(bytes), nil
}
func (b clauseAcceptanceBridge) CreateRevision(ctx context.Context, p contract.ClauseProposal, input contract.ClauseProposalDecision, digest string) (string, error) {
	raw, err := loadContractDraftBytes(ctx, b.tx, b.item.TenantID, input.ContractID)
	if err != nil {
		return "", err
	}
	draft, err := (storeDraftCodec{}).Open(ctx, b.item.TenantID, input.ContractID, raw)
	if err != nil {
		return "", err
	}
	var originalDigest string
	if err := b.tx.QueryRow(ctx, `SELECT digest FROM documents WHERE tenant_id=$1 AND org_id=$2 AND case_id=$3 AND id=$4`, b.item.TenantID, b.item.OwnerOrgID, b.item.ID, p.Evidence.DocumentID).Scan(&originalDigest); err != nil {
		return "", err
	}
	found := 0
	for i := range draft.Materials {
		m := &draft.Materials[i]
		if m.MaterialID == input.MaterialID && m.MaterialVersionID == p.Evidence.DocumentID && m.SHA256 == originalDigest {
			m.MaterialVersionID = input.DocumentID
			m.SHA256 = input.SHA256
			found++
		}
	}
	if found != 1 {
		return "", fault.Conflict("合同草稿未唯一引用该原材料版本")
	}
	prepared, err := contractapp.PrepareReplaceDraft(contract.ReplaceContractDraftInput{ContractID: input.ContractID, ExpectedRowVersion: input.ExpectedContractRowVersion, Draft: draft, IdempotencyKey: "proposal-replace-" + digest})
	if err != nil {
		return "", err
	}
	principal := contractapp.CreateDraftPrincipal{TenantID: b.item.TenantID, ActorID: b.user.ID, OrgID: b.user.OrgID}
	result, err := contractapp.ReplaceDraft(ctx, principal, prepared, contractpostgres.NewDraftCommands(platformpostgres.Bind(b.tx)), storeDraftCodec{}, storeDraftAccess{tx: b.tx, user: b.user}, storeDraftWorkflowReader{tx: b.tx, user: b.user})
	if err != nil {
		return "", err
	}
	freeze, err := contractapp.PrepareFreezeVersion(contract.FreezeContractVersionInput{ContractID: input.ContractID, ExpectedRowVersion: result.Summary.RowVersion, CaseID: b.item.ID, IdempotencyKey: "proposal-freeze-" + digest})
	if err != nil {
		return "", err
	}
	version, err := contractapp.FreezeVersion(ctx, contractapp.VersionPrincipal{TenantID: b.item.TenantID, ActorID: b.user.ID, OrgID: b.user.OrgID}, freeze, contractpostgres.NewVersionCommands(platformpostgres.Bind(b.tx)), storeVersionCodec{}, storeDraftAccess{tx: b.tx, user: b.user}, storeDraftWorkflowReader{tx: b.tx, user: b.user}, storeVersionReferenceResolver{tx: b.tx, user: b.user}, storeVersionEventAdapter{tx: b.tx}, storeVersionEventAdapter{tx: b.tx})
	if err != nil {
		return "", err
	}
	return version.CurrentRevisionID, nil
}

func (s *Store) DecideClauseProposal(ctx context.Context, user accounts.User, caseID, proposalID string, input contract.ClauseProposalDecision) (contract.ClauseProposal, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能处理条款建议", nil)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	defer rollbackCLM(tx)
	if err := scopedCaseLock(ctx, tx, caseID); err != nil {
		return contract.ClauseProposal{}, err
	}
	var org string
	if err := tx.QueryRow(ctx, `SELECT org_id FROM cases WHERE tenant_id=$1 AND id=$2`, accounts.Tenant(current), caseID).Scan(&org); err != nil {
		return contract.ClauseProposal{}, err
	}
	item, err := loadCLMCase(ctx, tx, current, org, caseID)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	scope := userScope(current)
	scope.CaseID = caseID
	scope.OrgID = org
	if err := setLocalScope(ctx, tx, scope); err != nil {
		return contract.ClauseProposal{}, err
	}
	bridge := clauseAcceptanceBridge{store: s, tx: tx, user: current, item: item}
	repo := contractpostgres.NewClauseProposals(platformpostgres.Bind(tx))
	result, err := contractapp.DecideClauseProposal(ctx, contractapp.ClauseProposalPrincipal{TenantID: item.TenantID, OrgID: item.OwnerOrgID, CaseID: item.ID, ActorID: current.ID}, proposalID, input, repo, bridge, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	fresh, err := freshClauseUser(current, item, access.Decide)
	if err != nil {
		return contract.ClauseProposal{}, err
	}
	bridge.user = fresh
	if err := bridge.AuthorizeDecision(ctx, proposalID, input); err != nil {
		return contract.ClauseProposal{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.ClauseProposal{}, err
	}
	return result, nil
}
