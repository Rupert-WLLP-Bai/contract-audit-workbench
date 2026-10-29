package store

import (
	"context"
	"errors"
	"testing"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
)

func TestBusinessMutationPublicEntryErrorPriority(t *testing.T) {
	st, err := OpenTest(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	unknown := accounts.User{ID: "missing-business-user"}
	st.UseReadOnly(true)

	assertStatus := func(name string, call func() error, want int) {
		t.Helper()
		err := call()
		var apiErr *fault.Error
		if !errors.As(err, &apiErr) || apiErr.Status != want {
			t.Fatalf("%s returned %v, want status %d", name, err, want)
		}
	}
	assertStatus("create remediation", func() error {
		_, err := st.CreateRemediation(unknown, "", RemediationInput{})
		return err
	}, 503)
	assertStatus("submit remediation", func() error {
		_, err := st.SubmitRemediation(unknown, "", "", RemediationSubmitInput{})
		return err
	}, 503)
	assertStatus("verify remediation", func() error {
		_, err := st.VerifyRemediation(unknown, "", "", RemediationVerifyInput{})
		return err
	}, 503)
	assertStatus("create report", func() error {
		_, err := st.CreateReport(unknown, "", "", "")
		return err
	}, 503)
	assertStatus("archive case", func() error {
		_, err := st.Archive(unknown, "", "", "")
		return err
	}, 503)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.CreateReportContext(canceled, unknown, "", "", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled report request returned %v, want context cancellation before read-only check", err)
	}

	st.UseReadOnly(false)
	if _, err := st.CreateRemediation(unknown, "", RemediationInput{}); !isUnauthenticated(err) {
		t.Fatalf("CreateRemediation validation preceded identity check: %v", err)
	}
	if _, _, err := st.ReadReport(unknown, "", "", "invalid"); !isUnauthenticated(err) {
		t.Fatalf("ReadReport format validation preceded identity check: %v", err)
	}
}

func isUnauthenticated(err error) bool {
	var apiErr *fault.Error
	return errors.As(err, &apiErr) && apiErr.Status == 401
}
