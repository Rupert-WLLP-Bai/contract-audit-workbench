package store

import (
	"context"
	"testing"
	"time"
)

func TestDistillationStatusFindsOnlyTheAuthorizedExactCandidate(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	ctx := context.Background()
	feedback, err := st.SubmitExperienceFeedback(ctx, user, input)
	if err != nil {
		t.Fatal(err)
	}
	delivery := createFeedbackDelivery(t, st, user, feedback,
		feedbackDistillInput(t, st, user, feedback, "candidate-for-status", ""), candidateContentForFeedback(t, st, user, feedback, time.Now()), time.Now().Add(time.Minute))
	status, err := st.GetExperienceDistillation(ctx, user, input.CaseID, feedback.FeedbackID, delivery.jobID)
	if err != nil || status.State != "running" || status.CandidateID != "" || status.VersionID != "" {
		t.Fatalf("unaccepted result exposed as a candidate: status=%+v err=%v", status, err)
	}
	candidate, err := acceptDelivery(st, user, feedback, delivery)
	if err != nil {
		t.Fatal(err)
	}
	status, err = st.GetExperienceDistillation(ctx, user, input.CaseID, feedback.FeedbackID, delivery.jobID)
	if err != nil || status.CandidateID != candidate.CandidateID || status.VersionID != candidate.VersionID {
		t.Fatalf("accepted exact candidate not discoverable: status=%+v candidate=%+v err=%v", status, candidate, err)
	}
	_, err = st.GetExperienceDistillation(ctx, user, input.CaseID, "other-feedback", delivery.jobID)
	requireFeedbackFault(t, err, 404)
	_, err = st.GetExperienceDistillation(ctx, user, "other-case", feedback.FeedbackID, delivery.jobID)
	requireFeedbackFault(t, err, 404)
}

func TestFeedbackResolvesFrozenDigestWithoutClientDiagnostics(t *testing.T) {
	st, user, input, _ := feedbackFixture(t, "false_positive")
	wantDigest := input.EvidenceRefs[0].NormalizedDigest
	input.EvidenceRefs[0].NormalizedDigest = ""
	first, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil || first.EvidenceRefs[0].NormalizedDigest != wantDigest {
		t.Fatalf("server did not resolve the frozen source: feedback=%+v err=%v", first, err)
	}
	if input.EvidenceRefs[0].NormalizedDigest != "" {
		t.Fatal("the caller's request was mutated")
	}
	replay, err := st.SubmitExperienceFeedback(context.Background(), user, input)
	if err != nil || replay.FeedbackID != first.FeedbackID {
		t.Fatalf("digest-free replay changed identity: feedback=%+v err=%v", replay, err)
	}
}
