// File metadata (manifest mirror):
// Purpose: 实现 `TenantConfigRef`, `TenantConfigSelection`, `TenantConfigCatalogEntry`, `TenantModelCatalogEntry` 等 Store 数据结构与事务操作，按租户和组织范围读写或校验持久状态。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-04
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

const tenantCredentialRef = "platform/llm-endpoint/active"

type TenantConfigRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type TenantConfigSelection struct {
	PublicTemplateRef TenantConfigRef   `json:"public_template_ref"`
	PrivatePolicyRefs []TenantConfigRef `json:"private_policy_refs"`
	ModelCatalogRef   string            `json:"model_catalog_ref"`
	CredentialRef     *string           `json:"credential_ref"`
	EgressMode        string            `json:"egress_mode"`
}

type TenantConfigCatalogEntry struct {
	TenantConfigRef
	Title string `json:"title"`
	Kind  string `json:"kind"`
}

type TenantModelCatalogEntry struct {
	ID            string `json:"id"`
	Revision      string `json:"revision"`
	ProviderLabel string `json:"provider_label"`
	ChatModel     string `json:"chat_model"`
	OCRModel      string `json:"ocr_model"`
}

type AdoptedTenantConfig struct {
	ConfigVersion     string                     `json:"config_version"`
	VersionID         string                     `json:"version_id"`
	Digest            string                     `json:"digest"`
	PublicTemplate    TenantConfigCatalogEntry   `json:"public_template"`
	PrivatePolicyRefs []TenantConfigCatalogEntry `json:"private_policy_refs"`
	ModelCatalogRef   string                     `json:"model_catalog_ref"`
	CredentialRef     *string                    `json:"credential_ref"`
	EgressMode        string                     `json:"egress_mode"`
	AdoptedAt         string                     `json:"adopted_at"`
	AdoptedBy         string                     `json:"adopted_by"`
}

type TenantConfigLiveControl struct {
	Enabled   bool   `json:"enabled"`
	Revision  string `json:"revision"`
	UpdatedAt string `json:"updated_at"`
}

type TenantConfigView struct {
	SchemaVersion         string                     `json:"schema_version"`
	TenantID              string                     `json:"tenant_id"`
	Revision              string                     `json:"revision"`
	Current               *AdoptedTenantConfig       `json:"current"`
	PublicTemplateCatalog []TenantConfigCatalogEntry `json:"public_template_catalog"`
	PrivatePolicyCatalog  []TenantConfigCatalogEntry `json:"private_policy_catalog"`
	ModelCatalog          []TenantModelCatalogEntry  `json:"model_catalog"`
	CredentialRefs        []map[string]string        `json:"credential_refs"`
	LiveControl           TenantConfigLiveControl    `json:"live_control"`
}

type TenantConfigDiff struct {
	Path   string  `json:"path"`
	Change string  `json:"change"`
	Before *string `json:"before"`
	After  *string `json:"after"`
}

type TenantConfigValidationError struct {
	Path string `json:"path"`
	Code string `json:"code"`
}

type TenantConfigPreview struct {
	Valid  bool                          `json:"valid"`
	Diff   []TenantConfigDiff            `json:"diff"`
	Errors []TenantConfigValidationError `json:"errors"`
}

type TenantConfigAdoptionResult struct {
	Revision      string              `json:"revision"`
	AdoptedConfig AdoptedTenantConfig `json:"adopted_config"`
}

type TenantLiveControlResult struct {
	Revision string `json:"revision"`
	Enabled  bool   `json:"enabled"`
}

type tenantConfigRow struct {
	Revision            int64
	VersionID           string
	Digest              string
	AdoptedAt           string
	AdoptedBy           string
	ModelSendsEnabled   bool
	ModelSendsRevision  int64
	ModelSendsUpdatedAt string
}

type tenantPolicyCandidate struct {
	TenantConfigCatalogEntry
}

func publicTemplateCandidate() TenantConfigCatalogEntry {
	raw := []byte(`{"id":"public-contract-review","version":"1","template":"contract-review-template/v1"}`)
	digest := sha256.Sum256(raw)
	return TenantConfigCatalogEntry{TenantConfigRef: TenantConfigRef{ID: "public-contract-review", Version: "1", Digest: hex.EncodeToString(digest[:])}, Title: "合同审查公共模板", Kind: "public_template"}
}

func canonicalTenantConfig(selection TenantConfigSelection) ([]byte, error) {
	selection.PrivatePolicyRefs = append([]TenantConfigRef(nil), selection.PrivatePolicyRefs...)
	sort.Slice(selection.PrivatePolicyRefs, func(i, j int) bool {
		a, b := selection.PrivatePolicyRefs[i], selection.PrivatePolicyRefs[j]
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Version < b.Version
	})
	return json.Marshal(selection)
}

func (s *Store) tenantConfigScope(ctx context.Context, tx pgx.Tx, user accounts.User) error {
	return setLocalScope(ctx, tx, dbScope{ActorID: user.ID, TenantID: user.TenantID, OrgID: user.OrgID, Role: "api"})
}

func (s *Store) readTenantConfigRow(ctx context.Context, q pgx.Tx, tenantID string) (tenantConfigRow, error) {
	var row tenantConfigRow
	err := q.QueryRow(ctx, `SELECT config_revision,COALESCE(version_id,''),digest,COALESCE(adopted_at::text,''),adopted_by,model_sends_enabled,model_sends_revision,model_sends_updated_at::text
		FROM tenant_config_control($1)`, tenantID).Scan(&row.Revision, &row.VersionID, &row.Digest, &row.AdoptedAt, &row.AdoptedBy, &row.ModelSendsEnabled, &row.ModelSendsRevision, &row.ModelSendsUpdatedAt)
	return row, err
}

func (s *Store) tenantConfigCatalogs(ctx context.Context, q queryer, user accounts.User) ([]TenantConfigCatalogEntry, []TenantConfigCatalogEntry, []TenantModelCatalogEntry, []map[string]string, error) {
	public := []TenantConfigCatalogEntry{publicTemplateCandidate()}
	private := []TenantConfigCatalogEntry{}
	rows, err := q.Query(ctx, `SELECT DISTINCT ON (v.knowledge_id) v.knowledge_id,v.version_id,v.content_sha256,v.display_version
		FROM governed_knowledge_versions v JOIN governed_knowledge_state state USING(tenant_id,knowledge_id,version_id)
		JOIN governed_knowledge_pointers pointer USING(tenant_id,knowledge_id,scope_key)
		WHERE v.tenant_id=$1 AND v.kind='policy' AND v.scope_org_id IS NULL AND v.scope_case_id IS NULL
		AND state.status='published' AND pointer.active ORDER BY v.knowledge_id,v.created_at DESC`, user.TenantID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var item TenantConfigCatalogEntry
		if err := rows.Scan(&item.ID, &item.Version, &item.Digest, &item.Title); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		item.Kind = "private_policy"
		private = append(private, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, nil, nil, nil, err
	}
	rows.Close()
	models := []TenantModelCatalogEntry{}
	credentials := []map[string]string{}
	settings, found, err := s.LLMEndpoint(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if found && settings.Active() {
		identity, _, err := model.StructuredConfigurationIDFor(settings.BaseURL, settings.ReviewModel, settings.OCRModel)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		models = append(models, TenantModelCatalogEntry{ID: identity, Revision: strconv.FormatInt(settings.Revision, 10), ProviderLabel: settings.Name, ChatModel: settings.ReviewModel, OCRModel: settings.OCRModel})
		if settings.HasAPIKey() {
			credentials = append(credentials, map[string]string{"id": tenantCredentialRef, "label": "当前平台模型凭据"})
		}
	}
	return public, private, models, credentials, nil
}

func catalogEntry(catalog []TenantConfigCatalogEntry, ref TenantConfigRef) (TenantConfigCatalogEntry, bool) {
	for _, item := range catalog {
		if item.ID == ref.ID && item.Version == ref.Version && item.Digest == ref.Digest {
			return item, true
		}
	}
	return TenantConfigCatalogEntry{}, false
}

func (s *Store) validateTenantConfigSelection(ctx context.Context, q queryer, user accounts.User, selection TenantConfigSelection) (TenantConfigPreview, []TenantConfigCatalogEntry, []TenantConfigCatalogEntry, []TenantModelCatalogEntry, []map[string]string, error) {
	selection.PrivatePolicyRefs = append([]TenantConfigRef(nil), selection.PrivatePolicyRefs...)
	canonical, err := canonicalTenantConfig(selection)
	if err != nil {
		return TenantConfigPreview{}, nil, nil, nil, nil, err
	}
	_ = canonical
	public, private, models, credentials, err := s.tenantConfigCatalogs(ctx, q, user)
	if err != nil {
		return TenantConfigPreview{}, nil, nil, nil, nil, err
	}
	problems := []TenantConfigValidationError{}
	if _, ok := catalogEntry(public, selection.PublicTemplateRef); !ok {
		problems = append(problems, TenantConfigValidationError{Path: "public_template_ref", Code: "config_not_allowed"})
	}
	seen := map[string]bool{}
	for _, ref := range selection.PrivatePolicyRefs {
		key := ref.ID + "\x00" + ref.Version
		if seen[key] {
			problems = append(problems, TenantConfigValidationError{Path: "private_policy_refs", Code: "duplicate_ref"})
			continue
		}
		seen[key] = true
		if _, ok := catalogEntry(private, ref); !ok {
			problems = append(problems, TenantConfigValidationError{Path: "private_policy_refs", Code: "config_not_allowed"})
		}
	}
	if selection.EgressMode != "approved_only" {
		problems = append(problems, TenantConfigValidationError{Path: "egress_mode", Code: "config_not_allowed"})
	}
	modelAllowed := false
	for _, item := range models {
		if item.ID == selection.ModelCatalogRef {
			modelAllowed = true
		}
	}
	if !modelAllowed {
		problems = append(problems, TenantConfigValidationError{Path: "model_catalog_ref", Code: "config_not_allowed"})
	}
	credentialAllowed := selection.CredentialRef == nil
	if selection.CredentialRef != nil {
		for _, item := range credentials {
			if item["id"] == *selection.CredentialRef {
				credentialAllowed = true
			}
		}
	}
	if !credentialAllowed {
		problems = append(problems, TenantConfigValidationError{Path: "credential_ref", Code: "config_not_allowed"})
	}
	return TenantConfigPreview{Valid: len(problems) == 0, Diff: []TenantConfigDiff{}, Errors: problems}, public, private, models, credentials, nil
}

func (s *Store) ReadTenantConfig(ctx context.Context, user accounts.User) (TenantConfigView, error) {
	if !s.HasDatabase() {
		return TenantConfigView{}, errors.New("tenant config requires PostgreSQL")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantConfigView{}, err
	}
	defer tx.Rollback(ctx)
	if err := s.tenantConfigScope(ctx, tx, user); err != nil {
		return TenantConfigView{}, err
	}
	row, err := s.readTenantConfigRow(ctx, tx, user.TenantID)
	if err != nil {
		return TenantConfigView{}, err
	}
	public, private, models, credentials, err := s.tenantConfigCatalogs(ctx, tx, user)
	if err != nil {
		return TenantConfigView{}, err
	}
	view := TenantConfigView{SchemaVersion: "tenant-config/v1", TenantID: user.TenantID, Revision: strconv.FormatInt(row.Revision, 10), PublicTemplateCatalog: public, PrivatePolicyCatalog: private, ModelCatalog: models, CredentialRefs: credentials,
		LiveControl: TenantConfigLiveControl{Enabled: row.ModelSendsEnabled, Revision: strconv.FormatInt(row.ModelSendsRevision, 10), UpdatedAt: row.ModelSendsUpdatedAt}}
	if row.VersionID != "" {
		var raw []byte
		var digest, createdAt, createdBy string
		if err := tx.QueryRow(ctx, `SELECT config_json,digest,created_at::text,created_by FROM tenant_config_versions WHERE tenant_id=$1 AND version_id=$2`, user.TenantID, row.VersionID).Scan(&raw, &digest, &createdAt, &createdBy); err != nil {
			return TenantConfigView{}, err
		}
		var selection TenantConfigSelection
		if err := json.Unmarshal(raw, &selection); err != nil {
			return TenantConfigView{}, errors.New("stored tenant config version is invalid")
		}
		publicEntry, ok := catalogEntry(public, selection.PublicTemplateRef)
		if !ok {
			publicEntry = TenantConfigCatalogEntry{TenantConfigRef: selection.PublicTemplateRef, Title: selection.PublicTemplateRef.ID, Kind: "public_template"}
		}
		privateEntries := []TenantConfigCatalogEntry{}
		for _, ref := range selection.PrivatePolicyRefs {
			item, ok := catalogEntry(private, ref)
			if !ok {
				item = TenantConfigCatalogEntry{TenantConfigRef: ref, Title: ref.ID, Kind: "private_policy"}
			}
			privateEntries = append(privateEntries, item)
		}
		view.Current = &AdoptedTenantConfig{ConfigVersion: strconv.FormatInt(row.Revision, 10), VersionID: row.VersionID, Digest: digest, PublicTemplate: publicEntry, PrivatePolicyRefs: privateEntries, ModelCatalogRef: selection.ModelCatalogRef, CredentialRef: selection.CredentialRef, EgressMode: selection.EgressMode, AdoptedAt: createdAt, AdoptedBy: createdBy}
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantConfigView{}, err
	}
	return view, nil
}

func (s *Store) PreviewTenantConfig(ctx context.Context, user accounts.User, expected string, selection TenantConfigSelection) (TenantConfigPreview, error) {
	if user.Role != "admin" {
		return TenantConfigPreview{}, fault.Forbidden("只有租户管理员可以预览配置")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantConfigPreview{}, err
	}
	defer tx.Rollback(ctx)
	if err := s.tenantConfigScope(ctx, tx, user); err != nil {
		return TenantConfigPreview{}, err
	}
	row, err := s.readTenantConfigRow(ctx, tx, user.TenantID)
	if err != nil {
		return TenantConfigPreview{}, err
	}
	expectedRevision, err := strconv.ParseInt(expected, 10, 64)
	if err != nil || expectedRevision < 0 {
		return TenantConfigPreview{}, fault.Invalid("配置 revision 无效")
	}
	if expectedRevision != row.Revision {
		return TenantConfigPreview{}, fault.Conflict("config_revision_conflict")
	}
	preview, public, private, models, _, err := s.validateTenantConfigSelection(ctx, tx, user, selection)
	if err != nil {
		return TenantConfigPreview{}, err
	}
	if preview.Valid {
		before := []string{}
		if row.VersionID != "" {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT config_json FROM tenant_config_versions WHERE tenant_id=$1 AND version_id=$2`, user.TenantID, row.VersionID).Scan(&raw); err != nil {
				return TenantConfigPreview{}, err
			}
			var previous TenantConfigSelection
			if json.Unmarshal(raw, &previous) == nil {
				before = selectionSummary(previous)
			}
		}
		after := selectionSummary(selection)
		labels := []string{"public_template_ref", "private_policy_refs", "model_catalog_ref", "credential_ref", "egress_mode"}
		for i, path := range labels {
			var a, b *string
			if i < len(before) {
				v := before[i]
				a = &v
			}
			if i < len(after) {
				v := after[i]
				b = &v
			}
			if a == nil || b == nil || *a != *b {
				change := "changed"
				if a == nil {
					change = "added"
				} else if b == nil {
					change = "removed"
				}
				preview.Diff = append(preview.Diff, TenantConfigDiff{Path: path, Change: change, Before: a, After: b})
			}
		}
	}
	_ = public
	_ = private
	_ = models
	return preview, nil
}

func selectionSummary(s TenantConfigSelection) []string {
	policy := make([]string, 0, len(s.PrivatePolicyRefs))
	for _, r := range s.PrivatePolicyRefs {
		policy = append(policy, r.ID+"@"+r.Version+"#"+r.Digest)
	}
	sort.Strings(policy)
	credential := ""
	if s.CredentialRef != nil {
		credential = *s.CredentialRef
	}
	return []string{s.PublicTemplateRef.ID + "@" + s.PublicTemplateRef.Version + "#" + s.PublicTemplateRef.Digest, strings.Join(policy, ","), s.ModelCatalogRef, credential, s.EgressMode}
}

func (s *Store) AdoptTenantConfig(ctx context.Context, user accounts.User, expected string, selection TenantConfigSelection) (TenantConfigAdoptionResult, error) {
	if user.Role != "admin" {
		return TenantConfigAdoptionResult{}, fault.Forbidden("只有租户管理员可以采用配置")
	}
	if s.ReadOnly() {
		return TenantConfigAdoptionResult{}, fault.New(503, "read_only", "维护模式不允许采用配置")
	}
	expectedRevision, err := strconv.ParseInt(expected, 10, 64)
	if err != nil || expectedRevision < 0 {
		return TenantConfigAdoptionResult{}, fault.Invalid("配置 revision 无效")
	}
	configJSON, err := canonicalTenantConfig(selection)
	if err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	sum := sha256.Sum256(configJSON)
	digest := hex.EncodeToString(sum[:])
	versionID, err := newID("tcv_")
	if err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	ctx = commandContext(ctx, user)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := s.tenantConfigScope(ctx, tx, user); err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	row, err := s.readTenantConfigRow(ctx, tx, user.TenantID)
	if err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	if row.Revision != expectedRevision {
		return TenantConfigAdoptionResult{}, fault.New(409, "config_revision_conflict", "租户配置已更新，请刷新后重试")
	}
	preview, public, private, models, _, err := s.validateTenantConfigSelection(ctx, tx, user, selection)
	if err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	if !preview.Valid {
		return TenantConfigAdoptionResult{}, fault.New(422, "config_not_allowed", "所选配置不在当前租户允许目录中")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO tenant_config_versions(tenant_id,version_id,digest,config_json,created_by) VALUES($1,$2,$3,$4::jsonb,$5)`, user.TenantID, versionID, digest, string(configJSON), user.ID); err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT tenant_config_adopt($1,$2,$3,$4,$5)`, user.TenantID, expectedRevision, versionID, digest, user.ID).Scan(&revision); err != nil {
		return TenantConfigAdoptionResult{}, tenantConfigFunctionError(err, "config_revision_conflict")
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantConfigAdoptionResult{}, err
	}
	publicEntry, _ := catalogEntry(public, selection.PublicTemplateRef)
	privateEntries := []TenantConfigCatalogEntry{}
	for _, ref := range selection.PrivatePolicyRefs {
		item, _ := catalogEntry(private, ref)
		privateEntries = append(privateEntries, item)
	}
	config := AdoptedTenantConfig{ConfigVersion: strconv.FormatInt(revision, 10), VersionID: versionID, Digest: digest, PublicTemplate: publicEntry, PrivatePolicyRefs: privateEntries, ModelCatalogRef: selection.ModelCatalogRef, CredentialRef: selection.CredentialRef, EgressMode: selection.EgressMode, AdoptedAt: stamp(), AdoptedBy: user.ID}
	_ = models
	return TenantConfigAdoptionResult{Revision: strconv.FormatInt(revision, 10), AdoptedConfig: config}, nil
}

func (s *Store) SetTenantModelSendControl(ctx context.Context, user accounts.User, expected string, enabled bool) (TenantLiveControlResult, error) {
	if user.Role != "admin" {
		return TenantLiveControlResult{}, fault.Forbidden("只有租户管理员可以修改实时模型发送控制")
	}
	if s.ReadOnly() {
		return TenantLiveControlResult{}, fault.New(503, "read_only", "维护模式不允许修改实时模型发送控制")
	}
	expectedRevision, err := strconv.ParseInt(expected, 10, 64)
	if err != nil || expectedRevision < 1 {
		return TenantLiveControlResult{}, fault.Invalid("实时控制 revision 无效")
	}
	ctx = commandContext(ctx, user)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantLiveControlResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := s.tenantConfigScope(ctx, tx, user); err != nil {
		return TenantLiveControlResult{}, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT tenant_model_send_set($1,$2,$3,$4)`, user.TenantID, expectedRevision, enabled, user.ID).Scan(&revision); err != nil {
		return TenantLiveControlResult{}, tenantConfigFunctionError(err, "live_control_revision_conflict")
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantLiveControlResult{}, err
	}
	return TenantLiveControlResult{Revision: strconv.FormatInt(revision, 10), Enabled: enabled}, nil
}

func tenantConfigFunctionError(err error, code string) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) && (state.SQLState() == "P7C01" || state.SQLState() == "P7C02") {
		return fault.New(409, code, "租户配置已被其他管理员更新，请刷新后重试")
	}
	return err
}

// acceptedTenantConfig resolves and locks the adoption on the same transaction
// that decides whether the run can be reused and writes its new snapshot.
func (s *Store) acceptedTenantConfig(ctx context.Context, tx pgx.Tx, tenantID string) (string, string, error) {
	var versionID, digest string
	err := tx.QueryRow(ctx, `SELECT COALESCE(version_id,''),digest FROM tenant_config_control($1)`, tenantID).Scan(&versionID, &digest)
	if errors.Is(err, pgx.ErrNoRows) && s.sel.Mode != "production" && (tenantID == accounts.LocalDemoTenant || tenantID == accounts.LocalDemoTenant2) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	if (versionID == "") != (digest == "") || (versionID != "" && !validPreparationHash(digest)) {
		return "", "", errors.New("invalid adopted tenant config identity")
	}
	return versionID, digest, nil
}

func (s *Store) freezeTenantConfigSnapshot(ctx context.Context, tx pgx.Tx, previous state, next *state) error {
	oldRuns := make(map[string]bool, len(previous.Snapshots))
	for _, snapshot := range previous.Snapshots {
		oldRuns[snapshot.RunID] = true
	}
	for i := range next.Snapshots {
		snapshot := &next.Snapshots[i]
		if oldRuns[snapshot.RunID] {
			continue
		}
		var caseID string
		for _, run := range next.Runs {
			if run.ID == snapshot.RunID {
				caseID = run.CaseID
				break
			}
		}
		item, ok := next.caseByID(caseID)
		if !ok || item.TenantID == "" {
			return errors.New("tenant config freeze has no tenant identity")
		}
		versionID, digest, err := s.acceptedTenantConfig(ctx, tx, item.TenantID)
		if err != nil {
			return err
		}
		// Older direct snapshot constructors have not chosen a version yet.
		if snapshot.InputHashVersion == 0 {
			snapshot.AdoptedTenantConfigVersionID, snapshot.AdoptedTenantConfigDigest = versionID, digest
			snapshot.InputHashVersion = 1
			if versionID != "" {
				snapshot.InputHashVersion = 2
			}
		}
		if snapshot.AdoptedTenantConfigVersionID != versionID || snapshot.AdoptedTenantConfigDigest != digest || (versionID != "" && snapshot.InputHashVersion != 2) || (versionID == "" && snapshot.InputHashVersion != 1) {
			return errors.New("tenant config changed during review admission")
		}
	}
	return nil
}
