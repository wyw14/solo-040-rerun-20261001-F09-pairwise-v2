package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/admission"
	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

type schedulerHarness struct {
	planner    *Planner
	controller *admission.Controller
	queue      *admission.Queue
	clock      time.Time
	serial     int
}

func newSchedulerHarness(t *testing.T) *schedulerHarness {
	t.Helper()
	h := &schedulerHarness{clock: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	now := func() time.Time { return h.clock }
	store := journal.NewMemoryStore()
	recorder := journal.NewRecorder(store, now)
	queue := admission.NewQueue()
	gates := gate.NewState("c1")
	signals := signal.NewStore()
	controller := admission.NewController(queue, gates, signals, now)
	service := admission.NewService(controller, recorder, now)
	planner := NewPlanner(service, recorder, now)
	h.planner, h.controller, h.queue = planner, controller, queue
	return h
}

func (h *schedulerHarness) vessel(name string) model.Vessel {
	vessel, err := model.NewVessel(name, "CS-"+name, 55)
	if err != nil {
		panic(err)
	}
	return vessel
}

// create registers a plan one minute after the previous one so first-come
// ordering is deterministic and independent of UUID tie-breaks.
func (h *schedulerHarness) create(t *testing.T, priority model.PlanPriority, eta time.Time) model.ConvoyPlan {
	t.Helper()
	h.serial++
	vessel := h.vessel("V" + string(rune('A'-1+h.serial)))
	plan, err := h.planner.Create(context.Background(), "c1", model.DirectionUpstream,
		[]model.Vessel{vessel}, model.PlanPreferences{Priority: priority, ETA: eta})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	h.clock = h.clock.Add(time.Minute)
	return plan
}

func activeIDs(plans []model.ConvoyPlan) []string {
	ids := make([]string, len(plans))
	for index, plan := range plans {
		ids[index] = plan.ID.String()
	}
	return ids
}

// Adjusting a waiting plan must move it in the queue, the plan list and the
// plan picked by the next admission decision together.
func TestScheduleUpdateKeepsQueuePlansAndAdmissionAligned(t *testing.T) {
	h := newSchedulerHarness(t)
	ctx := context.Background()

	first := h.create(t, model.PriorityNormal, time.Time{})
	second := h.create(t, model.PriorityNormal, time.Time{})

	if got := activeIDs(h.planner.Active("c1")); len(got) != 2 || got[0] != first.ID.String() || got[1] != second.ID.String() {
		t.Fatalf("initial order mismatch: %v", got)
	}

	// Promote the later arrival; it should lead everywhere.
	promoted, err := h.planner.SetSchedule(ctx, second.ID, ScheduleUpdate{Priority: model.PriorityHigh})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.Priority != model.PriorityHigh {
		t.Fatalf("promoted plan priority = %d", promoted.Priority)
	}
	if got := activeIDs(h.planner.Active("c1")); got[0] != second.ID.String() || got[1] != first.ID.String() {
		t.Fatalf("plan list not reordered: %v", got)
	}
	order := h.queue.PlanOrder("c1")
	if len(order) != 2 || order[0] != second.ID || order[1] != first.ID {
		t.Fatalf("queue not reordered: %v", order)
	}

	// The next admission decision must pick the promoted plan's vessel.
	cycle, err := model.NewTransitCycle("c1", second.ID, model.DirectionUpstream, 1, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := h.controller.Evaluate(ctx, cycle)
	if err != nil || !decision.Allowed || decision.VesselID != second.Vessels[0].ID {
		t.Fatalf("admission decision: allowed=%v vessel=%s err=%v", decision.Allowed, decision.VesselID, err)
	}

	// A released plan is frozen: reorder is rejected, and jumping it in the
	// queue cannot change admission.
	if _, err := h.planner.SetSchedule(ctx, second.ID, ScheduleUpdate{Priority: model.PriorityLow}); err == nil {
		t.Fatal("released plan must not be reorderable")
	}
	if _, err := h.planner.SetSchedule(ctx, first.ID, ScheduleUpdate{Priority: model.PriorityHigh}); err != nil {
		t.Fatalf("promote waiting plan: %v", err)
	}
	head, ok := h.queue.Peek("c1")
	if !ok || head.PlanID != second.ID {
		t.Fatalf("released plan must remain at head, got %v ok=%v", head.PlanID, ok)
	}
	blockedCycle, err := model.NewTransitCycle("c1", first.ID, model.DirectionUpstream, 2, h.clock)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = h.controller.Evaluate(ctx, blockedCycle)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Allowed {
		t.Fatal("waiting plan must not overtake the released head")
	}
}

// ETA only orders plans that share a priority; known ETAs precede unknown.
func TestETABreaksSamePriorityTieInActiveList(t *testing.T) {
	h := newSchedulerHarness(t)
	base := h.clock
	later := h.create(t, model.PriorityNormal, base.Add(2*time.Hour))
	unknown := h.create(t, model.PriorityNormal, time.Time{})
	earlier := h.create(t, model.PriorityNormal, base.Add(time.Hour))

	got := activeIDs(h.planner.Active("c1"))
	want := []string{earlier.ID.String(), later.ID.String(), unknown.ID.String()}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestCreateHonoursPriorityPreferences(t *testing.T) {
	h := newSchedulerHarness(t)
	normal := h.create(t, model.PriorityNormal, time.Time{})
	high := h.create(t, model.PriorityHigh, time.Time{})
	got := activeIDs(h.planner.Active("c1"))
	if got[0] != high.ID.String() || got[1] != normal.ID.String() {
		t.Fatalf("high priority must lead on creation: %v", got)
	}
}

func TestCancelledPlanCannotBeRescheduled(t *testing.T) {
	h := newSchedulerHarness(t)
	plan := h.create(t, model.PriorityNormal, time.Time{})
	plan.Cancelled = true
	h.planner.replace(plan)
	if _, err := h.planner.SetSchedule(context.Background(), plan.ID, ScheduleUpdate{Priority: model.PriorityHigh}); err == nil {
		t.Fatal("cancelled plan rescheduling must fail")
	}
}
