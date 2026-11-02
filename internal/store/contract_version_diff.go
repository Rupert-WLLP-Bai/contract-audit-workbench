package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"

	"github.com/jackc/pgx/v5"
)

// Version diffs are computed from two immutable revision snapshots plus the
// frozen restricted-field fingerprints. Unrestricted values are compared from
// the stored snapshot; restricted values never leave the server and are
// reported only as changed/unchanged (or unknown when a version predates the
// fingerprint seal or the data key was rotated).

// ContractVersionFieldChange is one field-level difference. Before/After carry
// no restricted value: restricted paths live only in RestrictedFields.
type ContractVersionFieldChange struct {
	Path           string `json:"path"`
	Category       string `json:"category"`
	Classification string `json:"classification"`
	Change         string `json:"change"`
	Before         any    `json:"before,omitempty"`
	After          any    `json:"after,omitempty"`
}

// ContractVersionRestrictedChange reports only whether a restricted field
// changed. A null Changed means the comparison could not be proven.
type ContractVersionRestrictedChange struct {
	Path    string `json:"path"`
	Changed *bool  `json:"changed"`
	Reason  string `json:"reason,omitempty"`
}

// ContractVersionDiffSummary counts the returned changes.
type ContractVersionDiffSummary struct {
	ChangedFields     int `json:"changed_fields"`
	AddedFields       int `json:"added_fields"`
	RemovedFields     int `json:"removed_fields"`
	RestrictedChanged int `json:"restricted_changed"`
	RestrictedUnknown int `json:"restricted_unknown"`
}

// ContractVersionDiff is the frozen field-level comparison. AmountTerms is the
// subset that locates the amount, item/subject-matter and payment clauses.
type ContractVersionDiff struct {
	ContractID         string                            `json:"contract_id"`
	FromRevisionID     *string                           `json:"from_revision_id"`
	ToRevisionID       string                            `json:"to_revision_id"`
	FromRevisionNumber *int                              `json:"from_revision_number"`
	ToRevisionNumber   int                               `json:"to_revision_number"`
	Changed            bool                              `json:"changed"`
	Fields             []ContractVersionFieldChange      `json:"fields"`
	RestrictedFields   []ContractVersionRestrictedChange `json:"restricted_fields"`
	AmountTerms        []ContractVersionFieldChange      `json:"amount_terms"`
	Summary            ContractVersionDiffSummary        `json:"summary"`
}

// DiffContractVersions compares two revisions of the same visible contract.
// When toRevisionID is empty the latest revision is used; when fromRevisionID
// is empty the immediately previous revision is used, or an empty baseline for
// revision 1. Both absent therefore answer "what changed in the latest frozen
// version".
func (s *Store) DiffContractVersions(ctx context.Context, user accounts.User, contractID, fromRevisionID, toRevisionID string) (ContractVersionDiff, error) {
	ctx = commandContext(ctx, user)
	id, err := normalizeContractUUID(contractID)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	if toRevisionID != "" {
		if toRevisionID, err = normalizeContractUUID(toRevisionID); err != nil {
			return ContractVersionDiff{}, err
		}
	}
	if fromRevisionID != "" {
		if fromRevisionID, err = normalizeContractUUID(fromRevisionID); err != nil {
			return ContractVersionDiff{}, err
		}
	}
	if !s.HasDatabase() {
		return ContractVersionDiff{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	current, err := trustedUser(user)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ContractVersionDiff{}, err
	}
	defer rollbackCLM(tx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return ContractVersionDiff{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return ContractVersionDiff{}, fault.NotFound("没有找到对应记录")
	}
	if err := setLocalScope(ctx, tx, dbScope{ActorID: current.ID, TenantID: accounts.Tenant(current), OrgID: record.OwnerOrgID, Role: "api"}); err != nil {
		return ContractVersionDiff{}, err
	}
	if toRevisionID == "" {
		if err := tx.QueryRow(ctx, `SELECT contract_revision_id FROM clm_contract_revisions
			WHERE tenant_id=$1 AND contract_id=$2 ORDER BY revision_number DESC LIMIT 1`,
			accounts.Tenant(current), id).Scan(&toRevisionID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ContractVersionDiff{}, fault.NotFound("没有找到可比较的冻结版本")
			}
			return ContractVersionDiff{}, err
		}
	}
	target, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), id, toRevisionID)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	var baseline *frozenContractVersion
	if fromRevisionID != "" {
		loaded, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), id, fromRevisionID)
		if err != nil {
			return ContractVersionDiff{}, err
		}
		baseline = &loaded
	} else if target.RevisionNumber > 1 {
		var previousID string
		err := tx.QueryRow(ctx, `SELECT contract_revision_id FROM clm_contract_revisions
			WHERE tenant_id=$1 AND contract_id=$2 AND revision_number=$3`,
			accounts.Tenant(current), id, target.RevisionNumber-1).Scan(&previousID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ContractVersionDiff{}, err
		}
		if err == nil {
			loaded, err := loadFrozenContractVersion(ctx, tx, accounts.Tenant(current), id, previousID)
			if err != nil {
				return ContractVersionDiff{}, err
			}
			baseline = &loaded
		}
	}
	var baselineDraft, targetDraft DraftContent
	if baseline != nil {
		if err := unmarshalStoredDraft(baseline.Snapshot, &baselineDraft); err != nil {
			return ContractVersionDiff{}, err
		}
	}
	if err := unmarshalStoredDraft(target.Snapshot, &targetDraft); err != nil {
		return ContractVersionDiff{}, err
	}
	baselineSeal, baselineMaterials, err := loadVersionComparisonState(ctx, tx, accounts.Tenant(current), id, baseline)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	targetSeal, targetMaterials, err := loadVersionComparisonState(ctx, tx, accounts.Tenant(current), id, &target)
	if err != nil {
		return ContractVersionDiff{}, err
	}
	before := flattenContractDraft(baselineDraft)
	after := flattenContractDraft(targetDraft)
	mergeMaterialFields(before, baselineMaterials)
	mergeMaterialFields(after, targetMaterials)
	fields := diffFrozenFields(before, after)
	restricted := diffRestrictedFields(baseline != nil, baselineSeal, targetSeal)
	amountTerms := make([]ContractVersionFieldChange, 0)
	for _, field := range fields {
		if fieldCategoryIsAmountTerm(field.Category) {
			amountTerms = append(amountTerms, field)
		}
	}
	summary := ContractVersionDiffSummary{}
	for _, field := range fields {
		switch field.Change {
		case "added":
			summary.AddedFields++
		case "removed":
			summary.RemovedFields++
		default:
			summary.ChangedFields++
		}
	}
	for _, field := range restricted {
		if field.Changed == nil {
			summary.RestrictedUnknown++
		} else if *field.Changed {
			summary.RestrictedChanged++
		}
	}
	diff := ContractVersionDiff{
		ContractID: id, FromRevisionID: nil, ToRevisionID: target.RevisionID,
		FromRevisionNumber: nil, ToRevisionNumber: target.RevisionNumber,
		Changed: len(fields) > 0 || summary.RestrictedChanged > 0,
		Fields:  fields, RestrictedFields: restricted, AmountTerms: amountTerms,
		Summary: summary,
	}
	if baseline != nil {
		fromID := baseline.RevisionID
		fromNumber := baseline.RevisionNumber
		diff.FromRevisionID = &fromID
		diff.FromRevisionNumber = &fromNumber
	}
	if err := tx.Commit(ctx); err != nil {
		return ContractVersionDiff{}, err
	}
	return diff, nil
}

// loadVersionComparisonState loads and cross-checks a revision's seal and
// material rows. A missing seal means the revision predates #81 (legacy
// import), so restricted comparison is unknown rather than invented.
func loadVersionComparisonState(ctx context.Context, tx pgx.Tx, tenantID, contractID string, version *frozenContractVersion) (*contractVersionSeal, []frozenVersionMaterial, error) {
	if version == nil {
		return nil, []frozenVersionMaterial{}, nil
	}
	seal, err := loadContractVersionSeal(ctx, tx, tenantID, contractID, version.RevisionID)
	if err != nil {
		return nil, nil, err
	}
	materials, err := loadContractVersionMaterials(ctx, tx, tenantID, contractID, version.RevisionID)
	if err != nil {
		return nil, nil, err
	}
	if seal != nil {
		if seal.SnapshotDigest != version.SnapshotDigest || sha256Bytes(version.Snapshot) != version.SnapshotDigest {
			return nil, nil, fault.Conflict("冻结版本的快照摘要与封印不一致")
		}
		materialsDigest, err := canonicalHash(materials)
		if err != nil {
			return nil, nil, err
		}
		if materialsDigest != seal.MaterialsDigest {
			return nil, nil, fault.Conflict("冻结版本的材料摘要与封印不一致")
		}
	}
	return seal, materials, nil
}

func mergeMaterialFields(fields map[string]any, materials []frozenVersionMaterial) {
	for _, material := range materials {
		prefix := "materials[material_id=" + material.MaterialID + "]"
		fields[prefix+".material_version_id"] = material.MaterialVersionID
		fields[prefix+".material_version"] = material.MaterialVersion
		fields[prefix+".filename"] = material.Filename
		fields[prefix+".digest"] = material.Digest
		fields[prefix+".purpose"] = material.Purpose
		fields[prefix+".case_id"] = material.CaseID
		fields[prefix+".case_org_id"] = material.CaseOrgID
	}
}

func diffFrozenFields(before, after map[string]any) []ContractVersionFieldChange {
	keys := make([]string, 0, len(before)+len(after))
	seen := make(map[string]bool, len(before)+len(after))
	for path := range before {
		if !seen[path] {
			seen[path] = true
			keys = append(keys, path)
		}
	}
	for path := range after {
		if !seen[path] {
			seen[path] = true
			keys = append(keys, path)
		}
	}
	sort.Strings(keys)
	changes := make([]ContractVersionFieldChange, 0)
	for _, path := range keys {
		beforeValue, inBefore := before[path]
		afterValue, inAfter := after[path]
		if inBefore && inAfter && reflect.DeepEqual(beforeValue, afterValue) {
			continue
		}
		change := ContractVersionFieldChange{
			Path: path, Category: fieldCategory(path), Classification: fieldClassification(path),
		}
		switch {
		case !inBefore:
			change.Change = "added"
			change.After = afterValue
		case !inAfter:
			change.Change = "removed"
			change.Before = beforeValue
		default:
			change.Change = "changed"
			change.Before = beforeValue
			change.After = afterValue
		}
		changes = append(changes, change)
	}
	return changes
}

func diffRestrictedFields(hasBaseline bool, before, after *contractVersionSeal) []ContractVersionRestrictedChange {
	changes := make([]ContractVersionRestrictedChange, 0, len(clmRestrictedFieldPaths))
	for _, path := range clmRestrictedFieldPaths {
		change := ContractVersionRestrictedChange{Path: path}
		if !hasBaseline {
			// Revision 1 compares against an empty baseline. A legacy revision
			// without a seal cannot prove presence, so report unknown rather
			// than claiming "unchanged".
			if after == nil {
				change.Reason = "fingerprint_unavailable"
				changes = append(changes, change)
				continue
			}
			state, ok := after.RestrictedFingerprints.Fields[path]
			changed := ok && state.Present
			change.Changed = &changed
			changes = append(changes, change)
			continue
		}
		if before == nil || after == nil {
			change.Reason = "fingerprint_unavailable"
			changes = append(changes, change)
			continue
		}
		beforeSet, afterSet := before.RestrictedFingerprints, after.RestrictedFingerprints
		if beforeSet.Algorithm != clmRestrictedFingerprintAlgorithm || afterSet.Algorithm != clmRestrictedFingerprintAlgorithm {
			change.Reason = "fingerprint_algorithm_unsupported"
			changes = append(changes, change)
			continue
		}
		beforeField, beforeOK := beforeSet.Fields[path]
		afterField, afterOK := afterSet.Fields[path]
		if !beforeOK || !afterOK {
			change.Reason = "fingerprint_unavailable"
			changes = append(changes, change)
			continue
		}
		if beforeField.Present && afterField.Present {
			if beforeSet.KeyID == "" || afterSet.KeyID == "" || beforeSet.KeyID != afterSet.KeyID {
				change.Reason = "fingerprint_key_changed"
				changes = append(changes, change)
				continue
			}
			changed := beforeField.Digest != afterField.Digest
			change.Changed = &changed
			changes = append(changes, change)
			continue
		}
		changed := beforeField.Present != afterField.Present
		change.Changed = &changed
		changes = append(changes, change)
	}
	return changes
}

func fieldCategoryIsAmountTerm(category string) bool {
	switch category {
	case "amount", "payment", "subject_matter":
		return true
	default:
		return false
	}
}

// fieldClassification mirrors the data dictionary: only the four supplier
// payment fields are restricted; confidential values stay visible to an
// authorized reader, as they already are on the editable detail.
func fieldClassification(path string) string {
	if clmRestrictedFieldPath(path) {
		return "restricted"
	}
	if confidentialContractField(path) {
		return "confidential"
	}
	return "internal"
}

var confidentialContractFields = map[string]bool{
	"contract.amount":          true,
	"contract.handler_user_id": true,
	"contract.handler_contact": true,
	"buyer.contact_name":       true,
	"buyer.contact_phone":      true,
	"supplier.credit_code":     true,
	"supplier.address":         true,
	"supplier.contact_name":    true,
	"supplier.contact_phone":   true,
}

func confidentialContractField(path string) bool {
	if confidentialContractFields[path] {
		return true
	}
	for _, suffix := range []string{
		".amount", ".unit_price", ".line_amount", ".budget_amount",
		".advance_amount", ".unit_amount", ".fiscal_amount",
	} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// fieldCategory maps a canonical field path onto the sections a reviewer uses
// to locate the amount and subject-matter clauses.
func fieldCategory(path string) string {
	switch path {
	case "contract.amount", "contract.currency", "contract.settlement_mode", "contract.prices_include_tax":
		return "amount"
	}
	for _, suffix := range []string{
		".amount", ".unit_price", ".line_amount", ".budget_amount", ".quantity",
		".tax_rate", ".ratio", ".advance_amount", ".unit_amount", ".fiscal_amount",
	} {
		if strings.HasSuffix(path, suffix) {
			return "amount"
		}
	}
	switch {
	case strings.HasPrefix(path, "payments["), strings.HasPrefix(path, "funding["):
		return "payment"
	case strings.HasPrefix(path, "items["), strings.HasPrefix(path, "assets["),
		strings.HasPrefix(path, "delivery_plans["), strings.HasPrefix(path, "acceptance_plans["),
		strings.HasPrefix(path, "contract.procurement_result_package"),
		path == "contract.name", path == "contract.project_type",
		path == "contract.classification", path == "contract.category":
		return "subject_matter"
	case strings.HasPrefix(path, "materials["):
		return "material"
	case strings.HasPrefix(path, "contract.related_parties"), strings.HasPrefix(path, "buyer."),
		strings.HasPrefix(path, "supplier."):
		return "party"
	case strings.HasPrefix(path, "contract."):
		return "term"
	default:
		return "other"
	}
}

// flattenContractDraft projects a typed draft onto canonical field paths.
// Arrays with a stable identity use that identity (item_id, sequence, ...) so
// reordering rows does not fabricate differences. Restricted fields are
// omitted entirely; they are compared through fingerprints only.
func flattenContractDraft(draft DraftContent) map[string]any {
	fields := make(map[string]any)
	flattenStructFields("", reflect.ValueOf(draft), fields)
	return fields
}

func clmServerPinFieldPath(path string) bool {
	return strings.HasSuffix(path, ".reference_snapshot") || strings.HasSuffix(path, ".source_snapshot")
}

func flattenStructFields(prefix string, value reflect.Value, out map[string]any) {
	typ := value.Type()
	for index := 0; index < value.NumField(); index++ {
		field := typ.Field(index)
		if !field.IsExported() {
			continue
		}
		tag := field.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		flattenFieldValue(path, value.Field(index), out)
	}
}

func flattenFieldValue(path string, value reflect.Value, out map[string]any) {
	if clmRestrictedFieldPath(path) {
		return
	}
	// reference_snapshot/source_snapshot are server-resolved pins written by
	// the freeze transaction. Their captured_at changes on every resolution,
	// so they must not become business differences or fingerprint inputs; the
	// sealed snapshot digest already covers the exact pinned bytes.
	if clmServerPinFieldPath(path) {
		return
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		flattenStructFields(path, value, out)
	case reflect.Slice:
		flattenSliceField(path, value, out)
	default:
		if value.Kind() == reflect.String && value.String() == "" {
			return
		}
		out[path] = value.Interface()
	}
}

func flattenSliceField(path string, value reflect.Value, out map[string]any) {
	if value.Len() == 0 {
		return
	}
	element := value.Type().Elem()
	for element.Kind() == reflect.Pointer {
		element = element.Elem()
	}
	switch element.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		out[path] = value.Interface()
		return
	}
	keyField := sliceKeyField(element)
	seen := make(map[string]int, value.Len())
	for index := 0; index < value.Len(); index++ {
		elementPath := fmt.Sprintf("%s[ordinal=%d]", path, index)
		if keyField != "" {
			if key, ok := structKeyValue(value.Index(index), keyField); ok {
				elementPath = fmt.Sprintf("%s[%s=%s]", path, keyField, key)
				seen[key]++
				if seen[key] > 1 {
					elementPath += "#" + strconv.Itoa(seen[key])
				}
			}
		}
		flattenFieldValue(elementPath, value.Index(index), out)
	}
}

func sliceKeyField(element reflect.Type) string {
	switch element {
	case reflect.TypeOf(DraftItem{}):
		return "item_id"
	case reflect.TypeOf(DraftAssetPlan{}):
		return "asset_plan_id"
	case reflect.TypeOf(PartyRef{}):
		return "party_id"
	case reflect.TypeOf(MaterialRef{}):
		return "material_id"
	case reflect.TypeOf(DraftPaymentPlan{}), reflect.TypeOf(DraftDeliveryPlan{}), reflect.TypeOf(DraftAcceptancePlan{}):
		return "sequence"
	default:
		return ""
	}
}

func structKeyValue(value reflect.Value, fieldName string) (string, bool) {
	goField := map[string]string{
		"item_id":       "ItemID",
		"asset_plan_id": "AssetPlanID",
		"party_id":      "PartyID",
		"material_id":   "MaterialID",
		"sequence":      "Sequence",
	}[fieldName]
	if goField == "" {
		return "", false
	}
	for value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return "", false
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return "", false
	}
	field := value.FieldByName(goField)
	if !field.IsValid() {
		return "", false
	}
	switch field.Kind() {
	case reflect.String:
		if field.String() == "" {
			return "", false
		}
		return field.String(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(field.Int(), 10), true
	default:
		return "", false
	}
}
