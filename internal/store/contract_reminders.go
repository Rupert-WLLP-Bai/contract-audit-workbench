// File metadata (manifest mirror):
// Purpose: Connects authorized contract reminder creation, cancellation, inbox reads and fenced worker delivery to caller-owned transactions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"contractaudit/internal/access"
	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	contract "contractaudit/internal/contract/model"
	contractpg "contractaudit/internal/contract/postgres"
	"contractaudit/internal/fault"
	app "contractaudit/internal/fulfillment/app"
	fulfillmentpg "contractaudit/internal/fulfillment/postgres"
	platform "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

type ReminderCreateInput = app.ReminderCreateInput
type ReminderCancelInput = app.ReminderCancelInput
type ContractReminderList = app.ReminderList

type reminderAuthority struct{ storeFulfillmentAccess }

func (a reminderAuthority) ResolveReminderRecipient(ctx context.Context, record contract.Aggregate, actor string) (app.ReminderRecipient, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return app.ReminderRecipient{}, fault.Invalid("请选择提醒责任人")
	}
	tenant := record.TenantID
	directory := accounts.LookupUserSnapshot(actor)
	if directory.Status.State != "ready" && directory.Status.State != "local_demo" {
		return app.ReminderRecipient{}, errors.New("trusted directory unavailable")
	}
	if a.user.HasResolvedMembership() && directory.Status.Mode != "enterprise" || !a.user.HasResolvedMembership() && directory.Status.Mode != "local_demo" {
		return app.ReminderRecipient{}, errors.New("trusted directory mode unavailable")
	}
	var target accounts.User
	if a.user.HasResolvedMembership() {
		rows, err := a.tx.Query(ctx, `SELECT principal_id::text,membership_id::text,actor_id,issuer,subject,org_id,role_code,grants FROM tenant_reminder_member($1,$2)`, tenant, actor)
		if err != nil {
			return app.ReminderRecipient{}, err
		}
		defer rows.Close()
		count := 0
		for rows.Next() {
			var principal, membership, id, issuer, subject, org, role string
			var raw []byte
			if err = rows.Scan(&principal, &membership, &id, &issuer, &subject, &org, &role, &raw); err != nil {
				return app.ReminderRecipient{}, err
			}
			var grants []access.Grant
			if err = json.Unmarshal(raw, &grants); err != nil {
				return app.ReminderRecipient{}, fault.Forbidden("责任人当前授权不可用")
			}
			profile, ok := directory.User, directory.Found
			if !ok || !profile.Enabled || profile.Issuer != issuer || profile.Subject != subject {
				return app.ReminderRecipient{}, fault.Forbidden("责任人当前授权不可用")
			}
			target = accounts.MembershipUser(id, profile.DisplayName, role, tenant, org, issuer, subject, grants)
			target.PrincipalID = principal
			target.MembershipID = membership
			count++
		}
		if err = rows.Err(); err != nil {
			return app.ReminderRecipient{}, err
		}
		if count != 1 {
			return app.ReminderRecipient{}, fault.Forbidden("责任人当前成员身份不可用或不唯一")
		}
	} else {
		var ok bool
		target, ok = directory.User, directory.Found
		if !ok || !target.Enabled || target.TenantID != tenant || target.Issuer != "" || target.Subject != "" {
			return app.ReminderRecipient{}, fault.Forbidden("责任人当前授权不可用")
		}
	}
	if !authorizeContractRead(ctx, a.tx, record, target) {
		return app.ReminderRecipient{}, fault.Forbidden("责任人当前不能读取该合同")
	}
	result := app.ReminderRecipient{ActorID: target.ID, OrgID: target.OrgID}
	if target.HasResolvedMembership() {
		p, m := target.PrincipalID, target.MembershipID
		result.PrincipalID = &p
		result.MembershipID = &m
	}
	return result, nil
}

type reminderScheduler struct {
	store *Store
	tx    pgx.Tx
	user  accounts.User
}

func (s reminderScheduler) EnqueueReminder(ctx context.Context, tenant, contractID, reminderID, jobID string, _ time.Time) error {
	epoch, err := s.store.admissionEpoch(ctx, s.tx, tenant)
	if err != nil {
		return err
	}
	policy := s.store.executionQueuePolicy()
	_, err = s.tx.Exec(ctx, `SELECT fulfillment_reminder_enqueue($1,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`, tenant, contractID, reminderID, jobID, s.user.OrgID, s.user.ID, *epoch, policy.hash, policy.encoded, policy.GlobalPending, policy.TenantPending, policy.MaxClaims, policy.MaxAgeSeconds, s.store.sel.Mode != config.ModeProduction)
	return err
}
func (s reminderScheduler) CancelReminderJob(ctx context.Context, tenant, contractID, reminderID string) error {
	_, err := s.tx.Exec(ctx, `SELECT fulfillment_reminder_cancel_job($1,$2::uuid,$3::uuid)`, tenant, contractID, reminderID)
	return err
}

func (s *Store) CreateContractReminder(ctx context.Context, user accounts.User, contractID, key string, input ReminderCreateInput) (app.Reminder, error) {
	if s.serviceOwner != "core" {
		return app.Reminder{}, fault.New(503, "reminder_worker_unavailable", "提醒服务尚未启用，请联系管理员")
	}
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能设置履约提醒", nil)
	if err != nil {
		return app.Reminder{}, err
	}
	defer rollbackCLM(tx)
	services := newFulfillmentApplication(tx, current)
	executor := platform.Bind(tx)
	result, err := app.CreateReminder(ctx, services.principal, contractID, key, input, services.contracts, contractpg.NewVersionQueries(executor), fulfillmentpg.NewReminderRepository(executor), reminderAuthority{storeFulfillmentAccess{tx: tx, user: current}}, reminderScheduler{store: s, tx: tx, user: current}, services.events, services.outbox)
	if err != nil {
		return app.Reminder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return app.Reminder{}, err
	}
	return result, nil
}
func (s *Store) CancelContractReminder(ctx context.Context, user accounts.User, contractID, id, key string, input ReminderCancelInput) (app.Reminder, error) {
	ctx = commandContext(ctx, user)
	tx, current, err := s.beginCLMWrite(ctx, user, "只读模式下不能取消履约提醒", nil)
	if err != nil {
		return app.Reminder{}, err
	}
	defer rollbackCLM(tx)
	services := newFulfillmentApplication(tx, current)
	executor := platform.Bind(tx)
	result, err := app.CancelReminder(ctx, services.principal, contractID, id, key, input, services.contracts, fulfillmentpg.NewReminderRepository(executor), reminderAuthority{storeFulfillmentAccess{tx: tx, user: current}}, reminderScheduler{store: s, tx: tx, user: current}, services.events, services.outbox)
	if err != nil {
		return app.Reminder{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return app.Reminder{}, err
	}
	return result, nil
}
func (s *Store) ListContractReminders(ctx context.Context, user accounts.User, contractID string) (app.ReminderList, error) {
	if !s.HasDatabase() {
		return app.ReminderList{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	ctx = commandContext(ctx, user)
	id, err := contract.NormalizeContractUUID(contractID)
	if err != nil {
		return app.ReminderList{}, err
	}
	current, err := trustedUser(user)
	if err != nil {
		return app.ReminderList{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return app.ReminderList{}, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return app.ReminderList{}, err
	}
	record, err := loadCLMContract(ctx, tx, accounts.Tenant(current), id)
	if err != nil {
		return app.ReminderList{}, err
	}
	if !authorizeContractRead(ctx, tx, record, current) {
		return app.ReminderList{}, fault.NotFound("没有找到对应记录")
	}
	out := app.ReminderList{ContractRowVersion: record.RowVersion, Plans: []app.ReminderPlan{}, Reminders: []app.Reminder{}}
	if record.CurrentRevisionID != nil && record.AuthorityMode == "native" && record.ValidityState == "active" && (record.LifecycleState == "registered" || record.LifecycleState == "performing") {
		repo := contractpg.NewVersionQueries(platform.Bind(tx))
		rev, e := repo.LoadFrozenRevision(ctx, record.TenantID, id, *record.CurrentRevisionID)
		if e != nil {
			return out, e
		}
		seal, e := repo.LoadVersionSeal(ctx, record.TenantID, id, *record.CurrentRevisionID)
		if e != nil {
			return out, e
		}
		if e = app.VerifyReminderRevision(rev, seal); e != nil {
			return out, e
		}
		out.ContractRevisionID = rev.RevisionID
		out.Plans, e = app.FrozenReminderPlans(rev)
		if e != nil {
			return out, e
		}
	}
	out.Reminders, err = fulfillmentpg.NewReminderRepository(platform.Bind(tx)).List(ctx, record.TenantID, id)
	if err != nil {
		return out, err
	}
	for i := range out.Reminders {
		if out.Reminders[i].Status == "active" && (record.CurrentRevisionID == nil || *record.CurrentRevisionID != out.Reminders[i].ContractRevisionID || record.ValidityState != "active" || (record.LifecycleState != "registered" && record.LifecycleState != "performing")) {
			out.Reminders[i].Status = "stale"
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	return out, nil
}

func (s *Store) ListReminderInbox(ctx context.Context, user accounts.User) (app.ReminderInbox, error) {
	if !s.HasDatabase() {
		return app.ReminderInbox{}, fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	ctx = commandContext(ctx, user)
	current, err := trustedUser(user)
	if err != nil {
		return app.ReminderInbox{}, err
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return app.ReminderInbox{}, err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, userScope(current)); err != nil {
		return app.ReminderInbox{}, err
	}
	tenant := accounts.Tenant(current)
	rows, err := fulfillmentpg.NewReminderRepository(platform.Bind(tx)).Inbox(ctx, tenant, current.ID)
	if err != nil {
		return app.ReminderInbox{}, err
	}
	items := []app.ReminderDelivery{}
	for _, row := range rows {
		if current.HasResolvedMembership() {
			if row.PrincipalID == nil || row.MembershipID == nil || *row.PrincipalID != current.PrincipalID || *row.MembershipID != current.MembershipID {
				continue
			}
		} else if row.PrincipalID != nil || row.MembershipID != nil {
			continue
		}
		record, e := loadCLMContract(ctx, tx, tenant, row.Delivery.ContractID)
		if e != nil {
			var refusal *fault.Error
			if errors.As(e, &refusal) && refusal.Status == 404 {
				continue
			}
			return app.ReminderInbox{}, e
		}
		authority := reminderAuthority{storeFulfillmentAccess{tx: tx, user: current}}
		recipient, e := authority.ResolveReminderRecipient(ctx, record, current.ID)
		if e != nil {
			var refusal *fault.Error
			if errors.As(e, &refusal) && (refusal.Status == 403 || refusal.Status == 404) {
				continue
			}
			return app.ReminderInbox{}, e
		}
		if recipient.OrgID != current.OrgID || (current.HasResolvedMembership() && (recipient.PrincipalID == nil || recipient.MembershipID == nil || *recipient.PrincipalID != current.PrincipalID || *recipient.MembershipID != current.MembershipID)) {
			continue
		}
		items = append(items, row.Delivery)
	}
	if err = tx.Commit(ctx); err != nil {
		return app.ReminderInbox{}, err
	}
	return app.ReminderInbox{Items: items}, nil
}

type ReminderClaim struct {
	TenantID   string `json:"tenant_id"`
	OrgID      string `json:"org_id"`
	JobID      string `json:"job_id"`
	WorkerID   string `json:"worker_id"`
	Generation int64  `json:"generation"`
}

func (s *Store) DeliverReminderClaim(ctx context.Context, claim ReminderClaim) error {
	if !s.HasDatabase() {
		return fault.New(503, "database_unavailable", "服务数据库不可用")
	}
	if s.serviceOwner != "core" {
		return fault.Forbidden("当前服务不能投递提醒")
	}
	if claim.TenantID == "" || claim.OrgID == "" || claim.JobID == "" || claim.WorkerID == "" || claim.Generation <= 0 {
		return fault.Invalid("提醒任务身份无效")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer rollbackCLM(tx)
	if err = setLocalScope(ctx, tx, dbScope{TenantID: claim.TenantID, OrgID: claim.OrgID, ActorID: "reminder-worker", Role: "api"}); err != nil {
		return err
	}
	executor := platform.Bind(tx)
	repo := fulfillmentpg.NewReminderRepository(executor)
	contractID, err := repo.WorkContract(ctx, claim.TenantID, claim.JobID)
	if err != nil {
		return err
	}
	contracts := contractpg.NewFulfillmentContracts(executor)
	if err = contracts.LockContract(ctx, contractID); err != nil {
		return err
	}
	var claimState string
	err = tx.QueryRow(ctx, `SELECT fulfillment_reminder_lock_claim($1,$2,$3,$4,$5,$6)`, claim.TenantID, claim.OrgID, claim.JobID, claim.WorkerID, claim.Generation, s.sel.Mode != config.ModeProduction).Scan(&claimState)
	if err != nil {
		return err
	}
	if claimState != "ready" && claimState != "exhausted" {
		return ErrLeaseLost
	}
	var valid bool
	work, err := repo.LockWork(ctx, claim.TenantID, claim.JobID)
	if err != nil {
		return err
	}
	item := work.Reminder
	status := "stale"
	exhausted := claimState == "exhausted"
	if exhausted {
		status = "failed"
	}
	record, err := contracts.LoadContract(ctx, claim.TenantID, contractID)
	if err != nil {
		return err
	}
	if !exhausted && item.Status == "active" && record.AuthorityMode == "native" && record.ValidityState == "active" && (record.LifecycleState == "registered" || record.LifecycleState == "performing") && record.CurrentRevisionID != nil && *record.CurrentRevisionID == item.ContractRevisionID {
		due, e := repo.IsDue(ctx, claim.TenantID, claim.JobID)
		if e != nil {
			return e
		}
		if !due {
			return fault.Conflict("提醒尚未到期")
		}
		rev, e := contracts.LoadFulfillmentRevision(ctx, claim.TenantID, contractID, item.ContractRevisionID)
		if e != nil {
			return e
		}
		seal, e := contractpg.NewVersionQueries(executor).LoadVersionSeal(ctx, claim.TenantID, contractID, item.ContractRevisionID)
		if e != nil {
			return e
		}
		sourceInput := app.ReminderCreateInput{ContractRevisionID: item.ContractRevisionID, PlanKind: item.PlanKind, PlanSequence: item.PlanSequence, Timezone: item.Timezone, RecipientID: item.RecipientID, LeadDays: item.LeadDays, ExpectedContractRowVersion: record.RowVersion}
		plan, instant, e := app.ConfirmedReminderPlan(record, rev, seal, sourceInput)
		if e != nil || plan.PlannedDate != item.PlannedDate || !instant.Equal(item.DueAt) {
			status = "stale"
		} else {
			mode := accounts.User{}
			if work.PrincipalID != nil {
				mode = accounts.MembershipUser("mode", "", "viewer", claim.TenantID, claim.OrgID, "issuer", "subject", nil)
			}
			authority := reminderAuthority{storeFulfillmentAccess{tx: tx, user: mode}}
			recipient, e := authority.ResolveReminderRecipient(ctx, record, item.RecipientID)
			if e != nil {
				var refusal *fault.Error
				if !errors.As(e, &refusal) || (refusal.Status != 403 && refusal.Status != 404) {
					return e
				}
				status = "denied"
			} else if recipient.OrgID != work.RecipientOrgID || (work.PrincipalID == nil) != (recipient.PrincipalID == nil) || (work.MembershipID == nil) != (recipient.MembershipID == nil) || (work.PrincipalID != nil && (*work.PrincipalID != *recipient.PrincipalID || *work.MembershipID != *recipient.MembershipID)) {
				status = "denied"
			} else {
				status = "delivered"
			}
		}
	}
	if err = repo.RecordAttempt(ctx, claim.TenantID, contractID, item.ReminderID, claim.Generation, status, item.RecipientID); err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT fulfillment_reminder_finish_claim($1,$2,$3,$4,$5,$6)`, claim.TenantID, claim.OrgID, claim.JobID, claim.WorkerID, claim.Generation, s.sel.Mode != config.ModeProduction).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrLeaseLost
	}
	return tx.Commit(ctx)
}
