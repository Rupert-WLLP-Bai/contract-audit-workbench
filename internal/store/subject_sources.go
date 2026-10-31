package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

const syntheticSubjectSourceID = "synthetic.uscc.demo"

type SubjectSourceSnapshotListResult struct {
	Items      []SubjectSourceSnapshotResult `json:"items"`
	NextCursor *string                       `json:"next_cursor"`
}

type SubjectSourceSnapshotDetail struct {
	SubjectSourceSnapshotResult
	CaseID string `json:"case_id"`
}

// ConfigureSubjectSourceManifest installs an explicit, server-owned, versioned
// controlled dataset. The caller must load this from deployment configuration;
// a missing configuration intentionally leaves the directory empty.
func (s *Store) ConfigureSubjectSourceManifest(raw []byte) error {
	if len(raw) == 0 || len(raw) > 4<<20 {
		return errors.New("subject source manifest size is invalid")
	}
	var manifest SubjectSourceManifest
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode subject source manifest: %w", err)
	}
	if err := decoder.Decode(new(any)); err == nil {
		return errors.New("subject source manifest has trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("subject source manifest has invalid trailing data: %w", err)
	}
	if manifest.SourceID != syntheticSubjectSourceID || manifest.SourceVersion < 1 ||
		strings.TrimSpace(manifest.DisplayName) == "" || manifest.VerificationScope != "synthetic_controlled" ||
		len(manifest.Records) == 0 || len(manifest.Records) > 1000 {
		return errors.New("subject source manifest metadata is invalid")
	}
	seenIDs := map[string]bool{}
	for i := range manifest.Records {
		record := &manifest.Records[i]
		record.RecordID = strings.TrimSpace(record.RecordID)
		record.DisplayName = strings.TrimSpace(record.DisplayName)
		record.USCC = strings.ToUpper(strings.TrimSpace(record.USCC))
		if record.RecordID == "" || record.DisplayName == "" || !validUSCCFormat(record.USCC) ||
			record.SourceAsOf.IsZero() || record.SourceAsOf.After(time.Now().UTC()) || record.ValidFrom.IsZero() ||
			(record.ValidUntil != nil && !record.ValidUntil.After(record.ValidFrom)) || seenIDs[record.RecordID] {
			return errors.New("subject source record is invalid or duplicated")
		}
		record.SourceAsOf = record.SourceAsOf.UTC()
		record.ValidFrom = record.ValidFrom.UTC()
		if record.ValidUntil != nil {
			value := record.ValidUntil.UTC()
			record.ValidUntil = &value
		}
		seenIDs[record.RecordID] = true
	}
	sort.Slice(manifest.Records, func(i, j int) bool { return manifest.Records[i].RecordID < manifest.Records[j].RecordID })
	s.subjectSourcesMu.Lock()
	if previous := s.subjectSources; previous != nil {
		if manifest.SourceVersion < previous.SourceVersion {
			s.subjectSourcesMu.Unlock()
			return errors.New("subject source manifest version cannot move backwards")
		}
		if manifest.SourceVersion == previous.SourceVersion {
			oldPayload, _ := json.Marshal(previous)
			newPayload, _ := json.Marshal(manifest)
			if string(oldPayload) != string(newPayload) {
				s.subjectSourcesMu.Unlock()
				return errors.New("subject source manifest content changed without a version increment")
			}
		}
	}
	s.subjectSources = &manifest
	s.subjectSourcesMu.Unlock()
	return nil
}

func configureSubjectSourceFromEnvironment(s *Store) error {
	path := strings.TrimSpace(os.Getenv("CAW_SUBJECT_SOURCE_MANIFEST"))
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read configured subject source manifest: %w", err)
	}
	if err := s.ConfigureSubjectSourceManifest(raw); err != nil {
		return fmt.Errorf("configure subject source manifest: %w", err)
	}
	return nil
}

func (s *Store) configuredSubjectSource() *SubjectSourceManifest {
	s.subjectSourcesMu.RLock()
	defer s.subjectSourcesMu.RUnlock()
	if s.subjectSources == nil {
		return nil
	}
	copy := *s.subjectSources
	copy.Records = append([]SubjectSourceRecord(nil), s.subjectSources.Records...)
	for i := range copy.Records {
		if copy.Records[i].ValidUntil != nil {
			value := *copy.Records[i].ValidUntil
			copy.Records[i].ValidUntil = &value
		}
	}
	return &copy
}

func (s *Store) ListConfiguredSubjectSources(ctx context.Context, user accounts.User, caseID string) (SubjectSourceDirectoryResult, error) {
	if err := ctx.Err(); err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, strings.TrimSpace(caseID))
	if err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceDirectoryResult{}, err
	}
	manifest := s.configuredSubjectSource()
	items := make([]SubjectSourceDirectoryItem, 0, 1)
	if manifest != nil {
		items = append(items, SubjectSourceDirectoryItem{SourceID: manifest.SourceID, SourceVersion: manifest.SourceVersion,
			DisplayName: manifest.DisplayName, VerificationScope: manifest.VerificationScope})
	}
	return SubjectSourceDirectoryResult{Items: items}, nil
}

func (s *Store) CreateSubjectSourceSnapshot(ctx context.Context, user accounts.User, caseID string, input SubjectSourceSnapshotCreateInput, key string) (SubjectSourceSnapshotResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectSourceSnapshotResult{}, false, fault.New(503, "read_only", "只读模式下不能创建来源快照")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	caseID = strings.TrimSpace(caseID)
	input.SourceID = strings.TrimSpace(input.SourceID)
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	if input.SourceID != syntheticSubjectSourceID || input.ExpectedSourceVersion < 1 {
		return SubjectSourceSnapshotResult{}, false, fault.Invalid("受控来源及预期版本无效")
	}
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return SubjectSourceSnapshotResult{}, false, err
	}
	var result SubjectSourceSnapshotResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		if prior, ok, err := readSubjectIdempotency(*st, current, "create_subject_source_snapshot", caseID, key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		live := s.configuredSubjectSource()
		if live == nil || live.SourceID != input.SourceID || live.SourceVersion != input.ExpectedSourceVersion {
			return fault.Conflict("受控来源版本已变化，请刷新后重试")
		}
		payload, err := json.Marshal(live)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		digestHex := hex.EncodeToString(digest[:])
		for _, existing := range st.SubjectSourceSnapshots {
			if existing.TenantID == current.TenantID && existing.SourceCatalogID == live.SourceID && existing.SourceVersion == live.SourceVersion && existing.SourceDigest != digestHex {
				return fault.Conflict("同一受控来源版本已有不同内容，请提升来源版本")
			}
		}
		result = SubjectSourceSnapshotResult{SourceID: live.SourceID, SourceVersion: live.SourceVersion,
			SourceDigest: digestHex, SourceScope: live.VerificationScope, CapturedAt: time.Now().UTC(),
			RecordCount: len(live.Records), Label: live.DisplayName}
		snapshotID, err := newID("ssnap_")
		if err != nil {
			return err
		}
		result.SnapshotID = snapshotID
		st.SubjectSourceSnapshots = append(st.SubjectSourceSnapshots, SubjectSourceSnapshot{TenantID: current.TenantID, OrgID: caseItem.OwnerOrgID,
			CaseID: caseID, SnapshotID: snapshotID, SourceCatalogID: live.SourceID, SourceVersion: live.SourceVersion,
			SourcePayload: string(payload), SourceDigest: result.SourceDigest, SourceScope: live.VerificationScope,
			CapturedAt: result.CapturedAt, CreatedByUser: current.ID, CreatedByIssuer: current.Issuer, CreatedBySubject: current.Subject})
		return appendSubjectIdempotency(st, current, "create_subject_source_snapshot", caseID, key, requestHash, 201, result, time.Now().UTC())
	})
	return result, replayed, err
}

func decodeFrozenSubjectSourceSnapshot(item SubjectSourceSnapshot) (SubjectSourceManifest, error) {
	digest := sha256.Sum256([]byte(item.SourcePayload))
	if hex.EncodeToString(digest[:]) != item.SourceDigest {
		return SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照完整性校验失败")
	}
	var manifest SubjectSourceManifest
	decoder := json.NewDecoder(strings.NewReader(item.SourcePayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil || manifest.SourceID != item.SourceCatalogID || manifest.SourceVersion != item.SourceVersion ||
		manifest.VerificationScope != item.SourceScope || item.SourceScope != "synthetic_controlled" {
		return SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照内容与元数据不一致")
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || string(canonical) != item.SourcePayload {
		return SubjectSourceManifest{}, fault.New(500, "integrity_error", "来源快照不是规范序列化数据")
	}
	return manifest, nil
}

func subjectSourceSnapshotResult(item SubjectSourceSnapshot) (SubjectSourceSnapshotResult, error) {
	manifest, err := decodeFrozenSubjectSourceSnapshot(item)
	if err != nil {
		return SubjectSourceSnapshotResult{}, err
	}
	return SubjectSourceSnapshotResult{SnapshotID: item.SnapshotID, SourceID: item.SourceCatalogID, SourceVersion: item.SourceVersion,
		SourceDigest: item.SourceDigest, SourceScope: item.SourceScope, CapturedAt: item.CapturedAt,
		RecordCount: len(manifest.Records), Label: manifest.DisplayName}, nil
}

func (s *Store) ListSubjectSourceSnapshots(ctx context.Context, user accounts.User, caseID, cursor string, limit int) (SubjectSourceSnapshotListResult, error) {
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	caseID = strings.TrimSpace(caseID)
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	caseItem, err := s.visibleCase(&st, current, caseID)
	if err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceSnapshotListResult{}, err
	}
	if limit < 1 {
		limit = 50
	}
	if limit > 100 {
		return SubjectSourceSnapshotListResult{}, fault.Invalid("每页来源快照最多为100")
	}
	items := make([]SubjectSourceSnapshot, 0)
	for _, item := range st.SubjectSourceSnapshots {
		if item.TenantID == current.TenantID && item.CaseID == caseID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].SnapshotID < items[j].SnapshotID })
	start := 0
	if cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return SubjectSourceSnapshotListResult{}, fault.Invalid("来源快照游标无效")
		}
		start = sort.Search(len(items), func(i int) bool { return items[i].SnapshotID > string(decoded) })
	}
	if start >= len(items) {
		return SubjectSourceSnapshotListResult{Items: []SubjectSourceSnapshotResult{}}, nil
	}
	end := min(start+limit, len(items))
	result := SubjectSourceSnapshotListResult{Items: make([]SubjectSourceSnapshotResult, 0, end-start)}
	for _, item := range items[start:end] {
		projected, err := subjectSourceSnapshotResult(item)
		if err != nil {
			return SubjectSourceSnapshotListResult{}, err
		}
		result.Items = append(result.Items, projected)
	}
	if end < len(items) {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(items[end-1].SnapshotID))
		result.NextCursor = &cursor
	}
	return result, nil
}

func (s *Store) GetSubjectSourceSnapshot(ctx context.Context, user accounts.User, caseID, snapshotID string) (SubjectSourceSnapshotDetail, error) {
	if err := ctx.Err(); err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	st, err := s.snapshotAsContext(ctx, current)
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	caseItem, err := s.visibleCase(&st, current, strings.TrimSpace(caseID))
	if err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	if err := requireCaseAction(caseItem, current, access.Read); err != nil {
		return SubjectSourceSnapshotDetail{}, err
	}
	for _, item := range st.SubjectSourceSnapshots {
		if item.TenantID == current.TenantID && item.CaseID == caseItem.ID && item.SnapshotID == strings.TrimSpace(snapshotID) {
			projected, err := subjectSourceSnapshotResult(item)
			if err != nil {
				return SubjectSourceSnapshotDetail{}, err
			}
			return SubjectSourceSnapshotDetail{SubjectSourceSnapshotResult: projected, CaseID: caseItem.ID}, nil
		}
	}
	return SubjectSourceSnapshotDetail{}, fault.NotFound("没有找到对应来源快照")
}

func (s *Store) VerifySubjectFromControlledSource(ctx context.Context, user accounts.User, caseID, snapshotID string, input SubjectSourceVerifyInput, key string) (SubjectSourceVerifyResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	if s.ReadOnly() {
		return SubjectSourceVerifyResult{}, false, fault.New(503, "read_only", "只读模式下不能核验主体来源")
	}
	current, err := trustedUser(user)
	if err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	caseID, snapshotID, input.SubjectID = strings.TrimSpace(caseID), strings.TrimSpace(snapshotID), strings.TrimSpace(input.SubjectID)
	if input.SubjectID == "" || input.ExpectedSubjectVersion < 1 {
		return SubjectSourceVerifyResult{}, false, fault.Invalid("主体及预期版本必填")
	}
	if err := validateIdempotencyKey(key); err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	targetID := caseID + "/" + snapshotID + "/" + input.SubjectID
	requestHash, err := canonicalSubjectHash(input)
	if err != nil {
		return SubjectSourceVerifyResult{}, false, err
	}
	var result SubjectSourceVerifyResult
	replayed := false
	err = s.updateCaseAsContext(ctx, current, caseID, func(st *state) error {
		caseItem, err := s.writableCase(st, current, caseID, access.Manage)
		if err != nil {
			return err
		}
		if prior, ok, err := readSubjectIdempotency(*st, current, "verify_subject_source", targetID, key, requestHash); err != nil {
			return err
		} else if ok {
			replayed = true
			return json.Unmarshal(prior.Response, &result)
		}
		var snapshot *SubjectSourceSnapshot
		for i := range st.SubjectSourceSnapshots {
			item := &st.SubjectSourceSnapshots[i]
			if item.TenantID == current.TenantID && item.OrgID == caseItem.OwnerOrgID && item.CaseID == caseID && item.SnapshotID == snapshotID {
				snapshot = item
				break
			}
		}
		if snapshot == nil {
			return fault.NotFound("没有找到案件来源快照")
		}
		manifest, err := decodeFrozenSubjectSourceSnapshot(*snapshot)
		if err != nil {
			return err
		}
		subject, version, visible := visibleSubject(*st, current, input.SubjectID)
		if !visible || subject.TenantID != current.TenantID || subject.CurrentVersion != input.ExpectedSubjectVersion || !caseHasSubject(st, caseID, subject.ID) {
			return fault.NotFound("没有找到对应案件主体")
		}
		claimedUSCC := ""
		for _, claim := range version.IdentityClaims {
			if claim.Namespace == "cn.gov.samr.uscc" && claim.IdentifierType == "unified_social_credit_code" {
				if claim.VerificationState == "verified" && claim.VerificationScope != "synthetic_controlled" {
					return fault.Conflict("当前主体已有其他来源核验，不能由synthetic受控来源替换")
				}
				claimedUSCC = strings.ToUpper(strings.TrimSpace(claim.Value))
				break
			}
		}
		usccMatches := make([]SubjectSourceRecord, 0, 1)
		for _, record := range manifest.Records {
			if claimedUSCC != "" && record.USCC == claimedUSCC {
				usccMatches = append(usccMatches, record)
			}
		}
		result = SubjectSourceVerifyResult{State: "not_proposed", MatchOutcome: "no_match", SnapshotID: snapshot.SnapshotID, SourceCatalogID: snapshot.SourceCatalogID,
			SourceVersion: snapshot.SourceVersion, SourceDigest: snapshot.SourceDigest, SourceScope: snapshot.SourceScope}
		operationStatus := 200
		if len(usccMatches) > 1 {
			// Without an explicit record/as-of selector, conflicting entries for
			// one identifier are ambiguous even when only one name happens to fit.
			result.MatchOutcome = "ambiguous"
		} else if len(usccMatches) == 1 && (usccMatches[0].DisplayName == version.DisplayName || (version.LegalName != "" && usccMatches[0].DisplayName == version.LegalName)) {
			record := usccMatches[0]
			asOf, validFrom, validUntil := record.SourceAsOf.UTC(), record.ValidFrom.UTC(), (*time.Time)(nil)
			if record.ValidUntil != nil {
				value := record.ValidUntil.UTC()
				validUntil = &value
			}
			result.SourceAsOf = &asOf
			result.ValidFrom = &validFrom
			result.ValidUntil = validUntil
			result.MatchOutcome = "exact_match"
			result.MatchedRecord = &SubjectSourceMatchedRecord{DisplayName: record.DisplayName, MaskedIdentifier: maskUSCC(record.USCC)}
			version.Version = subject.CurrentVersion + 1
			version.VerificationState, version.VerificationScope, version.SourceKind = "verified", "synthetic_controlled", "authorized_correction"
			version.BasisSummary = "按版本化synthetic_controlled受控来源精确比对名称和统一社会信用代码"
			version.CreatedByUser, version.CreatedByIssuer, version.CreatedBySubject = current.ID, current.Issuer, current.Subject
			version.CreatedAt = time.Now().UTC()
			version.IdentityClaims = []SubjectIdentityClaim{{Namespace: "cn.gov.samr.uscc", IdentifierType: "unified_social_credit_code", Value: record.USCC,
				VerificationState: "verified", VerificationScope: "synthetic_controlled", SourceKind: "synthetic_controlled_snapshot", SourceTime: &asOf,
				SourceCatalogID: snapshot.SourceCatalogID, SourceSnapshotID: snapshot.SnapshotID, SourceDigest: snapshot.SourceDigest,
				SourceValidFrom: &validFrom, SourceValidUntil: validUntil}}
			version.IdentityClaims[0].PayloadHash = hashSubjectClaim(version.IdentityClaims[0])
			version.PayloadHash = hashSubjectVersion(version)
			canonical, err := json.Marshal(subjectAfterPayload{SubjectVersion: version})
			if err != nil {
				return err
			}
			basis, err := json.Marshal([]ControlledSourceBasisRef{{Kind: "controlled_source", SnapshotID: snapshot.SnapshotID, SourceCatalogID: snapshot.SourceCatalogID,
				SourceVersion: snapshot.SourceVersion, SourceDigest: snapshot.SourceDigest, SourceScope: snapshot.SourceScope, SourceAsOf: asOf,
				ValidFrom: validFrom, ValidUntil: validUntil, CapturedAt: snapshot.CapturedAt}})
			if err != nil {
				return err
			}
			bindings := affectedSubjectBindings(*st, current.TenantID, subject.ID, "subject", "")
			bindingsHash, err := canonicalSubjectHash(bindings)
			if err != nil {
				return err
			}
			requestID, err := newID("sch_")
			if err != nil {
				return err
			}
			request := SubjectChangeRequest{TenantID: current.TenantID, OrgID: caseItem.OwnerOrgID, RequestID: requestID, SourceCaseID: caseID,
				TargetKind: "subject", TargetSubjectID: subject.ID, ExpectedSubjectVersion: subject.CurrentVersion, CanonicalPayload: canonical,
				CanonicalPayloadHash: hashJSON(canonical), BasisNote: "按版本化受控来源精确比对名称与统一社会信用代码；来源范围为synthetic_controlled。",
				BasisRefs: basis, ExpectedBindingsHash: bindingsHash, ProposedBy: TrustedPrincipalProjection{UserID: current.ID, Issuer: current.Issuer, Subject: current.Subject}, CreatedAt: time.Now().UTC()}
			st.SubjectChangeRequests = append(st.SubjectChangeRequests, request)
			result.RequestID, result.State = &requestID, "pending_decision"
			operationStatus = 202
		}
		return appendSubjectIdempotency(st, current, "verify_subject_source", targetID, key, requestHash, operationStatus, result, time.Now().UTC())
	})
	return result, replayed, err
}
