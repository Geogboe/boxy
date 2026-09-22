package pool

import (
	"context"
	"testing"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
)

func TestAdmissionNotificationFollowsReadyPersistence(t *testing.T) {
	st := newTestStoreWithAdmissionResource(t)
	ctx := context.Background()
	res, err := st.GetResource(ctx, "res-1")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h := &AdmissionHandler{Store: st, NotifyReady: func() {
		calls++
		ready, err := st.GetResource(ctx, res.ID)
		if err != nil || ready.State != model.ResourceStateReady {
			t.Fatal("notified before ready persisted")
		}
	}}
	if outcome, err := h.markReady(ctx, res, nil); err != nil || outcome != lifecycle.OutcomeAck || calls != 1 {
		t.Fatalf("outcome=%v error=%v calls=%d", outcome, err, calls)
	}
}
