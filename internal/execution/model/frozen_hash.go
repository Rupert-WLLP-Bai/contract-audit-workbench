package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// InputHashVersion returns the historical v1 default for snapshots written
// before explicit frozen-input versioning.
func InputHashVersion[PartyItem, KnowledgeSnapshot, OperatorContext any](snapshot RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext]) int {
	if snapshot.InputHashVersion == 0 {
		return 1
	}
	return snapshot.InputHashVersion
}

// FrozenInputHash hashes the canonical frozen snapshot and documents. Callers
// that derive review scope must do so before entering this pure function.
func FrozenInputHash[PartyItem, KnowledgeSnapshot, OperatorContext any, Original OriginalDocument](
	snapshot RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext],
	docs []FrozenDocument[Original],
) (string, error) {
	snapshot.RunID = ""
	snapshot.DocumentIDs = sortedCopy(snapshot.DocumentIDs)
	snapshot.ParsedDocumentIDs = sortedCopy(snapshot.ParsedDocumentIDs)

	var raw []byte
	var err error
	switch InputHashVersion(snapshot) {
	case 1:
		raw, err = json.Marshal(struct {
			Snapshot  RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext] `json:"snapshot"`
			Documents []FrozenDocument[Original]                                 `json:"documents"`
		}{Snapshot: snapshot, Documents: docs})
	case 2:
		if snapshot.AdoptedTenantConfigVersionID == "" || !validSHA256Hex(snapshot.AdoptedTenantConfigDigest) {
			return "", errors.New("invalid adopted tenant config for frozen input")
		}
		raw, err = json.Marshal(struct {
			Domain          string                                                     `json:"domain"`
			ConfigVersionID string                                                     `json:"config_version_id"`
			ConfigDigest    string                                                     `json:"config_digest"`
			Snapshot        RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext] `json:"snapshot"`
			Documents       []FrozenDocument[Original]                                 `json:"documents"`
		}{Domain: "review-frozen-input/v2", ConfigVersionID: snapshot.AdoptedTenantConfigVersionID,
			ConfigDigest: snapshot.AdoptedTenantConfigDigest, Snapshot: snapshot, Documents: docs})
	default:
		return "", errors.New("unsupported frozen input hash version")
	}
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}

// SelectionHash binds parser selection identity in ascending original-document
// ID order while preserving each pair's historical field order.
func SelectionHash[Original OriginalDocument](docs []FrozenDocument[Original]) string {
	pairs := make([][]string, len(docs))
	for i, item := range docs {
		proof := item.Document.OriginalProof()
		pairs[i] = []string{
			proof.ID, item.ParsedDocumentID, fmt.Sprint(item.SelectionOrder),
			item.ConfigHash, item.ParserID, item.ParserVersion, item.NormalizationVersion,
		}
	}
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && pairs[j][0] < pairs[j-1][0]; j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
	hash, _ := canonicalDigest(pairs)
	return hash
}

// FrozenConfigHash commits to the sorted distinct parser configuration hashes.
func FrozenConfigHash[Original OriginalDocument](docs []FrozenDocument[Original]) string {
	configurations := make([]string, len(docs))
	for i, item := range docs {
		configurations[i] = item.ConfigHash
	}
	slices.Sort(configurations)
	hash, _ := canonicalDigest(slices.Compact(configurations))
	return hash
}

// FrozenInputMatchesSnapshot checks the frozen parse identities without
// consulting current parser state.
func FrozenInputMatchesSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext any, Original OriginalDocument](
	snapshot RunSnapshot[PartyItem, KnowledgeSnapshot, OperatorContext],
	docs []FrozenDocument[Original],
) bool {
	ids := make([]string, len(docs))
	for i, doc := range docs {
		ids[i] = doc.ParsedDocumentID
	}
	return sameStringSet(snapshot.ParsedDocumentIDs, ids) &&
		snapshot.ParseSelectionHash == SelectionHash(docs) &&
		snapshot.ParserConfigurationID == FrozenConfigHash(docs)
}

// AttemptSelectionsMatchFrozen preserves legacy selections whose artifact
// digest was not stored while still matching all other exact frozen values.
func AttemptSelectionsMatchFrozen[Original OriginalDocument](
	selections []AttemptParseSelection,
	docs []FrozenDocument[Original],
) bool {
	if len(selections) != len(docs) {
		return false
	}
	byDocument := make(map[string]AttemptParseSelection, len(selections))
	for _, selection := range selections {
		if _, exists := byDocument[selection.DocumentID]; exists {
			return false
		}
		byDocument[selection.DocumentID] = selection
	}
	for _, frozen := range docs {
		proof := frozen.Document.OriginalProof()
		selection, ok := byDocument[proof.ID]
		if !ok || selection.OriginalDigest != proof.Digest || selection.ParsedDocumentID != frozen.ParsedDocumentID ||
			selection.SelectionOrder != frozen.SelectionOrder || selection.ParserID != frozen.ParserID ||
			selection.ParserVersion != frozen.ParserVersion || selection.ConfigHash != frozen.ConfigHash ||
			selection.NormalizationVersion != frozen.NormalizationVersion ||
			(selection.ArtifactDigest != "" && selection.ArtifactDigest != frozen.ParsedArtifactDigest) {
			return false
		}
	}
	return true
}

// AttemptOutputHash hashes the exact ordered output projection. Findings are
// copied before zeroing human concurrency versions, leaving caller state intact.
func AttemptOutputHash(
	findings []Finding,
	evaluations []ConstraintEval,
	rulePackHash, rulePackSnapshotDigest, rulePackCompilerRef, agentReviewDigest string,
) (string, error) {
	if findings != nil {
		copied := make([]Finding, len(findings))
		copy(copied, findings)
		findings = copied
	}
	for i := range findings {
		findings[i].Version = 0
	}
	raw, err := json.Marshal(struct {
		Findings               []Finding        `json:"findings"`
		Evaluations            []ConstraintEval `json:"evaluations"`
		RulePackHash           string           `json:"rulepack_hash,omitempty"`
		RulePackSnapshotDigest string           `json:"rulepack_snapshot_digest,omitempty"`
		RulePackCompilerRef    string           `json:"rulepack_compiler_ref,omitempty"`
		AgentReviewSHA256      string           `json:"agent_review_sha256,omitempty"`
	}{
		Findings: findings, Evaluations: evaluations,
		RulePackHash: rulePackHash, RulePackSnapshotDigest: rulePackSnapshotDigest,
		RulePackCompilerRef: rulePackCompilerRef, AgentReviewSHA256: agentReviewDigest,
	})
	if err != nil {
		return "", err
	}
	return digestBytes(raw), nil
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy, rightCopy := sortedCopy(left), sortedCopy(right)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

func sortedCopy(values []string) []string {
	if values == nil {
		return nil
	}
	copied := make([]string, len(values))
	copy(copied, values)
	sort.Strings(copied)
	return copied
}

func canonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(decoded) == value
}
