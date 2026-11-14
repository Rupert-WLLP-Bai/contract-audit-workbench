package app

import (
	"context"
	"testing"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

type emptyContextReader struct {
	ReadRepository
	heads []model.ServiceObjectHead
}

func (r emptyContextReader) ListAllContextHeads(context.Context, ReadIdentity) ([]model.ServiceObjectHead, error) {
	return r.heads, nil
}
func TestFreezeContextNilAndBusinessTimeOrder(t *testing.T) {
	ctx := context.Background()
	repo := emptyContextReader{}
	value, hash, err := FreezeContext(ctx, repo, ReadIdentity{}, time.Time{}, nil, false)
	if err != nil || value != nil || hash != "" {
		t.Fatalf("legacy empty context: %+v %q %v", value, hash, err)
	}
	_, _, err = FreezeContext(ctx, repo, ReadIdentity{}, time.Time{}, nil, true)
	e, ok := err.(*fault.Error)
	if !ok || e.Status != 422 {
		t.Fatalf("governed context must require business time: %v", err)
	}
	value, hash, err = FreezeContext(ctx, repo, ReadIdentity{}, time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC), nil, true)
	if err != nil || value == nil || value.Complete || value.SHA256 != hash || len(value.Items) != 2 || value.Items[1].UnresolvedReason != "recognized_object_inventory_pending_review" || value.BusinessAt.Nanosecond() != 123456000 {
		t.Fatalf("governed pending context: %+v %q %v", value, hash, err)
	}
}
func TestContextHeadLimitPrecedesIdentityReads(t *testing.T) {
	repo := emptyContextReader{heads: make([]model.ServiceObjectHead, 101)}
	_, err := LoadContextSources(context.Background(), repo, ReadIdentity{})
	e, ok := err.(*fault.Error)
	if !ok || e.Status != 422 {
		t.Fatalf("expected object limit before identity access: %v", err)
	}
}
