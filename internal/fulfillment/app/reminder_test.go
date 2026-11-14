// File metadata (manifest mirror):
// Purpose: Checks frozen reminder plan selection, confirmed dates and bounded calendar calculations.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	contract "contractaudit/internal/contract/model"
	"encoding/json"
	"testing"
	"time"
)

func TestFrozenReminderPlanUsesUnambiguousConfirmedCalendarDate(t *testing.T) {
	draft := contract.DraftContent{Payments: []contract.DraftPaymentPlan{{Sequence: 1, PlannedDate: "2030-03-05"}, {Sequence: 2, PlannedDate: ""}, {Sequence: 3, PlannedDate: "2030-02-30"}}, DeliveryPlans: []contract.DraftDeliveryPlan{{Sequence: 4, PlannedDate: "2030-05-06"}, {Sequence: 4, PlannedDate: ""}}}
	raw, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := FrozenReminderPlans(contract.FrozenContractRevision{Snapshot: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Kind != "payment" || plans[0].Sequence != 1 {
		t.Fatalf("untrusted or ambiguous dates accepted: %+v", plans)
	}
	input := ReminderCreateInput{PlanKind: "payment", PlanSequence: 1, Timezone: "Asia/Shanghai", RecipientID: "actor", LeadDays: 2}
	_, due, err := ResolveReminderPlan(plans, input)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2030, 3, 3, 1, 0, 0, 0, time.UTC)
	if !due.Equal(want) {
		t.Fatalf("due=%s want=%s", due, want)
	}
	for _, invalid := range []ReminderCreateInput{{PlanKind: "payment", PlanSequence: 2, Timezone: "UTC", RecipientID: "actor"}, {PlanKind: "payment", PlanSequence: 1, Timezone: "Local", RecipientID: "actor"}, {PlanKind: "payment", PlanSequence: 1, Timezone: "UTC", RecipientID: "", LeadDays: 1}} {
		if _, _, err := ResolveReminderPlan(plans, invalid); err == nil {
			t.Fatalf("invalid reminder accepted: %+v", invalid)
		}
	}
}
