package model

import "time"

// TransferApprovalTaskInput reassigns one live task. The first version has no
// standing delegation: only a tenant administrator may perform a transfer, and
// every transfer is an append-only fact.
type TransferApprovalTaskInput struct {
	TaskID                 string
	ToUserID               string
	Reason                 string
	ExpectedTaskRowVersion int64
	IdempotencyKey         string
}

type TransferApprovalTaskResult struct {
	TransferID    string    `json:"transfer_id"`
	TaskID        string    `json:"task_id"`
	FromUserID    string    `json:"from_user_id"`
	ToUserID      string    `json:"to_user_id"`
	ActorID       string    `json:"actor_id"`
	RowVersion    int64     `json:"row_version"`
	TransferredAt time.Time `json:"transferred_at"`
}
