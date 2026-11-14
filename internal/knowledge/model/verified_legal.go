package model

// PreparedVerifiedLegalDraft is a Knowledge-owned immutable draft plan. Its
// scope key is deliberately derived only after the Legal capture is inserted.
type PreparedVerifiedLegalDraft struct {
	Ref          KnowledgeRef
	Content      GovernedKnowledgeContent
	ContentBytes []byte
}
