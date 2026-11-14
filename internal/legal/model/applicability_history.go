package model

type LegalApplicabilityHistoryInput struct {
	Cursor string
	Limit  int
}

// LegalApplicabilityFactHistory is the authored immutable history and current CAS head.
type LegalApplicabilityFactHistory struct {
	CaseID         string                          `json:"case_id"`
	Dimension      string                          `json:"dimension"`
	CurrentVersion int64                           `json:"current_version"`
	Items          []LegalApplicabilityFactVersion `json:"items"`
	NextCursor     *string                         `json:"next_cursor"`
	CanManage      bool                            `json:"can_manage"`
}
