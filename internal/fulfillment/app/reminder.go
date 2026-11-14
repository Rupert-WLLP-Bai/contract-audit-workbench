// File metadata (manifest mirror):
// Purpose: Plans and manages revision-bound fulfillment reminders using confirmed dates, authorization, idempotency and transactional events.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	contractapp "contractaudit/internal/contract/app"
	contract "contractaudit/internal/contract/model"
	"contractaudit/internal/fault"
)

// ReminderPlan is a date explicitly recorded in a frozen contract revision.
type ReminderPlan struct {
	Kind        string `json:"kind"`
	Sequence    int    `json:"sequence"`
	PlannedDate string `json:"planned_date"`
	Description string `json:"description"`
}

type ReminderCreateInput struct {
	ContractRevisionID         string `json:"contract_revision_id"`
	PlanKind                   string `json:"plan_kind"`
	PlanSequence               int    `json:"plan_sequence"`
	Timezone                   string `json:"timezone"`
	RecipientID                string `json:"recipient_id"`
	LeadDays                   int    `json:"lead_days"`
	ExpectedContractRowVersion int64  `json:"expected_contract_row_version"`
}

// FrozenReminderPlans only exposes explicitly dated source plan items. An
// absent or malformed date is not treated as an inferred obligation.
func FrozenReminderPlans(revision contract.FrozenContractRevision) ([]ReminderPlan, error) {
	draft, err := contractapp.DecodeFulfillmentSnapshot(revision)
	if err != nil {
		return nil, err
	}
	result := []ReminderPlan{}
	counts := map[string]int{}
	for _, plan := range draft.DeliveryPlans {
		counts[fmt.Sprintf("delivery:%d", plan.Sequence)]++
	}
	for _, plan := range draft.AcceptancePlans {
		counts[fmt.Sprintf("acceptance:%d", plan.Sequence)]++
	}
	for _, plan := range draft.Payments {
		counts[fmt.Sprintf("payment:%d", plan.Sequence)]++
	}
	appendPlan := func(kind string, sequence int, date, description string) {
		if sequence <= 0 || counts[fmt.Sprintf("%s:%d", kind, sequence)] != 1 {
			return
		}
		date = strings.TrimSpace(date)
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || parsed.Format("2006-01-02") != date {
			return
		}
		result = append(result, ReminderPlan{Kind: kind, Sequence: sequence, PlannedDate: date, Description: description})
	}
	for _, plan := range draft.DeliveryPlans {
		appendPlan("delivery", plan.Sequence, plan.PlannedDate, plan.Description)
	}
	for _, plan := range draft.AcceptancePlans {
		appendPlan("acceptance", plan.Sequence, plan.PlannedDate, plan.Description)
	}
	for _, plan := range draft.Payments {
		appendPlan("payment", plan.Sequence, plan.PlannedDate, plan.Description)
	}
	return result, nil
}

// ResolveReminderPlan rejects ambiguous identities and computes the notification
// at 09:00 local time on the requested calendar day before the source date.
func ResolveReminderPlan(plans []ReminderPlan, input ReminderCreateInput) (ReminderPlan, time.Time, error) {
	input.PlanKind = strings.TrimSpace(input.PlanKind)
	if input.PlanKind != "delivery" && input.PlanKind != "acceptance" && input.PlanKind != "payment" {
		return ReminderPlan{}, time.Time{}, fault.Invalid("提醒计划类型无效")
	}
	if input.PlanSequence <= 0 || input.LeadDays < 0 || input.LeadDays > 365 || strings.TrimSpace(input.RecipientID) == "" || strings.TrimSpace(input.Timezone) == "" {
		return ReminderPlan{}, time.Time{}, fault.Invalid("提醒责任人、提前天数或时区无效")
	}
	location, err := time.LoadLocation(input.Timezone)
	if err != nil || input.Timezone == "Local" {
		return ReminderPlan{}, time.Time{}, fault.Invalid("提醒时区无效")
	}
	var found ReminderPlan
	count := 0
	for _, plan := range plans {
		if plan.Kind == input.PlanKind && plan.Sequence == input.PlanSequence {
			found = plan
			count++
		}
	}
	if count != 1 {
		return ReminderPlan{}, time.Time{}, fault.Conflict("冻结计划日期不存在或存在歧义")
	}
	date, err := time.Parse("2006-01-02", found.PlannedDate)
	if err != nil {
		return ReminderPlan{}, time.Time{}, fault.Conflict("冻结计划日期无效")
	}
	local := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, location).AddDate(0, 0, -input.LeadDays)
	if local.Hour() != 9 {
		return ReminderPlan{}, time.Time{}, fault.Invalid("提醒时间在所选时区不可用")
	}
	return found, local.UTC(), nil
}

// Reminder is a confirmed, revision-bound plan. Delivery is a separate fact.
type Reminder struct {
	ReminderID         string    `json:"reminder_id"`
	ContractID         string    `json:"contract_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	PlanKind           string    `json:"plan_kind"`
	PlanSequence       int       `json:"plan_sequence"`
	PlannedDate        string    `json:"planned_date"`
	Timezone           string    `json:"timezone"`
	RecipientID        string    `json:"recipient_id"`
	LeadDays           int       `json:"lead_days"`
	DueAt              time.Time `json:"due_at"`
	Status             string    `json:"status"`
	Version            int64     `json:"version"`
	ConfirmedBy        string    `json:"confirmed_by"`
	ConfirmedAt        time.Time `json:"confirmed_at"`
}

type ReminderDelivery struct {
	ReminderID         string    `json:"reminder_id"`
	ContractID         string    `json:"contract_id"`
	ContractRevisionID string    `json:"contract_revision_id"`
	PlanKind           string    `json:"plan_kind"`
	PlanSequence       int       `json:"plan_sequence"`
	PlannedDate        string    `json:"planned_date"`
	Timezone           string    `json:"timezone"`
	DeliveredAt        time.Time `json:"delivered_at"`
}

type ReminderInbox struct {
	Items []ReminderDelivery `json:"items"`
}

type ReminderList struct {
	ContractRevisionID string         `json:"contract_revision_id"`
	ContractRowVersion int64          `json:"contract_row_version"`
	Plans              []ReminderPlan `json:"plans"`
	Reminders          []Reminder     `json:"reminders"`
}

// ConfirmedReminderPlan validates a Contract-owned seal and requires the exact
// current active, registered revision before calculating a reminder.
func ConfirmedReminderPlan(record contract.Aggregate, revision contract.FrozenContractRevision, seal *contract.ContractVersionSeal, input ReminderCreateInput) (ReminderPlan, time.Time, error) {
	if record.AuthorityMode != "native" || record.ValidityState != "active" || (record.LifecycleState != "registered" && record.LifecycleState != "performing") {
		return ReminderPlan{}, time.Time{}, fault.Conflict("合同当前状态不能设置履约提醒")
	}
	if input.ExpectedContractRowVersion <= 0 || record.RowVersion != input.ExpectedContractRowVersion {
		return ReminderPlan{}, time.Time{}, fault.Conflict("合同已被修改，请刷新后重试")
	}
	if record.CurrentRevisionID == nil || *record.CurrentRevisionID != input.ContractRevisionID || revision.RevisionID != input.ContractRevisionID {
		return ReminderPlan{}, time.Time{}, fault.Conflict("提醒必须对应合同当前冻结修订")
	}
	if err := VerifyReminderRevision(revision, seal); err != nil {
		return ReminderPlan{}, time.Time{}, err
	}
	plans, err := FrozenReminderPlans(revision)
	if err != nil {
		return ReminderPlan{}, time.Time{}, err
	}
	return ResolveReminderPlan(plans, input)
}

type ReminderRecipient struct {
	ActorID, OrgID            string
	PrincipalID, MembershipID *string
}
type ReminderInsert struct {
	TenantID, ContractID, RevisionID, ReminderID, JobID string
	Plan                                                ReminderPlan
	Timezone, RecipientID, RecipientOrgID               string
	RecipientPrincipalID, RecipientMembershipID         *string
	LeadDays                                            int
	DueAt                                               time.Time
	ConfirmedBy                                         string
}
type ReminderRepository interface {
	Insert(context.Context, ReminderInsert) (Reminder, error)
	List(context.Context, string, string) ([]Reminder, error)
	Cancel(context.Context, string, string, string, string, int64) (Reminder, string, error)
}
type ReminderAuthority interface {
	FulfillmentAccess
	ResolveReminderRecipient(context.Context, contract.Aggregate, string) (ReminderRecipient, error)
}
type ReminderScheduler interface {
	EnqueueReminder(context.Context, string, string, string, string, time.Time) error
	CancelReminderJob(context.Context, string, string, string) error
}
type ReminderSealReader interface {
	LoadVersionSeal(context.Context, string, string, string) (*contract.ContractVersionSeal, error)
}

const CreateReminderRoute = "fulfillment.reminder.create"
const CancelReminderRoute = "fulfillment.reminder.cancel"

func CreateReminder(ctx context.Context, principal Principal, contractID, key string, input ReminderCreateInput,
	contracts contractapp.FulfillmentContractCommands, seals ReminderSealReader, repo ReminderRepository, authority ReminderAuthority,
	scheduler ReminderScheduler, events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (Reminder, error) {
	id, err := contract.NormalizeContractUUID(contractID)
	if err != nil {
		return Reminder{}, err
	}
	revisionID, err := contract.NormalizeContractUUID(input.ContractRevisionID)
	if err != nil {
		return Reminder{}, err
	}
	input.ContractRevisionID = revisionID
	if err = contract.ValidateCLMIdempotencyKey(key); err != nil {
		return Reminder{}, err
	}
	digest, err := contract.RequestDigest(CreateReminderRoute, "POST", "/api/contracts/"+id+"/reminders", input.ExpectedContractRowVersion, input)
	if err != nil {
		return Reminder{}, err
	}
	receipt, found, err := loadReplayReceipt(ctx, principal, CreateReminderRoute, key, digest, id, contracts, authority)
	if err != nil {
		return Reminder{}, err
	}
	if found {
		var previous Reminder
		if err = json.Unmarshal(receipt.Response, &previous); err != nil {
			return Reminder{}, err
		}
		return previous, nil
	}
	if err = contracts.LockContract(ctx, id); err != nil {
		return Reminder{}, err
	}
	record, err := loadWritableContract(ctx, principal.TenantID, id, input.ExpectedContractRowVersion, contracts, authority)
	if err != nil {
		return Reminder{}, err
	}
	if !authority.CanRead(ctx, record) {
		return Reminder{}, fault.NotFound("没有找到对应记录")
	}
	revision, err := contracts.LoadFulfillmentRevision(ctx, principal.TenantID, id, revisionID)
	if err != nil {
		return Reminder{}, err
	}
	seal, err := seals.LoadVersionSeal(ctx, principal.TenantID, id, revisionID)
	if err != nil {
		return Reminder{}, err
	}
	plan, due, err := ConfirmedReminderPlan(record, revision, seal, input)
	if err != nil {
		return Reminder{}, err
	}
	recipient, err := authority.ResolveReminderRecipient(ctx, record, input.RecipientID)
	if err != nil {
		return Reminder{}, err
	}
	reminderID, err := contract.NewContractUUID()
	if err != nil {
		return Reminder{}, err
	}
	jobID, err := contract.NewContractUUID()
	if err != nil {
		return Reminder{}, err
	}
	jobID = "reminder_" + jobID
	created, err := repo.Insert(ctx, ReminderInsert{TenantID: principal.TenantID, ContractID: id, RevisionID: revisionID, ReminderID: reminderID, JobID: jobID, Plan: plan, Timezone: input.Timezone, RecipientID: recipient.ActorID, RecipientOrgID: recipient.OrgID, RecipientPrincipalID: recipient.PrincipalID, RecipientMembershipID: recipient.MembershipID, LeadDays: input.LeadDays, DueAt: due, ConfirmedBy: principal.ActorID})
	if err != nil {
		return Reminder{}, err
	}
	if err = scheduler.EnqueueReminder(ctx, principal.TenantID, id, reminderID, jobID, due); err != nil {
		return Reminder{}, err
	}
	if err = appendFulfillmentEvent(ctx, record, record.RowVersion, "fulfillment.reminder.confirmed", map[string]any{"reminder_id": reminderID, "contract_revision_id": revisionID}, events, outbox); err != nil {
		return Reminder{}, err
	}
	response, err := json.Marshal(created)
	if err != nil {
		return Reminder{}, err
	}
	if err = contracts.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, CreateReminderRoute, key, digest, id, 201, response); err != nil {
		return Reminder{}, err
	}
	return created, nil
}

type ReminderCancelInput struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func CancelReminder(ctx context.Context, principal Principal, contractID, reminderID, key string, input ReminderCancelInput,
	contracts contractapp.FulfillmentContractCommands, repo ReminderRepository, authority ReminderAuthority, scheduler ReminderScheduler,
	events contractapp.VersionBusinessEventAppender, outbox contractapp.VersionOutboxEnqueuer) (Reminder, error) {
	id, err := contract.NormalizeContractUUID(contractID)
	if err != nil {
		return Reminder{}, err
	}
	reminderID, err = contract.NormalizeContractUUID(reminderID)
	if err != nil {
		return Reminder{}, err
	}
	if input.ExpectedVersion < 1 {
		return Reminder{}, fault.Invalid("expected_version 必须为正数")
	}
	if err = contract.ValidateCLMIdempotencyKey(key); err != nil {
		return Reminder{}, err
	}
	path := "/api/contracts/" + id + "/reminders/" + reminderID + "/cancel"
	digest, err := contract.RequestDigest(CancelReminderRoute, "POST", path, input.ExpectedVersion, input)
	if err != nil {
		return Reminder{}, err
	}
	receipt, found, err := loadReplayReceipt(ctx, principal, CancelReminderRoute, key, digest, id, contracts, authority)
	if err != nil {
		return Reminder{}, err
	}
	if found {
		var previous Reminder
		if err = json.Unmarshal(receipt.Response, &previous); err != nil {
			return Reminder{}, err
		}
		return previous, nil
	}
	if err = contracts.LockContract(ctx, id); err != nil {
		return Reminder{}, err
	}
	record, err := contracts.LoadContract(ctx, principal.TenantID, id)
	if err != nil {
		return Reminder{}, err
	}
	if !authority.CanRead(ctx, record) || !authority.CanReadForManagement(ctx, record) {
		return Reminder{}, fault.NotFound("没有找到对应记录")
	}
	if !authority.CanManage(ctx, record) {
		return Reminder{}, fault.Forbidden("当前授权不能管理合同履约提醒")
	}
	item, _, err := repo.Cancel(ctx, principal.TenantID, id, reminderID, principal.ActorID, input.ExpectedVersion)
	if err != nil {
		return Reminder{}, err
	}
	if err = scheduler.CancelReminderJob(ctx, principal.TenantID, id, reminderID); err != nil {
		return Reminder{}, err
	}
	if err = appendFulfillmentEvent(ctx, record, record.RowVersion, "fulfillment.reminder.cancelled", map[string]any{"reminder_id": reminderID}, events, outbox); err != nil {
		return Reminder{}, err
	}
	response, err := json.Marshal(item)
	if err != nil {
		return Reminder{}, err
	}
	if err = contracts.InsertWriteReceipt(ctx, principal.TenantID, principal.ActorID, CancelReminderRoute, key, digest, id, 200, response); err != nil {
		return Reminder{}, err
	}
	return item, nil
}

func VerifyReminderRevision(revision contract.FrozenContractRevision, seal *contract.ContractVersionSeal) error {
	digest := sha256.Sum256(revision.Snapshot)
	actual := hex.EncodeToString(digest[:])
	if seal == nil || actual != revision.SnapshotDigest || actual != seal.SnapshotDigest {
		return fault.Conflict("冻结计划来源完整性校验失败")
	}
	return nil
}
