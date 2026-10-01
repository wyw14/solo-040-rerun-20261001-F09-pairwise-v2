package schedule

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/admission"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

func newTestPlanner(t *testing.T) (*Planner, *admission.Queue) {
	t.Helper()
	now := time.Now
	queue := admission.NewQueue()
	controller := admission.NewController(queue, gate.NewState("chamber-1"), signal.NewStore(), now)
	recorder := journal.NewRecorder(journal.NewMemoryStore(), now)
	service := admission.NewService(controller, recorder, now)
	return NewPlanner(service, recorder, now), queue
}
func createPlan(t *testing.T, planner *Planner, priority int, eta *time.Time, name string) model.ConvoyPlan {
	t.Helper()
	vessel, err := model.NewVessel(name, "CS-"+name, 80)
	if err != nil {
		t.Fatalf("vessel: %v", err)
	}
	plan, err := planner.Create(context.Background(), "chamber-1", model.DirectionUpstream, []model.Vessel{vessel}, priority, eta)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return plan
}

func TestAdjustKeepsQueueAndPlanListConsistent(t *testing.T) {
	planner, queue := newTestPlanner(t)
	first := createPlan(t, planner, 0, nil, "alpha")
	second := createPlan(t, planner, 0, nil, "bravo")

	eta := time.Now().Add(-time.Hour).UTC()
	updated, err := planner.Adjust(context.Background(), second.ID, 7, &eta)
	if err != nil {
		t.Fatalf("adjust: %v", err)
	}
	if updated.Priority != 7 || updated.ETA == nil {
		t.Fatalf("adjust did not apply: %+v", updated)
	}

	active := planner.Active("chamber-1")
	if len(active) != 2 || active[0].ID != second.ID || active[1].ID != first.ID {
		t.Fatalf("plan list not re-sorted: %+v", active)
	}
	head, ok := queue.Peek("chamber-1")
	if !ok || head.PlanID != second.ID {
		t.Fatalf("queue head = %v, want adjusted plan %v", head.PlanID, second.ID)
	}
}

func TestAdjustRejectsStartedOrCancelledPlans(t *testing.T) {
	planner, queue := newTestPlanner(t)
	started := createPlan(t, planner, 0, nil, "started")
	other := createPlan(t, planner, 0, nil, "waiting")

	head, ok := queue.Peek("chamber-1")
	if !ok {
		t.Fatal("queue should not be empty")
	}
	if !queue.MarkReleased("chamber-1", head.Vessel.ID) {
		t.Fatal("mark released failed")
	}
	if _, err := planner.Adjust(context.Background(), started.ID, 5, nil); err == nil ||
		!strings.Contains(err.Error(), "already started") {
		t.Fatalf("started plan adjust should fail, got %v", err)
	}
	if _, err := planner.Adjust(context.Background(), other.ID, 5, nil); err != nil {
		t.Fatalf("unstarted plan adjust should succeed, got %v", err)
	}
	if _, err := planner.Adjust(context.Background(), other.ID, 99, nil); err == nil {
		t.Fatal("out-of-range priority should fail")
	}
	publisher := signal.NewPublisher(signal.NewStore(), journal.NewRecorder(journal.NewMemoryStore(), time.Now), time.Now)
	publication := signal.Publication{Previous: make(map[uuid.UUID]model.SignalState)}
	if _, err := planner.Cancel(context.Background(), other.ID, "test", publisher, publication); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := planner.Adjust(context.Background(), other.ID, 1, nil); err == nil ||
		!strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelled plan adjust should fail, got %v", err)
	}
}
