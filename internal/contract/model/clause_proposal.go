package contract

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// ClauseEvidence pins the selected text to one frozen parsing result. Offsets
// are Unicode code points, as in the existing evidence citation protocol.
type ClauseEvidence struct {
	DocumentID       string `json:"document_id"`
	ParsedDocumentID string `json:"parsed_document_id"`
	NormalizedDigest string `json:"normalized_digest"`
	Start            int    `json:"start"`
	End              int    `json:"end"`
	Quote            string `json:"quote"`
}

type ClauseProposalCreate struct {
	CaseID         string          `json:"case_id"`
	RunID          string          `json:"run_id"`
	AttemptID      string          `json:"attempt_id"`
	FindingID      string          `json:"finding_id,omitempty"`
	Evidence       *ClauseEvidence `json:"evidence,omitempty"`
	ProposedText   string          `json:"proposed_text"`
	Reason         string          `json:"reason"`
	RemediationID  string          `json:"remediation_id,omitempty"`
	IdempotencyKey string          `json:"-"`
}

type ClauseProposalDecision struct {
	Action                     string `json:"action"`
	Confirmed                  bool   `json:"confirmed"`
	ExpectedVersion            int64  `json:"expected_version"`
	ContractID                 string `json:"contract_id,omitempty"`
	ExpectedContractRowVersion int64  `json:"expected_contract_row_version,omitempty"`
	AcceptedText               string `json:"accepted_text,omitempty"`
	Reason                     string `json:"reason,omitempty"`
	MaterialID                 string `json:"material_id,omitempty"`
	DocumentID                 string `json:"document_id,omitempty"`
	SHA256                     string `json:"sha256,omitempty"`
	IdempotencyKey             string `json:"-"`
}

// ReplaceClause produces the only accepted text for a derived plaintext
// material. It never guesses an occurrence or changes adjacent text.
func ReplaceClause(original string, evidence ClauseEvidence, replacement string) (string, error) {
	if !utf8.ValidString(original) || !utf8.ValidString(evidence.Quote) || !utf8.ValidString(replacement) || strings.TrimSpace(replacement) == "" || len(replacement) > 16000 {
		return "", errors.New("invalid clause text")
	}
	runes := []rune(original)
	if evidence.Start < 0 || evidence.End <= evidence.Start || evidence.End > len(runes) || string(runes[evidence.Start:evidence.End]) != evidence.Quote {
		return "", errors.New("clause evidence does not match frozen text")
	}
	return string(runes[:evidence.Start]) + replacement + string(runes[evidence.End:]), nil
}

// ValidateClauseProposalCreate accepts a candidate without evidence so that it
// can be displayed for human inspection; execution requires verified evidence.
func ValidateClauseProposalCreate(input ClauseProposalCreate) error {
	if strings.TrimSpace(input.CaseID) == "" || strings.TrimSpace(input.RunID) == "" || strings.TrimSpace(input.AttemptID) == "" ||
		!utf8.ValidString(input.ProposedText) || strings.TrimSpace(input.ProposedText) == "" || len(input.ProposedText) > 16000 ||
		!utf8.ValidString(input.Reason) || len(input.Reason) > 4000 {
		return errors.New("invalid clause proposal")
	}
	return nil
}

// ValidateClauseProposalDecision validates the user's explicit choice before
// any persistence; the caller must independently recheck permissions and CAS.
func ValidateClauseProposalDecision(input ClauseProposalDecision) error {
	if !input.Confirmed || input.ExpectedVersion < 1 || !utf8.ValidString(input.Reason) || len(input.Reason) > 4000 {
		return errors.New("invalid clause proposal decision")
	}
	switch input.Action {
	case "reject":
		if input.ContractID != "" || input.ExpectedContractRowVersion != 0 || input.AcceptedText != "" || input.MaterialID != "" || input.DocumentID != "" || input.SHA256 != "" {
			return errors.New("rejection must not contain revision input")
		}
	case "accept", "accept_edited":
		if input.ContractID == "" || input.ExpectedContractRowVersion < 1 || input.MaterialID == "" || input.DocumentID == "" || len(input.SHA256) != 64 {
			return errors.New("acceptance requires a contract and uploaded material")
		}
		for _, c := range input.SHA256 {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				return errors.New("invalid material digest")
			}
		}
		if input.Action == "accept" && input.AcceptedText != "" {
			return errors.New("unedited acceptance cannot replace proposal text")
		}
		if input.Action == "accept_edited" && (!utf8.ValidString(input.AcceptedText) || strings.TrimSpace(input.AcceptedText) == "" || len(input.AcceptedText) > 16000) {
			return errors.New("edited acceptance requires replacement text")
		}
	default:
		return errors.New("unknown clause proposal decision")
	}
	return nil
}

// ClauseProposal records the source identity and the result of server-side
// evidence verification. An unverifiable candidate remains readable only.
type ClauseProposal struct {
	ID                string                  `json:"id"`
	CreatedBy         string                  `json:"created_by"`
	CreatedAt         string                  `json:"created_at"`
	CaseID            string                  `json:"case_id"`
	RunID             string                  `json:"run_id"`
	AttemptID         string                  `json:"attempt_id"`
	InputHash         string                  `json:"input_hash"`
	FindingID         string                  `json:"finding_id,omitempty"`
	Evidence          *ClauseEvidence         `json:"evidence,omitempty"`
	OriginalText      string                  `json:"original_text,omitempty"`
	BeforeContext     string                  `json:"before_context,omitempty"`
	AfterContext      string                  `json:"after_context,omitempty"`
	ProposedText      string                  `json:"proposed_text"`
	Reason            string                  `json:"reason"`
	RemediationID     string                  `json:"remediation_id,omitempty"`
	EvidenceVerified  bool                    `json:"evidence_verified"`
	UnavailableReason string                  `json:"unavailable_reason,omitempty"`
	RulePack          *ClauseRulePackRef      `json:"rule_pack,omitempty"`
	Playbook          *ClausePlaybookRef      `json:"playbook,omitempty"`
	Status            string                  `json:"status"`
	Version           int64                   `json:"version"`
	History           []ClauseProposalHistory `json:"history"`
}

type ClauseProposalHistory struct {
	Version      int64  `json:"version"`
	ContractID   string `json:"contract_id,omitempty"`
	SHA256       string `json:"sha256,omitempty"`
	Action       string `json:"action"`
	ActorID      string `json:"actor_id"`
	Reason       string `json:"reason,omitempty"`
	AcceptedText string `json:"accepted_text,omitempty"`
	MaterialID   string `json:"material_id,omitempty"`
	DocumentID   string `json:"document_id,omitempty"`
	RevisionID   string `json:"revision_id,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// VerifyDerivedClauseText checks the complete parsed replacement document, not
// just the quoted clause. A matching substring alone cannot prove that the
// rest of the original text was preserved.
func VerifyDerivedClauseText(original string, evidence ClauseEvidence, replacement, derived string) error {
	expected, err := ReplaceClause(original, evidence, replacement)
	if err != nil {
		return err
	}
	if derived != expected {
		return errors.New("derived material does not match the frozen text with the confirmed replacement")
	}
	return nil
}

// DerivedClauseText is available only for a still-pending, verified proposal.
// The caller supplies the exact frozen text after its current authorization and
// identity checks; an optional edited value is an explicit user choice.
func DerivedClauseText(proposal ClauseProposal, expectedVersion int64, frozenText string, editedText *string) (string, error) {
	if proposal.Status != "pending" || proposal.Version != expectedVersion || !proposal.EvidenceVerified || proposal.Evidence == nil {
		return "", errors.New("proposal is not available for verified replacement")
	}
	replacement := proposal.ProposedText
	if editedText != nil {
		replacement = *editedText
	}
	return ReplaceClause(frozenText, *proposal.Evidence, replacement)
}

// ClauseRulePackRef and ClausePlaybookRef expose identity only, not governed content.
type ClauseRulePackRef struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	ContentHash    string `json:"content_hash"`
	SnapshotDigest string `json:"snapshot_digest"`
	CompilerRef    string `json:"compiler_ref"`
}
type ClausePlaybookRef struct {
	KnowledgeID   string `json:"knowledge_id"`
	VersionID     string `json:"version_id"`
	ContentSHA256 string `json:"content_sha256"`
}
