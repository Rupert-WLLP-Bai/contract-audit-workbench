package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type ReadIdentity struct {
	TenantID string
	OrgID    string
	CaseID   string
}

type VersionRow struct {
	ServiceObjectID  string
	Version          int
	ObjectType       model.ServiceObjectType
	DisplayName      string
	BusinessCode     string
	Aliases          []byte
	State            model.ServiceObjectState
	EffectiveFrom    time.Time
	ChangeDecisionID string
	BasisRefs        []byte
	BasisSummary     string
	PayloadSHA256    string
	CreatedBy        string
	CreatedAt        time.Time
}

type AssociationRow struct {
	ServiceObjectID          string
	AssociationVersion       int
	ObjectVersion            int
	MaterialID               string
	MaterialVersion          int
	DocumentID               string
	ParsedDocumentID         string
	SourceRunID              string
	SourceAttemptID          string
	ObjectRef                string
	FactsCanonicalJSON       []byte
	FactsSHA256              string
	BasisRefs                []byte
	IdentityChangeDecisionID string
	PayloadSHA256            string
	ConfirmedBy              string
	ConfirmedAt              time.Time
	CreatedAt                time.Time
}

type VersionRows interface {
	Next() bool
	Read() (VersionRow, error)
	Err() error
	Close()
}

type AssociationRows interface {
	Next() bool
	Read() (AssociationRow, error)
	Err() error
	Close()
}

type MappingLineageRow struct {
	CaseID                string
	MappingID             string
	ServiceObjectID       string
	CurrentVersion        int64
	CurrentBindingVersion string
	CreatedBy             string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type MappingBindingRow struct {
	RequestSHA256 string
	Payload       model.ServiceObjectMappingBindingPayload
	ResponseBody  []byte
}

type ReadRepository interface {
	ReadHead(context.Context, ReadIdentity, string) (model.ServiceObjectHead, bool, error)
	ListHeads(context.Context, ReadIdentity, string, int) ([]model.ServiceObjectHead, error)
	ReadVersionRaw(context.Context, ReadIdentity, string, int) (VersionRow, bool, error)
	ListVersionNumbers(context.Context, ReadIdentity, string, int, int) ([]int, error)
	StreamVersionRows(context.Context, ReadIdentity, string) (VersionRows, error)
	ReadAssociationRaw(context.Context, ReadIdentity, string, int) (AssociationRow, bool, error)
	ListAssociationNumbers(context.Context, ReadIdentity, string, int, int) ([]int, error)
	StreamAssociationRows(context.Context, ReadIdentity, string) (AssociationRows, error)
	FindMappingID(context.Context, ReadIdentity, string) (string, bool, error)
	ReadMappingLineage(context.Context, ReadIdentity, string) (MappingLineageRow, bool, error)
	ReadMappingBinding(context.Context, ReadIdentity, string) (MappingBindingRow, bool, error)
}

type objectCursor struct {
	Kind     string `json:"kind"`
	CaseID   string `json:"case_id"`
	ObjectID string `json:"object_id"`
	Last     string `json:"last"`
}

func ServiceObjectPageLimit(limit int) (int, error) {
	if limit == 0 {
		return 20, nil
	}
	if limit < 0 || limit > 100 {
		return 0, fault.Invalid("服务对象分页范围为1到100")
	}
	return limit, nil
}

func EncodeServiceObjectCursor(kind, caseID, objectID, value string) string {
	payload, _ := json.Marshal(objectCursor{Kind: kind, CaseID: caseID, ObjectID: objectID, Last: value})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func DecodeServiceObjectCursor(value, kind, caseID, objectID string) (string, error) {
	if value == "" {
		return "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	var cursor objectCursor
	if err != nil || len(decoded) == 0 || len(decoded) > 512 || json.Unmarshal(decoded, &cursor) != nil ||
		cursor.Kind != kind || cursor.CaseID != caseID || cursor.ObjectID != objectID || cursor.Last == "" || strings.TrimSpace(cursor.Last) != cursor.Last {
		return "", fault.Invalid("服务对象游标无效")
	}
	return cursor.Last, nil
}

func EncodeServiceObjectIntegerCursor(kind, caseID, objectID string, value int) string {
	return EncodeServiceObjectCursor(kind, caseID, objectID, strconv.Itoa(value))
}

func DecodeServiceObjectIntegerCursor(value, kind, caseID, objectID string) (int, error) {
	last, err := DecodeServiceObjectCursor(value, kind, caseID, objectID)
	if err != nil || last == "" {
		if err != nil {
			return 0, err
		}
		return 0, nil
	}
	parsed, err := strconv.Atoi(last)
	if err != nil || parsed < 1 {
		return 0, fault.Invalid("服务对象版本游标无效")
	}
	return parsed, nil
}

func ServiceObjectCursorIntOrMax(value int) int {
	if value == 0 {
		// The backing version columns are PostgreSQL int4, not Go int.
		return int(1<<31 - 1)
	}
	return value
}

func ReadServiceObjectSummaries(ctx context.Context, repo ReadRepository, id ReadIdentity, after string, limit int,
	canManage bool, facts model.ServiceObjectMaterialSelectionFacts) (model.ServiceObjectListResult, error) {
	heads, err := repo.ListHeads(ctx, id, after, limit+1)
	if err != nil {
		return model.ServiceObjectListResult{}, err
	}
	pageHeads := heads
	var next *string
	if len(pageHeads) > limit {
		pageHeads = pageHeads[:limit]
		value := EncodeServiceObjectCursor("objects", id.CaseID, "", pageHeads[len(pageHeads)-1].ID)
		next = &value
	}
	result := model.ServiceObjectListResult{Items: make([]model.ServiceObjectSummary, 0, len(pageHeads)), NextCursor: next, CanManage: canManage}
	for _, head := range pageHeads {
		version, err := readVersionAtHead(ctx, repo, id, head)
		if err != nil {
			return model.ServiceObjectListResult{}, err
		}
		var association *model.ServiceObjectAssociationPin
		if head.CurrentAssociationVersion > 0 {
			item, err := readAssociationAtHead(ctx, repo, id, head)
			if err != nil {
				return model.ServiceObjectListResult{}, err
			}
			pin := model.AssociationPin(item)
			association = &pin
		}
		result.Items = append(result.Items, model.ServiceObjectSummary{
			ServiceObjectID: head.ID, ObjectType: version.ObjectType, State: version.State, CurrentVersion: head.CurrentVersion,
			CurrentAssociationVersion: head.CurrentAssociationVersion, DisplayName: version.DisplayName, BusinessCode: version.BusinessCode,
			Aliases: version.Aliases, CurrentAssociation: association, CanManage: canManage,
			NeedsReassociation: model.NeedsReassociation(association, facts),
		})
	}
	return result, nil
}

func ReadServiceObjectHistory(ctx context.Context, repo ReadRepository, id ReadIdentity, head model.ServiceObjectHead) (model.ServiceObjectIdentityHistory, error) {
	result := model.ServiceObjectIdentityHistory{Head: head, Versions: []model.ServiceObjectVersion{}, Associations: []model.ServiceObjectContractAssociation{}}
	versions, err := repo.StreamVersionRows(ctx, id, head.ID)
	if err != nil {
		return model.ServiceObjectIdentityHistory{}, err
	}
	for versions.Next() {
		row, err := versions.Read()
		if err != nil {
			versions.Close()
			return model.ServiceObjectIdentityHistory{}, err
		}
		item, err := decodeVersionRow(row)
		if err != nil {
			versions.Close()
			return model.ServiceObjectIdentityHistory{}, err
		}
		result.Versions = append(result.Versions, item)
	}
	if err := versions.Err(); err != nil {
		versions.Close()
		return model.ServiceObjectIdentityHistory{}, err
	}
	versions.Close()

	associations, err := repo.StreamAssociationRows(ctx, id, head.ID)
	if err != nil {
		return model.ServiceObjectIdentityHistory{}, err
	}
	for associations.Next() {
		row, err := associations.Read()
		if err != nil {
			associations.Close()
			return model.ServiceObjectIdentityHistory{}, err
		}
		item, err := decodeAssociationRow(row)
		if err != nil {
			associations.Close()
			return model.ServiceObjectIdentityHistory{}, err
		}
		result.Associations = append(result.Associations, item)
	}
	if err := associations.Err(); err != nil {
		associations.Close()
		return model.ServiceObjectIdentityHistory{}, err
	}
	associations.Close()
	return result, nil
}

func ProjectServiceObjectDetail(head model.ServiceObjectHead, version model.ServiceObjectVersion,
	association *model.ServiceObjectContractAssociation, mapping *model.ServiceObjectMappingPin,
	facts model.ServiceObjectMaterialSelectionFacts, canManage bool) model.ServiceObjectDetail {
	return model.ServiceObjectDetail{ServiceObjectID: head.ID, CurrentVersion: head.CurrentVersion,
		Identity: version, Association: association, Mapping: mapping, CanManage: canManage,
		NeedsReassociation: model.NeedsReassociation(model.AssociationPinPointer(association), facts)}
}

func ReadServiceObjectVersions(ctx context.Context, repo ReadRepository, id ReadIdentity, objectID string, after, limit int) (model.ServiceObjectVersionPage, error) {
	numbers, err := repo.ListVersionNumbers(ctx, id, objectID, ServiceObjectCursorIntOrMax(after), limit+1)
	if err != nil {
		return model.ServiceObjectVersionPage{}, err
	}
	var next *string
	if len(numbers) > limit {
		numbers = numbers[:limit]
		value := EncodeServiceObjectIntegerCursor("versions", id.CaseID, objectID, numbers[len(numbers)-1])
		next = &value
	}
	result := model.ServiceObjectVersionPage{Items: make([]model.ServiceObjectVersion, 0, len(numbers)), NextCursor: next}
	for _, number := range numbers {
		item, err := ReadServiceObjectVersionExact(ctx, repo, id, objectID, number)
		if err != nil {
			return model.ServiceObjectVersionPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func ReadServiceObjectAssociations(ctx context.Context, repo ReadRepository, id ReadIdentity, objectID string, after, limit int) (model.ServiceObjectAssociationPage, error) {
	numbers, err := repo.ListAssociationNumbers(ctx, id, objectID, ServiceObjectCursorIntOrMax(after), limit+1)
	if err != nil {
		return model.ServiceObjectAssociationPage{}, err
	}
	var next *string
	if len(numbers) > limit {
		numbers = numbers[:limit]
		value := EncodeServiceObjectIntegerCursor("associations", id.CaseID, objectID, numbers[len(numbers)-1])
		next = &value
	}
	result := model.ServiceObjectAssociationPage{Items: make([]model.ServiceObjectContractAssociation, 0, len(numbers)), NextCursor: next}
	for _, number := range numbers {
		item, err := ReadServiceObjectAssociationExact(ctx, repo, id, objectID, number)
		if err != nil {
			return model.ServiceObjectAssociationPage{}, err
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func ReadServiceObjectHead(ctx context.Context, repo ReadRepository, id ReadIdentity, objectID string) (model.ServiceObjectHead, error) {
	head, found, err := repo.ReadHead(ctx, id, objectID)
	if err != nil {
		return model.ServiceObjectHead{}, err
	}
	if !found {
		return model.ServiceObjectHead{}, fault.NotFound("没有找到服务对象")
	}
	return head, nil
}

func ReadServiceObjectVersionExact(ctx context.Context, repo ReadRepository, id ReadIdentity, objectID string, number int) (model.ServiceObjectVersion, error) {
	row, found, err := repo.ReadVersionRaw(ctx, id, objectID, number)
	if err != nil {
		return model.ServiceObjectVersion{}, err
	}
	if !found {
		return model.ServiceObjectVersion{}, fault.NotFound("没有找到服务对象身份版本")
	}
	return decodeVersionRow(row)
}

func ReadServiceObjectAssociationExact(ctx context.Context, repo ReadRepository, id ReadIdentity, objectID string, number int) (model.ServiceObjectContractAssociation, error) {
	row, found, err := repo.ReadAssociationRaw(ctx, id, objectID, number)
	if err != nil {
		return model.ServiceObjectContractAssociation{}, err
	}
	if !found {
		return model.ServiceObjectContractAssociation{}, fault.NotFound("没有找到服务对象关联版本")
	}
	return decodeAssociationRow(row)
}

type resolvedMapping struct {
	head        MappingLineageRow
	binding     model.ServiceObjectMappingBindingPayload
	identity    model.ServiceObjectVersion
	association model.ServiceObjectContractAssociation
}

func readResolvedMapping(ctx context.Context, repo ReadRepository, id ReadIdentity, serviceObjectID string) (resolvedMapping, bool, error) {
	var out resolvedMapping
	mappingID, found, err := repo.FindMappingID(ctx, id, serviceObjectID)
	if err != nil || !found {
		return out, false, err
	}
	out.head, found, err = repo.ReadMappingLineage(ctx, id, mappingID)
	if err != nil {
		return out, false, err
	}
	if !found {
		return out, false, fault.NotFound("没有找到对象映射")
	}
	row, found, err := repo.ReadMappingBinding(ctx, id, out.head.CurrentBindingVersion)
	if err != nil {
		return out, false, err
	}
	if !found {
		return out, false, fault.NotFound("没有找到对象映射版本")
	}
	record, err := DecodeBindingRecord(row, "对象映射响应回执损坏")
	if err != nil {
		return out, false, err
	}
	out.binding = record.Value
	out.identity, err = ReadServiceObjectVersionExact(ctx, repo, id, out.head.ServiceObjectID, out.binding.ObjectVersion)
	if err != nil {
		return out, false, err
	}
	out.association, err = ReadServiceObjectAssociationExact(ctx, repo, id, out.head.ServiceObjectID, out.binding.AssociationVersion)
	if err != nil {
		return out, false, err
	}
	return out, true, nil
}
func ReadServiceObjectMappingPin(ctx context.Context, repo ReadRepository, id ReadIdentity, serviceObjectID string) (*model.ServiceObjectMappingPin, error) {
	value, found, err := readResolvedMapping(ctx, repo, id, serviceObjectID)
	if err != nil || !found {
		return nil, err
	}
	head, binding := value.head, value.binding
	return &model.ServiceObjectMappingPin{MappingID: head.MappingID, MappingVersion: head.CurrentVersion, BindingVersion: head.CurrentBindingVersion, ObjectVersion: binding.ObjectVersion, AssociationVersion: binding.AssociationVersion, ExternalObject: binding.ExternalObject, EffectiveFrom: binding.EffectiveFrom, EffectiveUntil: binding.EffectiveUntil}, nil
}

func readVersionAtHead(ctx context.Context, repo ReadRepository, id ReadIdentity, head model.ServiceObjectHead) (model.ServiceObjectVersion, error) {
	return ReadServiceObjectVersionExact(ctx, repo, id, head.ID, head.CurrentVersion)
}

func readAssociationAtHead(ctx context.Context, repo ReadRepository, id ReadIdentity, head model.ServiceObjectHead) (model.ServiceObjectContractAssociation, error) {
	return ReadServiceObjectAssociationExact(ctx, repo, id, head.ID, head.CurrentAssociationVersion)
}

func decodeVersionRow(row VersionRow) (model.ServiceObjectVersion, error) {
	item := model.ServiceObjectVersion{ServiceObjectID: row.ServiceObjectID, Version: row.Version, ObjectType: row.ObjectType,
		DisplayName: row.DisplayName, BusinessCode: row.BusinessCode, State: row.State, EffectiveFrom: row.EffectiveFrom,
		ChangeDecisionID: row.ChangeDecisionID, BasisSummary: row.BasisSummary, PayloadSHA256: row.PayloadSHA256,
		CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt}
	if err := json.Unmarshal(row.Aliases, &item.Aliases); err != nil {
		return model.ServiceObjectVersion{}, fault.New(500, "integrity_error", "服务对象别名记录无效")
	}
	item.BasisRefs = append(json.RawMessage(nil), row.BasisRefs...)
	digest, err := model.CanonicalServiceObjectIdentitySHA256(item)
	if err != nil || digest != item.PayloadSHA256 {
		return model.ServiceObjectVersion{}, fault.New(500, "integrity_error", "服务对象身份版本摘要校验失败")
	}
	return item, nil
}

func decodeAssociationRow(row AssociationRow) (model.ServiceObjectContractAssociation, error) {
	item := model.ServiceObjectContractAssociation{ServiceObjectID: row.ServiceObjectID, AssociationVersion: row.AssociationVersion,
		ObjectVersion: row.ObjectVersion, MaterialID: row.MaterialID, MaterialVersion: row.MaterialVersion,
		DocumentID: row.DocumentID, ParsedDocumentID: row.ParsedDocumentID, SourceRunID: row.SourceRunID,
		SourceAttemptID: row.SourceAttemptID, ObjectRef: row.ObjectRef, FactsCanonicalJSON: row.FactsCanonicalJSON,
		FactsSHA256: row.FactsSHA256, IdentityChangeDecisionID: row.IdentityChangeDecisionID, PayloadSHA256: row.PayloadSHA256,
		ConfirmedBy: row.ConfirmedBy, ConfirmedAt: row.ConfirmedAt, CreatedAt: row.CreatedAt}
	item.BasisRefs = append(json.RawMessage(nil), row.BasisRefs...)
	factsDigest := sha256.Sum256(item.FactsCanonicalJSON)
	if hex.EncodeToString(factsDigest[:]) != item.FactsSHA256 || json.Unmarshal(item.FactsCanonicalJSON, &item.FactRefs) != nil {
		return model.ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联冻结事实校验失败")
	}
	if err := model.ValidateServiceObjectFactRefs(item); err != nil {
		return model.ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联引用未通过校验")
	}
	canonical, digest, err := model.CanonicalServiceObjectFacts(item.FactRefs)
	if err != nil || !bytes.Equal(canonical, item.FactsCanonicalJSON) || digest != item.FactsSHA256 {
		return model.ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联事实与冻结字节不一致")
	}
	payloadSHA, err := model.CanonicalServiceObjectAssociationSHA256(item)
	if err != nil || payloadSHA != item.PayloadSHA256 {
		return model.ServiceObjectContractAssociation{}, fault.New(500, "integrity_error", "服务对象关联摘要校验失败")
	}
	return item, nil
}
