// File metadata (manifest mirror):
// Purpose: Reads and writes transaction-bound fulfillment reminder schedules, delivery attempts and inbox facts.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-20
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

package postgres

import (
	"context"
	"errors"

	"contractaudit/internal/fault"
	app "contractaudit/internal/fulfillment/app"
	platform "contractaudit/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type ReminderRepository struct{ tx platform.Executor }

func NewReminderRepository(tx platform.Executor) *ReminderRepository {
	return &ReminderRepository{tx: tx}
}

const reminderColumns = `reminder_id::text,contract_id::text,contract_revision_id::text,plan_kind,plan_sequence,planned_date::text,timezone,recipient_id,lead_days,due_at,state,row_version,confirmed_by,confirmed_at`

type rowScanner interface{ Scan(...any) error }

func scanReminder(row rowScanner) (app.Reminder, error) {
	var r app.Reminder
	err := row.Scan(&r.ReminderID, &r.ContractID, &r.ContractRevisionID, &r.PlanKind, &r.PlanSequence, &r.PlannedDate, &r.Timezone, &r.RecipientID, &r.LeadDays, &r.DueAt, &r.Status, &r.Version, &r.ConfirmedBy, &r.ConfirmedAt)
	return r, err
}

func (r *ReminderRepository) Insert(ctx context.Context, p app.ReminderInsert) (app.Reminder, error) {
	out, err := scanReminder(r.tx.QueryRow(ctx, `INSERT INTO clm_fulfillment_reminders
        (tenant_id,contract_id,contract_revision_id,reminder_id,job_id,plan_kind,plan_sequence,planned_date,timezone,recipient_id,recipient_org_id,recipient_principal_id,recipient_membership_id,lead_days,due_at,confirmed_by)
        VALUES ($1,$2,$3,$4,$5,$6,$7,$8::date,$9,$10,$11,$12::uuid,$13::uuid,$14,$15,$16)
        RETURNING `+reminderColumns, p.TenantID, p.ContractID, p.RevisionID, p.ReminderID, p.JobID, p.Plan.Kind, p.Plan.Sequence, p.Plan.PlannedDate, p.Timezone, p.RecipientID, p.RecipientOrgID, p.RecipientPrincipalID, p.RecipientMembershipID, p.LeadDays, p.DueAt, p.ConfirmedBy))
	if err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) && pg.Code == "23505" {
			return out, fault.Conflict("此合同计划已有生效的提醒")
		}
		return out, err
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO clm_fulfillment_reminder_history(tenant_id,contract_id,reminder_id,version,action,actor_id) VALUES($1,$2,$3,1,'confirmed',$4)`, p.TenantID, p.ContractID, p.ReminderID, p.ConfirmedBy)
	return out, err
}
func (r *ReminderRepository) List(ctx context.Context, tenant, contractID string) ([]app.Reminder, error) {
	rows, err := r.tx.Query(ctx, `SELECT r.reminder_id::text,r.contract_id::text,r.contract_revision_id::text,r.plan_kind,r.plan_sequence,r.planned_date::text,r.timezone,r.recipient_id,r.lead_days,r.due_at,
 CASE WHEN r.state='cancelled' THEN 'cancelled' WHEN EXISTS (SELECT 1 FROM clm_fulfillment_reminder_deliveries d WHERE d.tenant_id=r.tenant_id AND d.contract_id=r.contract_id AND d.reminder_id=r.reminder_id) THEN 'delivered'
 ELSE COALESCE((SELECT a.status FROM clm_fulfillment_reminder_attempts a WHERE a.tenant_id=r.tenant_id AND a.contract_id=r.contract_id AND a.reminder_id=r.reminder_id ORDER BY a.generation DESC LIMIT 1),r.state) END,
 r.row_version,r.confirmed_by,r.confirmed_at FROM clm_fulfillment_reminders r WHERE r.tenant_id=$1 AND r.contract_id=$2 ORDER BY r.confirmed_at DESC,r.reminder_id`, tenant, contractID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []app.Reminder{}
	for rows.Next() {
		item, e := scanReminder(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
func (r *ReminderRepository) Cancel(ctx context.Context, tenant, contractID, id, actor string, expected int64) (app.Reminder, string, error) {
	var jobID string
	err := r.tx.QueryRow(ctx, `SELECT job_id FROM clm_fulfillment_reminders WHERE tenant_id=$1 AND contract_id=$2 AND reminder_id=$3 FOR UPDATE`, tenant, contractID, id).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, "", fault.NotFound("没有找到对应提醒")
	}
	if err != nil {
		return app.Reminder{}, "", err
	}
	item, err := scanReminder(r.tx.QueryRow(ctx, `UPDATE clm_fulfillment_reminders SET state='cancelled',row_version=row_version+1 WHERE tenant_id=$1 AND contract_id=$2 AND reminder_id=$3 AND state='active' AND row_version=$4 RETURNING `+reminderColumns, tenant, contractID, id, expected))
	if errors.Is(err, pgx.ErrNoRows) {
		return app.Reminder{}, "", fault.Conflict("提醒已变更或已取消")
	}
	if err != nil {
		return app.Reminder{}, "", err
	}
	_, err = r.tx.Exec(ctx, `INSERT INTO clm_fulfillment_reminder_history(tenant_id,contract_id,reminder_id,version,action,actor_id) VALUES($1,$2,$3,$4,'cancelled',$5)`, tenant, contractID, id, item.Version, actor)
	return item, jobID, err
}

type ReminderInboxRow struct {
	Delivery                  app.ReminderDelivery
	PrincipalID, MembershipID *string
}

func (r *ReminderRepository) Inbox(ctx context.Context, tenant, actor string) ([]ReminderInboxRow, error) {
	rows, err := r.tx.Query(ctx, `SELECT d.reminder_id::text,d.contract_id::text,r.contract_revision_id::text,r.plan_kind,r.plan_sequence,r.planned_date::text,r.timezone,d.delivered_at,r.recipient_principal_id::text,r.recipient_membership_id::text
 FROM clm_fulfillment_reminder_deliveries d JOIN clm_fulfillment_reminders r ON r.tenant_id=d.tenant_id AND r.contract_id=d.contract_id AND r.reminder_id=d.reminder_id
 WHERE d.tenant_id=$1 AND d.recipient_id=$2 ORDER BY d.delivered_at DESC,d.reminder_id LIMIT 500`, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ReminderInboxRow{}
	for rows.Next() {
		var item ReminderInboxRow
		v := &item.Delivery
		if err = rows.Scan(&v.ReminderID, &v.ContractID, &v.ContractRevisionID, &v.PlanKind, &v.PlanSequence, &v.PlannedDate, &v.Timezone, &v.DeliveredAt, &item.PrincipalID, &item.MembershipID); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type ReminderWork struct {
	Reminder                        app.Reminder
	TenantID, JobID, RecipientOrgID string
	PrincipalID, MembershipID       *string
}

func (r *ReminderRepository) LockWork(ctx context.Context, tenant, jobID string) (ReminderWork, error) {
	var work ReminderWork
	row := r.tx.QueryRow(ctx, `SELECT `+reminderColumns+`,tenant_id,job_id,recipient_org_id,recipient_principal_id::text,recipient_membership_id::text FROM clm_fulfillment_reminders WHERE tenant_id=$1 AND job_id=$2 FOR UPDATE`, tenant, jobID)
	item := &work.Reminder
	err := row.Scan(&item.ReminderID, &item.ContractID, &item.ContractRevisionID, &item.PlanKind, &item.PlanSequence, &item.PlannedDate, &item.Timezone, &item.RecipientID, &item.LeadDays, &item.DueAt, &item.Status, &item.Version, &item.ConfirmedBy, &item.ConfirmedAt, &work.TenantID, &work.JobID, &work.RecipientOrgID, &work.PrincipalID, &work.MembershipID)
	return work, err
}
func (r *ReminderRepository) RecordAttempt(ctx context.Context, tenant, contractID, reminderID string, generation int64, status, recipient string) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO clm_fulfillment_reminder_attempts(tenant_id,contract_id,reminder_id,generation,status) VALUES($1,$2,$3,$4,$5)`, tenant, contractID, reminderID, generation, status)
	if err != nil {
		return err
	}
	if status == "delivered" {
		_, err = r.tx.Exec(ctx, `INSERT INTO clm_fulfillment_reminder_deliveries(tenant_id,contract_id,reminder_id,recipient_id) VALUES($1,$2,$3,$4) ON CONFLICT (tenant_id,contract_id,reminder_id) DO NOTHING`, tenant, contractID, reminderID, recipient)
	}
	return err
}
func (r *ReminderRepository) WorkContract(ctx context.Context, tenant, jobID string) (string, error) {
	var id string
	err := r.tx.QueryRow(ctx, `SELECT contract_id::text FROM clm_fulfillment_reminders WHERE tenant_id=$1 AND job_id=$2`, tenant, jobID).Scan(&id)
	return id, err
}
func (r *ReminderRepository) IsDue(ctx context.Context, tenant, jobID string) (bool, error) {
	var due bool
	err := r.tx.QueryRow(ctx, `SELECT due_at<=clock_timestamp() FROM clm_fulfillment_reminders WHERE tenant_id=$1 AND job_id=$2`, tenant, jobID).Scan(&due)
	return due, err
}
