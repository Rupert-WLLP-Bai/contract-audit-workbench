package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"

	"github.com/jackc/pgx/v5"
)

// RulePackSummary is the bounded public summary of one persisted version.
type RulePackSummary struct {
	ID                    string   `json:"id"`
	Version               string   `json:"version"`
	TenantID              string   `json:"tenant_id"`
	Status                string   `json:"status"`
	Revision              int64    `json:"revision"`
	ContentHash           string   `json:"content_hash"`
	SnapshotSHA256        string   `json:"snapshot_sha256"`
	EffectiveFrom         string   `json:"effective_from"`
	EffectiveUntil        string   `json:"effective_until"`
	AuthoritySources      []string `json:"authority_sources"`
	IsActive              bool     `json:"is_active"`
	ActivePointerRevision int64    `json:"active_pointer_revision"`
}

// RulePackDetail is the exact persisted manifest plus its summary.
type RulePackDetail struct {
	Pack     RulePackSummary `json:"pack"`
	Manifest json.RawMessage `json:"manifest"`
}

// RulePackHistory is the append-only governance record for one pack.
type RulePackHistory struct {
	Items []RulePackHistoryEntry `json:"items"`
}

// RulePackList is the bounded list of a tenant's pack versions.
type RulePackList struct {
	Items []RulePackSummary `json:"items"`
}

// ActiveRulePack is the immutable identity a review acceptance freezes.
type ActiveRulePack struct {
	PackID          string `json:"id"`
	Version         string `json:"version"`
	ContentHash     string `json:"content_sha256"`
	SnapshotDigest  string `json:"snapshot_sha256"`
	Snapshot        []byte `json:"-"`
	CompilerMarker  string `json:"-"`
	PointerRevision int64  `json:"pointer_revision"`
}

// CreateRulePack stores a new draft version. The manifest tenant must equal the
// trusted admin tenant, and a duplicate version is refused.
func (s *Store) CreateRulePack(user accounts.User, manifest []byte) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	pack, err := parseGovernedManifest(manifest)
	if err != nil {
		return RulePackDetail{}, err
	}
	content := pack.Manifest()
	if content.TenantID != accounts.Tenant(admin) {
		return RulePackDetail{}, fault.Invalid("规则包租户与当前登录租户不一致")
	}
	if !validIdentifier(content.ID) {
		return RulePackDetail{}, fault.Invalid("规则包 ID 不合法")
	}
	if _, err := governanceVersionNumber(content.Version); err != nil {
		return RulePackDetail{}, err
	}
	// The stored snapshot digest is the digest of the exact draft manifest bytes
	// a validation must re-derive. Lifecycle transitions then replace both the
	// stored manifest and its digest together.
	draftSnapshot, err := pack.Snapshot()
	if err != nil {
		return RulePackDetail{}, err
	}
	digest := sha256Bytes(draftSnapshot)
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), content.ID); err != nil {
			return err
		}
		count, err := countGovernanceVersions(context.Background(), tx, accounts.Tenant(admin), content.ID)
		if err != nil {
			return err
		}
		if count >= maxGovernancePackVersions {
			return fault.Invalid(fmt.Sprintf("规则包版本数量超过上限 %d", maxGovernancePackVersions))
		}
		if _, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), content.ID, content.Version); err == nil {
			return fault.Conflict("该规则包版本已存在")
		} else if !errors.Is(err, errGovernanceVersionMissing) {
			return err
		}
		now := nowUTC()
		if _, err := tx.Exec(context.Background(), `INSERT INTO rule_pack_versions
			(tenant_id, pack_id, version, org_id, status, revision, manifest, content_hash, snapshot_digest,
			 effective_from, effective_until, authority_sources, approval_actor_id, approved_at, published_at, published_by, created_at, updated_at)
			VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$9,$10,$11,'','','','',$12,$12)`,
			accounts.Tenant(admin), content.ID, content.Version, admin.OrgID, GovernanceDraft,
			draftSnapshot, pack.Hash(), digest, content.EffectiveFrom, content.EffectiveUntil, authorityList(content), now); err != nil {
			return err
		}
		pointerRevision, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), content.ID)
		if err != nil {
			return err
		}
		if err := insertGovernanceHistory(context.Background(), tx, RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: content.ID, Version: content.Version,
			Action: GovernanceActionCreate, ActorID: admin.ID, ContentHash: pack.Hash(),
			PointerRevision: pointerRevision, CreatedAt: now,
		}); err != nil {
			return err
		}
		created, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), content.ID, content.Version)
		if err != nil {
			return err
		}
		detail = RulePackDetail{Pack: created.summary(), Manifest: append([]byte(nil), created.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// EditRulePackVersion replaces draft content. Any edit resets validation and
// approval, because the new content hash differs from the attested hash.
func (s *Store) EditRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	pack, err := parseGovernedManifest(input.Manifest)
	if err != nil {
		return RulePackDetail{}, err
	}
	content := pack.Manifest()
	if content.TenantID != accounts.Tenant(admin) {
		return RulePackDetail{}, fault.Invalid("规则包租户与当前登录租户不一致")
	}
	if !validIdentifier(content.ID) || content.ID != input.PackID {
		return RulePackDetail{}, fault.Invalid("规则包 ID 与请求路径不一致")
	}
	if content.Version != input.Version {
		return RulePackDetail{}, fault.Invalid("规则包版本与请求路径不一致")
	}
	if _, err := governanceVersionNumber(content.Version); err != nil {
		return RulePackDetail{}, err
	}
	draftSnapshot, err := pack.Snapshot()
	if err != nil {
		return RulePackDetail{}, err
	}
	digest := sha256Bytes(draftSnapshot)
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), content.ID); err != nil {
			return err
		}
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), content.ID, content.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		if row.Version.Status != GovernanceDraft {
			return fault.Conflict("只有 draft 状态的规则包版本可以编辑内容")
		}
		if row.Version.Revision != input.ExpectedRevision {
			return fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		now := nowUTC()
		if _, err := tx.Exec(context.Background(), `UPDATE rule_pack_versions SET manifest=$1, content_hash=$2, snapshot_digest=$3,
			effective_from=$4, effective_until=$5, authority_sources=$6, approval_actor_id='', approved_at='', revision=revision+1, updated_at=$7
			WHERE tenant_id=$8 AND pack_id=$9 AND version=$10 AND revision=$11`,
			draftSnapshot, pack.Hash(), digest, content.EffectiveFrom, content.EffectiveUntil, authorityList(content), now,
			accounts.Tenant(admin), content.ID, content.Version, input.ExpectedRevision); err != nil {
			return err
		}
		pointerRevision, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), content.ID)
		if err != nil {
			return err
		}
		if err := insertGovernanceHistory(context.Background(), tx, RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: content.ID, Version: content.Version,
			Action: GovernanceActionEdit, ActorID: admin.ID, ContentHash: pack.Hash(),
			PointerRevision: pointerRevision, CreatedAt: now,
		}); err != nil {
			return err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), content.ID, content.Version)
		if err != nil {
			return err
		}
		detail = RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// ValidateRulePackVersion records the deterministic verifier record bound to the
// current content hash.
func (s *Store) ValidateRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), input.PackID); err != nil {
			return err
		}
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		if row.Version.Status != GovernanceDraft {
			return fault.Conflict("只有 draft 状态的规则包版本可以校验")
		}
		if row.Version.Revision != input.ExpectedRevision {
			return fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		// Re-parse the stored bytes so a corrupted row can never be validated.
		pack, err := rulepack.Parse(row.Version.Manifest)
		if err != nil {
			return fault.Conflict("规则包内容已损坏，不能校验")
		}
		validated, err := pack.Validate()
		if err != nil {
			return fault.Conflict("规则包校验失败")
		}
		if validated.Hash() != row.Version.ContentHash {
			return fault.Conflict("规则包内容摘要与记录不一致，不能校验")
		}
		// The stored digest is the draft snapshot digest; the validated snapshot
		// legitimately adds the verifier record. Both must parse to the same
		// content hash, which is what binds the lifecycle to the content.
		draftSnapshot, err := pack.Snapshot()
		if err != nil {
			return err
		}
		if sha256Bytes(draftSnapshot) != row.Version.SnapshotDigest {
			return fault.Conflict("规则包快照摘要与记录不一致，不能校验")
		}
		snapshot, err := validated.Snapshot()
		if err != nil {
			return err
		}
		now := nowUTC()
		if _, err := tx.Exec(context.Background(), `UPDATE rule_pack_versions SET status=$1, manifest=$2, snapshot_digest=$3, revision=revision+1, updated_at=$4
			WHERE tenant_id=$5 AND pack_id=$6 AND version=$7 AND revision=$8`,
			GovernanceValidated, snapshot, sha256Bytes(snapshot), now,
			accounts.Tenant(admin), input.PackID, input.Version, input.ExpectedRevision); err != nil {
			return err
		}
		pointerRevision, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return err
		}
		if err := insertGovernanceHistory(context.Background(), tx, RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionValidate, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: now,
		}); err != nil {
			return err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			return err
		}
		detail = RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// ApproveRulePackVersion binds the trusted admin identity to the exact content
// hash. A content edit invalidates this approval because the hash changes.
func (s *Store) ApproveRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), input.PackID); err != nil {
			return err
		}
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		if row.Version.Status != GovernanceValidated {
			return fault.Conflict("只有 validated 状态的规则包版本可以批准")
		}
		if row.Version.Revision != input.ExpectedRevision {
			return fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		if input.ExpectedContentHash != row.Version.ContentHash {
			return fault.Conflict("规则包内容已变化，原批准失效，请重新校验后再批准")
		}
		pack, err := rulepack.Parse(row.Version.Manifest)
		if err != nil {
			return fault.Conflict("规则包内容已损坏，不能批准")
		}
		approved, err := pack.Approve(admin.ID, row.Version.ContentHash)
		if err != nil {
			return fault.Conflict("规则包批准失败")
		}
		snapshot, err := approved.Snapshot()
		if err != nil {
			return err
		}
		now := nowUTC()
		if _, err := tx.Exec(context.Background(), `UPDATE rule_pack_versions SET status=$1, manifest=$2, snapshot_digest=$3, approval_actor_id=$4, approved_at=$5, revision=revision+1, updated_at=$6
			WHERE tenant_id=$7 AND pack_id=$8 AND version=$9 AND revision=$10`,
			GovernanceApproved, snapshot, sha256Bytes(snapshot), admin.ID, now, now,
			accounts.Tenant(admin), input.PackID, input.Version, input.ExpectedRevision); err != nil {
			return err
		}
		pointerRevision, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return err
		}
		if err := insertGovernanceHistory(context.Background(), tx, RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionApprove, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: now,
		}); err != nil {
			return err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			return err
		}
		detail = RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// PublishRulePackVersion makes an approved version immutable and moves the
// per-pack active pointer. The pointer CAS and pack revision CAS both apply.
func (s *Store) PublishRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	return s.governanceIdempotentReplay(admin, input, map[string]any{"action": "publish", "pack": input.PackID, "version": input.Version, "expected_revision": input.ExpectedRevision, "expected_pointer_revision": input.ExpectedPointerRevision}, func(tx pgx.Tx) (RulePackDetail, error) {
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return RulePackDetail{}, fault.NotFound("没有找到对应规则包版本")
			}
			return RulePackDetail{}, err
		}
		if row.Version.Status != GovernanceApproved {
			return RulePackDetail{}, fault.Conflict("只有 approved 状态的规则包版本可以发布")
		}
		if row.Version.Revision != input.ExpectedRevision {
			return RulePackDetail{}, fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		currentPointer, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return RulePackDetail{}, err
		}
		if currentPointer != input.ExpectedPointerRevision {
			return RulePackDetail{}, fault.Conflict("规则包启用指针已被其他操作修改，请刷新后重试")
		}
		// Version rollback guard: the published version must be the greatest
		// persisted version of this pack unless it is already the active one.
		if row.PointerVersion != "" && row.PointerVersion != input.Version {
			pointerNumber, err := governanceVersionNumber(row.PointerVersion)
			if err != nil {
				return RulePackDetail{}, err
			}
			targetNumber, err := governanceVersionNumber(input.Version)
			if err != nil {
				return RulePackDetail{}, err
			}
			if targetNumber < pointerNumber {
				return RulePackDetail{}, fault.Conflict("不能通过发布操作回退到旧版本，请使用回滚操作")
			}
		}
		pack, err := rulepack.Parse(row.Version.Manifest)
		if err != nil {
			return RulePackDetail{}, fault.Conflict("规则包内容已损坏，不能发布")
		}
		published, err := pack.Publish()
		if err != nil {
			return RulePackDetail{}, fault.Conflict("规则包发布失败")
		}
		snapshot, err := published.Snapshot()
		if err != nil {
			return RulePackDetail{}, err
		}
		now := nowUTC()
		if _, err := tx.Exec(context.Background(), `UPDATE rule_pack_versions SET status=$1, manifest=$2, snapshot_digest=$3, published_at=$4, published_by=$5, revision=revision+1, updated_at=$4
			WHERE tenant_id=$6 AND pack_id=$7 AND version=$8 AND revision=$9`,
			GovernancePublished, snapshot, sha256Bytes(snapshot), now, admin.ID,
			accounts.Tenant(admin), input.PackID, input.Version, input.ExpectedRevision); err != nil {
			return RulePackDetail{}, err
		}
		pointerRevision := currentPointer + 1
		transition, err := moveRulePackPointer(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version, true, pointerRevision, now, admin.ID, currentPointer, false)
		if err != nil {
			return RulePackDetail{}, err
		}
		historyEntry := RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionPublish, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: now,
		}
		historyEntry.recordPointerTransition(transition)
		if err := insertGovernanceHistory(context.Background(), tx, historyEntry); err != nil {
			return RulePackDetail{}, err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			return RulePackDetail{}, err
		}
		return RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}, nil
	})
}

// DisableRulePackVersion clears the active pointer without deleting the
// published version or changing its bytes.
func (s *Store) DisableRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), input.PackID); err != nil {
			return err
		}
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		if row.Version.Status != GovernancePublished {
			return fault.Conflict("只有 published 状态的规则包版本可以停用")
		}
		if row.Version.Revision != input.ExpectedRevision {
			return fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		// Only the currently active version may be disabled: disabling an older
		// published version must never clear the pointer of the live one.
		if !row.pointerExists || !row.pointerActive || row.PointerVersion != input.Version {
			return fault.Conflict("该规则包版本不是当前启用版本，不能停用")
		}
		currentPointer, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return err
		}
		if currentPointer != input.ExpectedPointerRevision {
			return fault.Conflict("规则包启用指针已被其他操作修改，请刷新后重试")
		}
		now := nowUTC()
		pointerRevision := currentPointer + 1
		// Tombstone, never delete: the revision keeps increasing so a stale CAS
		// captured before this disable can never match again.
		transition, err := moveRulePackPointer(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version, false, pointerRevision, now, admin.ID, currentPointer, true)
		if err != nil {
			return err
		}
		historyEntry := RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionDisable, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: now,
		}
		historyEntry.recordPointerTransition(transition)
		if err := insertGovernanceHistory(context.Background(), tx, historyEntry); err != nil {
			return err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			return err
		}
		detail = RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// RollbackRulePackVersion moves the pointer to an exact already published
// version. Published bytes and history are never edited.
func (s *Store) RollbackRulePackVersion(user accounts.User, input GovernanceInput) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	return s.governanceIdempotentReplay(admin, input, map[string]any{"action": "rollback", "pack": input.PackID, "version": input.Version, "expected_pointer_revision": input.ExpectedPointerRevision}, func(tx pgx.Tx) (RulePackDetail, error) {
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return RulePackDetail{}, fault.NotFound("没有找到对应规则包版本")
			}
			return RulePackDetail{}, err
		}
		if row.Version.Status != GovernancePublished {
			return RulePackDetail{}, fault.Conflict("只能回滚到已发布的规则包版本")
		}
		if row.pointerExists && row.pointerActive && row.PointerVersion == input.Version {
			return RulePackDetail{}, fault.Conflict("该规则包版本已是当前启用版本")
		}
		currentPointer, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return RulePackDetail{}, err
		}
		if currentPointer != input.ExpectedPointerRevision {
			return RulePackDetail{}, fault.Conflict("规则包启用指针已被其他操作修改，请刷新后重试")
		}
		now := nowUTC()
		pointerRevision := currentPointer + 1
		transition, err := moveRulePackPointer(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version, true, pointerRevision, now, admin.ID, currentPointer, false)
		if err != nil {
			return RulePackDetail{}, err
		}
		historyEntry := RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionRollback, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: now,
		}
		historyEntry.recordPointerTransition(transition)
		if err := insertGovernanceHistory(context.Background(), tx, historyEntry); err != nil {
			return RulePackDetail{}, err
		}
		updated, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			return RulePackDetail{}, err
		}
		return RulePackDetail{Pack: updated.summary(), Manifest: append([]byte(nil), updated.Version.Manifest...)}, nil
	})
}

// ListRulePackVersions returns the tenant's persisted versions with pointer state.
func (s *Store) ListRulePackVersions(user accounts.User) (RulePackList, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackList{}, err
	}
	var list RulePackList
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT v.tenant_id, v.pack_id, v.version, v.org_id, v.status, v.revision, v.manifest,
			v.content_hash, v.snapshot_digest, v.effective_from, v.effective_until, v.authority_sources,
			COALESCE(p.version,''), COALESCE(p.active,false), COALESCE(p.pointer_revision,0)
			FROM rule_pack_versions v
			LEFT JOIN rule_pack_pointers p ON p.tenant_id=v.tenant_id AND p.pack_id=v.pack_id
			WHERE v.tenant_id=$1 ORDER BY v.pack_id, v.version`, accounts.Tenant(admin))
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			row := governanceRow{}
			var authority []string
			if err := rows.Scan(&row.Version.TenantID, &row.Version.ID, &row.Version.Version, &row.Version.OrgID,
				&row.Version.Status, &row.Version.Revision, &row.Version.Manifest, &row.Version.ContentHash,
				&row.Version.SnapshotDigest, &row.Version.EffectiveFrom, &row.Version.EffectiveUntil, &authority,
				&row.PointerVersion, &row.pointerActive, &row.PointerRevision); err != nil {
				return err
			}
			row.Version.AuthoritySources = authority
			row.pointerExists = row.PointerVersion != ""
			// is_active must reflect the pointer's active flag, so a disabled
			// tombstone is never reported active in the list while Get and
			// acceptance correctly treat it as inactive.
			row.Active = row.pointerExists && row.pointerActive && row.PointerVersion == row.Version.Version && row.Version.Status == GovernancePublished
			list.Items = append(list.Items, row.summary())
		}
		return rows.Err()
	})
	if err != nil {
		return RulePackList{}, err
	}
	if len(list.Items) > maxGovernancePackVersions {
		return RulePackList{}, fault.Invalid(fmt.Sprintf("规则包版本数量超过上限 %d", maxGovernancePackVersions))
	}
	sortSummaries(list.Items)
	if list.Items == nil {
		list.Items = []RulePackSummary{}
	}
	return list, nil
}

// GetRulePackVersion returns one exact persisted version and manifest.
func (s *Store) GetRulePackVersion(user accounts.User, packID, version string) (RulePackDetail, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackDetail{}, err
	}
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), packID, version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		detail = RulePackDetail{Pack: row.summary(), Manifest: append([]byte(nil), row.Version.Manifest...)}
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}

// RulePackHistory returns the bounded append-only governance history.
func (s *Store) RulePackHistory(user accounts.User, packID string) (RulePackHistory, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackHistory{}, err
	}
	var history RulePackHistory
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		rows, err := tx.Query(context.Background(), `SELECT seq, tenant_id, pack_id, version, action, actor_id, content_hash, pointer_revision, created_at,
			previous_version, previous_active, resulting_version, resulting_active
			FROM rule_pack_history WHERE tenant_id=$1 AND pack_id=$2 ORDER BY seq DESC LIMIT $3`,
			accounts.Tenant(admin), packID, maxGovernanceHistoryEntries)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var entry RulePackHistoryEntry
			if err := rows.Scan(&entry.Sequence, &entry.TenantID, &entry.PackID, &entry.Version, &entry.Action,
				&entry.ActorID, &entry.ContentHash, &entry.PointerRevision, &entry.CreatedAt,
				&entry.PreviousVersion, &entry.PreviousActive, &entry.ResultingVersion, &entry.ResultingActive); err != nil {
				return err
			}
			history.Items = append(history.Items, entry)
		}
		return rows.Err()
	})
	if err != nil {
		return RulePackHistory{}, err
	}
	if history.Items == nil {
		history.Items = []RulePackHistoryEntry{}
	}
	sort.Slice(history.Items, func(i, j int) bool { return history.Items[i].Sequence < history.Items[j].Sequence })
	return history, nil
}

// selectActiveRulePackTx resolves the single effective active published pack
// inside the caller's transaction. The acceptance writer uses it so the pack it
// freezes is read under the same transaction and locks as the run/attempt write.
func selectActiveRulePackTx(ctx context.Context, tx pgx.Tx, tenantID, businessLine, scenario, contractType string, acceptanceTime time.Time) (*ActiveRulePack, error) {
	rows, err := tx.Query(ctx, `SELECT p.pack_id, p.version, p.pointer_revision, v.manifest, v.content_hash, v.snapshot_digest
		FROM rule_pack_pointers p
		JOIN rule_pack_versions v ON v.tenant_id=p.tenant_id AND v.pack_id=p.pack_id AND v.version=p.version
		WHERE p.tenant_id=$1 AND v.status=$2 AND p.active
			AND (convert_from(v.manifest,'UTF8')::jsonb->>'effective_from')::timestamptz <= $3::timestamptz
			AND $3::timestamptz < (convert_from(v.manifest,'UTF8')::jsonb->>'effective_until')::timestamptz
		ORDER BY p.pack_id`, tenantID, GovernancePublished, acceptanceTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var candidates []ActiveRulePack
	for rows.Next() {
		var candidate ActiveRulePack
		var manifest []byte
		var storedContentHash, storedSnapshotDigest string
		if err := rows.Scan(&candidate.PackID, &candidate.Version, &candidate.PointerRevision, &manifest, &storedContentHash, &storedSnapshotDigest); err != nil {
			return nil, err
		}
		candidate.Snapshot = append([]byte(nil), manifest...)
		pack, err := rulepack.Parse(candidate.Snapshot)
		if err != nil {
			return nil, fault.Conflict("已发布规则包内容损坏，不能用于审查")
		}
		content := pack.Manifest()
		if content.Lifecycle.State != rulepack.StatePublished {
			continue
		}
		if pack.Hash() != storedContentHash || sha256Bytes(candidate.Snapshot) != storedSnapshotDigest {
			return nil, fault.Conflict("已发布规则包内容摘要与记录不一致，不能用于审查")
		}
		from, err := time.Parse(time.RFC3339Nano, content.EffectiveFrom)
		if err != nil {
			return nil, fault.Conflict("已发布规则包生效时间不合法")
		}
		until, err := time.Parse(time.RFC3339Nano, content.EffectiveUntil)
		if err != nil {
			return nil, fault.Conflict("已发布规则包失效时间不合法")
		}
		if acceptanceTime.Before(from.UTC()) || !acceptanceTime.Before(until.UTC()) {
			continue
		}
		matched := false
		for _, rule := range content.Rules {
			if rule.BusinessScope.BusinessLine == businessLine && rule.BusinessScope.Scenario == scenario && rule.BusinessScope.ContractType == contractType {
				matched = true
				break
			}
		}
		if matched {
			candidate.ContentHash = storedContentHash
			candidate.SnapshotDigest = storedSnapshotDigest
			candidate.CompilerMarker = governedCompilerMarker
			candidates = append(candidates, candidate)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) > 1 {
		return nil, fault.Conflict("存在多个适用的已发布规则包，请先停用多余的启用指针")
	}
	return &candidates[0], nil
}

// SelectActiveRulePack resolves the single effective active published pack for
// one trusted case scope and acceptance time. No match is not an error: the
// caller keeps the synthetic baseline adapter. Multiple matches fail closed.
func (s *Store) SelectActiveRulePack(ctx context.Context, tenantID, businessLine, scenario, contractType, at string) (*ActiveRulePack, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if !validIdentifier(businessLine) || !validIdentifier(scenario) || !validIdentifier(contractType) || at == "" {
		return nil, fault.Invalid("案件业务范围不完整，不能选择规则包")
	}
	acceptanceTime, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(at))
	if err != nil {
		return nil, fault.Invalid("受理时间格式不正确，不能选择规则包")
	}
	acceptanceTime = acceptanceTime.UTC()
	var candidates []ActiveRulePack
	err = s.updateAdminAs(accounts.User{ID: "system", Role: "admin", TenantID: tenantID}, func(tx pgx.Tx) error {
		// The SQL window filter is only a coarse prefilter: PostgreSQL compares the
		// timestamptz form, which is correct for any zone or fractional-second
		// spelling. The authoritative comparison is the typed one below.
		rows, err := tx.Query(ctx, `SELECT p.pack_id, p.version, p.pointer_revision, v.manifest, v.content_hash, v.snapshot_digest
			FROM rule_pack_pointers p
			JOIN rule_pack_versions v ON v.tenant_id=p.tenant_id AND v.pack_id=p.pack_id AND v.version=p.version
			WHERE p.tenant_id=$1 AND v.status=$2 AND p.active
				AND (convert_from(v.manifest,'UTF8')::jsonb->>'effective_from')::timestamptz <= $3::timestamptz
				AND $3::timestamptz < (convert_from(v.manifest,'UTF8')::jsonb->>'effective_until')::timestamptz
			ORDER BY p.pack_id`, tenantID, GovernancePublished, acceptanceTime)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var candidate ActiveRulePack
			var manifest []byte
			var storedContentHash, storedSnapshotDigest string
			if err := rows.Scan(&candidate.PackID, &candidate.Version, &candidate.PointerRevision, &manifest, &storedContentHash, &storedSnapshotDigest); err != nil {
				return err
			}
			candidate.Snapshot = append([]byte(nil), manifest...)
			pack, err := rulepack.Parse(candidate.Snapshot)
			if err != nil {
				return fault.Conflict("已发布规则包内容损坏，不能用于审查")
			}
			content := pack.Manifest()
			if content.Lifecycle.State != rulepack.StatePublished {
				continue
			}
			// The stored identity must match the stored bytes exactly. A mismatch is
			// a corrupted row and is refused, never silently rewritten into a new
			// identity.
			actualDigest := sha256Bytes(candidate.Snapshot)
			if pack.Hash() != storedContentHash || actualDigest != storedSnapshotDigest {
				return fault.Conflict("已发布规则包内容摘要与记录不一致，不能用于审查")
			}
			candidate.ContentHash = storedContentHash
			candidate.SnapshotDigest = storedSnapshotDigest
			// Typed effective-window comparison so a non-UTC or fractional-second
			// policy is accepted exactly as written.
			from, err := time.Parse(time.RFC3339Nano, content.EffectiveFrom)
			if err != nil {
				return fault.Conflict("已发布规则包生效时间不合法")
			}
			until, err := time.Parse(time.RFC3339Nano, content.EffectiveUntil)
			if err != nil {
				return fault.Conflict("已发布规则包失效时间不合法")
			}
			if acceptanceTime.Before(from.UTC()) || !acceptanceTime.Before(until.UTC()) {
				continue
			}
			matched := false
			for _, rule := range content.Rules {
				if rule.BusinessScope.BusinessLine == businessLine && rule.BusinessScope.Scenario == scenario && rule.BusinessScope.ContractType == contractType {
					matched = true
					break
				}
			}
			if matched {
				candidate.CompilerMarker = governedCompilerMarker
				candidates = append(candidates, candidate)
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) > 1 {
		return nil, fault.Conflict("存在多个适用的已发布规则包，请先停用多余的启用指针")
	}
	return &candidates[0], nil
}

// TestRulePackVersion executes one bounded synthetic sample against a temporary
// in-memory published copy of the exact requested revision. No persisted
// lifecycle transition happens and no case or document is read.
func (s *Store) TestRulePackVersion(user accounts.User, input RulePackTestInput) (RulePackTestResult, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackTestResult{}, err
	}
	if err := validateRulePackTestInput(input); err != nil {
		return RulePackTestResult{}, fault.Invalid(err.Error())
	}
	var result RulePackTestResult
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		row, err := loadGovernanceRow(context.Background(), tx, accounts.Tenant(admin), input.PackID, input.Version)
		if err != nil {
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对应规则包版本")
			}
			return err
		}
		if row.Version.Revision != input.ExpectedRevision {
			return fault.Conflict("规则包版本已被其他操作修改，请刷新后重试")
		}
		// Editing, testing, then approving is the intended workflow, so a draft,
		// validated or approved version can be tested. The lifecycle transitions
		// needed to execute are applied to a temporary in-memory copy under a
		// test-only actor; nothing is persisted and no real approval is implied.
		pack, err := rulepack.Parse(row.Version.Manifest)
		if err != nil {
			return fault.Conflict("规则包内容已损坏，不能试算")
		}
		if row.Version.Status == GovernanceDraft {
			if pack, err = pack.Validate(); err != nil {
				return fault.Conflict("规则包内容不能通过校验，无法试算")
			}
		}
		if pack.Manifest().Lifecycle.State == rulepack.StateValidated {
			if pack, err = pack.Approve("rulepack-sample-test", pack.Hash()); err != nil {
				return fault.Conflict("规则包试算副本创建失败")
			}
		}
		if pack.Manifest().Lifecycle.State != rulepack.StatePublished {
			if pack, err = pack.Publish(); err != nil {
				return fault.Conflict("规则包试算副本创建失败")
			}
		}
		request, err := buildRulePackTestRequest(accounts.Tenant(admin), input)
		if err != nil {
			return err
		}
		report, err := pack.Execute(context.Background(), request)
		if err != nil {
			return fault.Invalid("规则包试算失败：" + err.Error())
		}
		pointerRevision, err := currentGovernancePointerRevision(context.Background(), tx, accounts.Tenant(admin), input.PackID)
		if err != nil {
			return err
		}
		if err := insertGovernanceHistory(context.Background(), tx, RulePackHistoryEntry{
			TenantID: accounts.Tenant(admin), PackID: input.PackID, Version: input.Version,
			Action: GovernanceActionTest, ActorID: admin.ID, ContentHash: row.Version.ContentHash,
			PointerRevision: pointerRevision, CreatedAt: nowUTC(),
		}); err != nil {
			return err
		}
		result = RulePackTestResult{Revision: row.Version.Revision, ContentHash: row.Version.ContentHash, Report: report}
		return nil
	})
	if err != nil {
		return RulePackTestResult{}, err
	}
	return result, nil
}

func authorityList(content rulepack.Manifest) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, rule := range content.Rules {
		for _, authority := range rule.AuthoritySources {
			key := authority.ID + "@" + authority.Version + ":" + authority.Kind
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func sha256Bytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validIdentifier(value string) bool { return rulepackValidIdentifier(value) }

// rulepackValidIdentifier reuses the rulepack identifier rule without exporting it.
func rulepackValidIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case i > 0 && (c == '.' || c == '_' || c == ':' || c == '/' || c == '-'):
		default:
			return false
		}
	}
	return true
}

// governedCompilerMarker marks acceptance-frozen admin-published bytes so the
// worker never recompiles or re-selects a pack for an accepted attempt.
const governedCompilerMarker = "governed-rulepack/v1"

// ParseFrozenGovernedRulePack validates and parses the exact published pack
// frozen on an accepted attempt. It never queries the active pointer, so a
// publish, rollback or disable after acceptance cannot change that run. A
// synthetic attempt with no governed-pack identity returns (nil, nil); a
// partial, unknown, or corrupt identity returns an error. The caller must
// independently ensure attempt.RunID belongs to caseItem through its trusted
// run/attempt lookup.
func ParseFrozenGovernedRulePack(attempt Attempt, caseItem Case) (*rulepack.Pack, error) {
	hasPackIdentity := attempt.RulePackID != "" || attempt.RulePackVersion != "" ||
		attempt.RulePackPointerRevision != 0 || attempt.RulePackHash != "" ||
		attempt.RulePackSnapshotDigest != "" || len(attempt.RulePackSnapshot) != 0
	switch attempt.RulePackCompilerRef {
	case "", telecom.CompilerRef:
		if !hasPackIdentity {
			if isCloudServicesCase(caseItem) {
				return nil, errors.New("cloud review has no frozen governed RulePack")
			}
			return nil, nil
		}
		return nil, errors.New("synthetic attempt carries governed RulePack identity")
	case governedCompilerMarker:
	default:
		return nil, errors.New("attempt has an unknown RulePack compiler identity")
	}
	if !isGovernedReviewCase(caseItem) || caseItem.TenantID == "" || caseItem.ContractType != "service" {
		return nil, errors.New("frozen governed RulePack is outside the case scope")
	}
	if attempt.RulePackID == "" || attempt.RulePackVersion == "" || attempt.RulePackPointerRevision < 1 ||
		attempt.RulePackHash == "" || attempt.RulePackSnapshotDigest == "" || len(attempt.RulePackSnapshot) == 0 {
		return nil, errors.New("frozen governed RulePack identity is incomplete")
	}
	pack, err := rulepack.Parse(attempt.RulePackSnapshot)
	if err != nil {
		return nil, fmt.Errorf("frozen governed RulePack cannot be parsed: %w", err)
	}
	manifest := pack.Manifest()
	if manifest.Lifecycle.State != rulepack.StatePublished || manifest.ID != attempt.RulePackID ||
		manifest.Version != attempt.RulePackVersion || manifest.TenantID != caseItem.TenantID {
		return nil, errors.New("frozen governed RulePack identity or tenant does not match its attempt")
	}
	for _, rule := range manifest.Rules {
		if rule.TenantID != caseItem.TenantID || rule.BusinessScope.BusinessLine != caseItem.BusinessLine ||
			rule.BusinessScope.Scenario != caseItem.Scenario || rule.BusinessScope.ContractType != caseItem.ContractType {
			return nil, errors.New("frozen governed RulePack rule is outside the case tenant or business scope")
		}
	}
	if pack.Hash() != attempt.RulePackHash || sha256Bytes(attempt.RulePackSnapshot) != attempt.RulePackSnapshotDigest {
		return nil, errors.New("frozen governed RulePack hashes do not match its bytes")
	}
	if err := requireGovernedCapabilitiesForCase(caseItem, attempt.RulePackSnapshot); err != nil {
		return nil, fmt.Errorf("frozen governed RulePack violates mandatory coverage: %w", err)
	}
	return pack, nil
}

// governanceIdempotentReplay returns the stored response for an identical replay
// of one keyed mutation, or executes fn and stores its result. Authorization is
// always rechecked by the caller before this runs, so a replay can never bypass
// a revoked grant.
func (s *Store) governanceIdempotentReplay(admin accounts.User, input GovernanceInput, request any, fn func(pgx.Tx) (RulePackDetail, error)) (RulePackDetail, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		var detail RulePackDetail
		err := s.updateAdminAs(admin, func(tx pgx.Tx) error {
			var inner error
			detail, inner = fn(tx)
			return inner
		})
		return detail, err
	}
	digest, err := governanceIdempotencyKey(request)
	if err != nil {
		return RulePackDetail{}, err
	}
	var detail RulePackDetail
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		if err := lockGovernancePack(context.Background(), tx, accounts.Tenant(admin), input.PackID); err != nil {
			return err
		}
		stored, found, err := lookupGovernanceIdempotency(context.Background(), tx, accounts.Tenant(admin), admin.ID, input.IdempotencyKey, digest)
		if err != nil {
			return err
		}
		if found {
			if err := json.Unmarshal(stored, &detail); err != nil {
				return err
			}
			return nil
		}
		result, err := fn(tx)
		if err != nil {
			return err
		}
		response, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := storeGovernanceIdempotency(context.Background(), tx, accounts.Tenant(admin), admin.ID, input.IdempotencyKey, digest, response); err != nil {
			return err
		}
		detail = result
		return nil
	})
	if err != nil {
		return RulePackDetail{}, err
	}
	return detail, nil
}
