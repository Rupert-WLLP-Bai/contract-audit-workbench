package store

import (
	"context"
	"testing"
)

// Durable checks for the approval workbench read models (#85). They require
// TEST_DATABASE_URL and MIGRATOR_DATABASE_URL; without a database they are
// skipped and reported as NOT_RUN, never as passed.

func TestApprovalInboxAndRoundHistoryReadStoredFacts(t *testing.T) {
	fixture, _ := newApprovalDecisionEnv(t)
	ctx := context.Background()
	contractID, submitted := submitApprovalForDecisionTest(t, fixture, "read-submit-0001")
	reviewer := approvalTestUser(t, "demo-reviewer")

	todo, err := fixture.core.ListApprovalInbox(ctx, reviewer, ApprovalInboxQuery{View: "todo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(todo.Items) != 1 || todo.Items[0].TaskID == nil || *todo.Items[0].TaskID != submitted.Tasks[0].ApprovalTaskID {
		t.Fatalf("todo page %+v", todo)
	}
	if len(todo.Items[0].AllowedActions) != 2 || todo.Items[0].RoundNumber != 1 {
		t.Fatalf("todo item %+v", todo.Items[0])
	}

	decided, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: submitted.Tasks[0].ApprovalTaskID, Decision: clmDecisionApprove,
		Comment: "同意", ExpectedRoundRowVersion: submitted.Round.RowVersion,
		IdempotencyKey: "read-decision-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	done, err := fixture.core.ListApprovalInbox(ctx, reviewer, ApprovalInboxQuery{View: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if len(done.Items) != 1 || done.Items[0].DecisionAction == nil || *done.Items[0].DecisionAction != "approve" {
		t.Fatalf("done page %+v", done)
	}
	if done.Items[0].DecisionOpinion == nil || *done.Items[0].DecisionOpinion != "同意" {
		t.Fatalf("done opinion %+v", done.Items[0])
	}

	taskDetail, err := fixture.core.GetApprovalTask(ctx, fixture.admin, submitted.Tasks[0].ApprovalTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(taskDetail.RoundDetail.Tasks) != clmApprovalNodeCount ||
		len(taskDetail.RoundDetail.Decisions) != 1 ||
		taskDetail.RoundDetail.PreviousRound != nil ||
		taskDetail.RoundDetail.RevisionChanged {
		t.Fatalf("task detail %+v", taskDetail)
	}
	if taskDetail.RoundDetail.Decisions[0].Action != "approve" ||
		taskDetail.RoundDetail.Decisions[0].Opinion != "同意" {
		t.Fatalf("decision projection %+v", taskDetail.RoundDetail.Decisions[0])
	}

	// A return starts a new round on the same frozen revision. The read model
	// must expose lineage and must not invent a revision change.
	returned, err := fixture.core.DecideApprovalTask(ctx, reviewer, DecideApprovalTaskInput{
		TaskID: decided.NextTask.ApprovalTaskID, Decision: clmDecisionReturn,
		Comment: "请补充分包说明", ExpectedRoundRowVersion: decided.Round.RowVersion,
		IdempotencyKey: "read-return-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	resubmitted, err := fixture.core.SubmitContractApproval(ctx, fixture.admin, SubmitContractApprovalInput{
		ContractID: contractID, ExpectedRowVersion: returned.RowVersion, IdempotencyKey: "read-resubmit-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resubmitted.Round.RoundNumber != returned.Round.RoundNumber+1 {
		t.Fatalf("resubmitted round %d after %d", resubmitted.Round.RoundNumber, returned.Round.RoundNumber)
	}
	roundDetail, err := fixture.core.GetContractApprovalRound(ctx, fixture.admin, contractID, resubmitted.Round.ApprovalRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if roundDetail.PreviousRound == nil || roundDetail.PreviousRound.ApprovalRoundID != returned.Round.ApprovalRoundID {
		t.Fatalf("previous round %+v", roundDetail.PreviousRound)
	}
	if roundDetail.RevisionChanged || roundDetail.RevisionDiff != nil {
		t.Fatalf("same revision reported changed: %+v", roundDetail.RevisionDiff)
	}
	if len(roundDetail.Tasks) != clmApprovalNodeCount || roundDetail.ActiveTaskID == nil ||
		*roundDetail.ActiveTaskID != resubmitted.Tasks[0].ApprovalTaskID {
		t.Fatalf("new round must restart at the first node: %+v", roundDetail)
	}

	history, err := fixture.core.ListContractApprovalRounds(ctx, fixture.admin, contractID, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 2 || history.Items[0].Round.RoundNumber != 2 || history.Items[1].Round.RoundNumber != 1 {
		t.Fatalf("history %+v", history)
	}
	if history.Items[0].Round.PreviousRoundID == nil ||
		*history.Items[0].Round.PreviousRoundID != returned.Round.ApprovalRoundID ||
		history.Items[1].DecisionCount != 2 {
		t.Fatalf("history lineage/counts %+v", history.Items)
	}
}
