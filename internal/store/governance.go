package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"

	"github.com/jackc/pgx/v5"
)

var (
	errGovernanceVersionMissing      = errors.New("rule pack version is unavailable")
	errGovernancePointerMissing      = errors.New("rule pack active pointer is unavailable")
	errGovernanceIdempotencyConflict = errors.New("idempotency key was already used with a different request")
)

// trustedAdmin resolves the verified admin identity and refuses any caller whose
// session role is not the trusted platform admin role. Tenant and org always come
// from the directory entry, never from the request body.
func trustedAdmin(user accounts.User) (accounts.User, error) {
	current, err := trustedUser(user)
	if err != nil {
		return accounts.User{}, err
	}
	if current.Role != "admin" {
		return accounts.User{}, fault.Forbidden("需要平台管理权限")
	}
	return current, nil
}

// governanceScope is the trusted transaction scope for one admin mutation.
func governanceScope(admin accounts.User) dbScope {
	scope := userScope(admin)
	scope.Role = "admin"
	return scope
}

// parseGovernedManifest strictly parses a client manifest and rejects any
// submitted lifecycle attestation: governance records are server-owned, so a
// draft is created without attestations and a published pack cannot be imported
// through the write API.
func parseGovernedManifest(raw []byte) (*rulepack.Pack, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fault.Invalid("规则包内容不能为空")
	}
	if len(raw) > maxGovernanceManifestBytes {
		return nil, fault.TooLarge("规则包内容超过 1MB")
	}
	pack, err := rulepack.Parse(raw)
	if err != nil {
		return nil, fault.Invalid("规则包格式或内容不合法：" + err.Error())
	}
	lifecycle := pack.Manifest().Lifecycle
	if lifecycle.State != rulepack.StateDraft {
		return nil, fault.Invalid("规则包必须处于 draft 状态，不能导入已校验、已批准或已发布的生命周期")
	}
	if lifecycle.Verification != nil || lifecycle.Approval != nil || lifecycle.Release != nil {
		return nil, fault.Invalid("规则包不能携带校验、批准或发布记录")
	}
	if len(pack.Manifest().Rules) == 0 {
		return nil, fault.Invalid("规则包至少需要一条规则")
	}
	if len(pack.Manifest().Rules) > maxGovernanceRules {
		return nil, fault.Invalid(fmt.Sprintf("规则数量超过上限 %d", maxGovernanceRules))
	}
	return pack, nil
}

// governanceRow is one persisted version joined with its pointer state.
type governanceRow struct {
	Version         RulePackVersion
	Active          bool
	PointerRevision int64
	PointerVersion  string
	pointerExists   bool
	pointerActive   bool
}

func (row governanceRow) summary() RulePackSummary {
	return RulePackSummary{
		ID: row.Version.ID, Version: row.Version.Version, TenantID: row.Version.TenantID,
		Status: row.Version.Status, Revision: row.Version.Revision, ContentHash: row.Version.ContentHash,
		SnapshotSHA256: row.Version.SnapshotDigest, EffectiveFrom: row.Version.EffectiveFrom,
		EffectiveUntil: row.Version.EffectiveUntil, AuthoritySources: append([]string(nil), row.Version.AuthoritySources...),
		IsActive: row.Active, ActivePointerRevision: row.PointerRevision,
	}
}

// loadGovernanceRow reads one version with its pointer. The pointer is optional:
// an unpublished or disabled version has no active pointer row.
func loadGovernanceRow(ctx context.Context, tx pgx.Tx, tenantID, packID, version string) (governanceRow, error) {
	row := governanceRow{}
	var authority []string
	err := tx.QueryRow(ctx, `SELECT tenant_id, pack_id, version, org_id, status, revision, manifest, content_hash,
		snapshot_digest, effective_from, effective_until, authority_sources, approval_actor_id, approved_at,
		published_at, published_by, created_at, updated_at FROM rule_pack_versions WHERE tenant_id=$1 AND pack_id=$2 AND version=$3`,
		tenantID, packID, version).Scan(&row.Version.TenantID, &row.Version.ID, &row.Version.Version, &row.Version.OrgID,
		&row.Version.Status, &row.Version.Revision, &row.Version.Manifest, &row.Version.ContentHash, &row.Version.SnapshotDigest,
		&row.Version.EffectiveFrom, &row.Version.EffectiveUntil, &authority, &row.Version.ApprovalActorID, &row.Version.ApprovedAt,
		&row.Version.PublishedAt, &row.Version.PublishedBy, &row.Version.CreatedAt, &row.Version.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return governanceRow{}, errGovernanceVersionMissing
		}
		return governanceRow{}, err
	}
	row.Version.AuthoritySources = authority
	pointerVersion := ""
	var pointerActive bool
	if err := tx.QueryRow(ctx, `SELECT version, active, pointer_revision FROM rule_pack_pointers WHERE tenant_id=$1 AND pack_id=$2`,
		tenantID, packID).Scan(&pointerVersion, &pointerActive, &row.PointerRevision); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return governanceRow{}, err
		}
	} else {
		row.pointerExists = true
		row.PointerVersion = pointerVersion
		row.pointerActive = pointerActive
		row.Active = pointerActive && pointerVersion == version && row.Version.Status == GovernancePublished
	}
	return row, nil
}

// countGovernanceVersions keeps the persisted version list bounded per pack.
func countGovernanceVersions(ctx context.Context, tx pgx.Tx, tenantID, packID string) (int, error) {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM rule_pack_versions WHERE tenant_id=$1 AND pack_id=$2`, tenantID, packID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// insertGovernanceHistory appends one immutable history row. Callers append it in
// the same transaction as the mutation it records.
func insertGovernanceHistory(ctx context.Context, tx pgx.Tx, entry RulePackHistoryEntry) error {
	if entry.Sequence != 0 {
		return errors.New("history sequence is allocated by the database")
	}
	var seq int64
	if err := tx.QueryRow(ctx, `INSERT INTO rule_pack_history (tenant_id, pack_id, version, action, actor_id, content_hash, pointer_revision, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING seq`,
		entry.TenantID, entry.PackID, entry.Version, entry.Action, entry.ActorID, entry.ContentHash, entry.PointerRevision, entry.CreatedAt).Scan(&seq); err != nil {
		return err
	}
	return nil
}

// currentGovernancePointerRevision reads the pointer revision a CAS request must
// name. A missing pointer is revision 0, which is what a first publish names.
func currentGovernancePointerRevision(ctx context.Context, tx pgx.Tx, tenantID, packID string) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT pointer_revision FROM rule_pack_pointers WHERE tenant_id=$1 AND pack_id=$2`, tenantID, packID).Scan(&revision); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return revision, nil
}

// lockGovernancePack serialises every write for one pack inside the transaction.
func lockGovernancePack(ctx context.Context, tx pgx.Tx, tenantID, packID string) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "rule-pack:"+tenantID+":"+packID); err != nil {
		return err
	}
	return nil
}

// governanceIdempotencyKey is the hash over the exact request payload a caller
// replayed. Same key plus same payload returns the stored response; same key with
// a different payload conflicts.
func governanceIdempotencyKey(request any) (string, error) {
	raw, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// lookupGovernanceIdempotency returns the stored response for an identical
// replay. A different payload under the same key conflicts.
func lookupGovernanceIdempotency(ctx context.Context, tx pgx.Tx, tenantID, actorID, key, requestDigest string) ([]byte, bool, error) {
	var storedDigest string
	var response []byte
	if err := tx.QueryRow(ctx, `SELECT request_digest, response_json FROM rule_pack_idempotency WHERE tenant_id=$1 AND actor_id=$2 AND idem_key=$3`,
		tenantID, actorID, key).Scan(&storedDigest, &response); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if storedDigest != requestDigest {
		return nil, false, errGovernanceIdempotencyConflict
	}
	return response, true, nil
}

func storeGovernanceIdempotency(ctx context.Context, tx pgx.Tx, tenantID, actorID, key, requestDigest string, response []byte) error {
	raw, err := json.Marshal(json.RawMessage(response))
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO rule_pack_idempotency (tenant_id, actor_id, idem_key, request_digest, response_json, created_at)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (tenant_id, actor_id, idem_key) DO NOTHING`,
		tenantID, actorID, key, requestDigest, raw, stamp()); err != nil {
		return err
	}
	return nil
}

// nowUTC is the single clock used by governance writes so history and version
// timestamps stay ordered inside one transaction.
func nowUTC() string {
	return time.Now().UTC().Format(timeLayout)
}

func governanceVersionNumber(version string) (uint64, error) {
	value, err := strconv.ParseUint(strings.TrimSpace(version), 10, 64)
	if err != nil {
		return 0, fault.Invalid("规则包版本必须是正整数字符串")
	}
	return value, nil
}

func sortSummaries(items []RulePackSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].ID != items[j].ID {
			return items[i].ID < items[j].ID
		}
		return items[i].Version < items[j].Version
	})
}
