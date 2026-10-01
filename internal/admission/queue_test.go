package admission

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func testVessel(name string) model.Vessel {
	vessel, err := model.NewVessel(name, "CS-"+name, 42)
	if err != nil {
		panic(err)
	}
	return vessel
}

func makePlan(chamber string, vessels []model.Vessel, priority model.PlanPriority, eta time.Time, arrived time.Time) model.ConvoyPlan {
	return model.ConvoyPlan{
		ID: uuid.New(), ChamberID: chamber, Direction: model.DirectionUpstream,
		Vessels: vessels, Priority: priority, ETA: eta, CreatedAt: arrived,
	}
}

func planIDs(entries []QueueEntry) []string {
	seen := map[uuid.UUID]bool{}
	var ids []string
	for _, entry := range entries {
		if seen[entry.PlanID] {
			continue
		}
		seen[entry.PlanID] = true
		ids = append(ids, entry.PlanID.String())
	}
	return ids
}

func TestFCFSWithinSamePriority(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	a := makePlan("c1", []model.Vessel{testVessel("A")}, model.PriorityNormal, time.Time{}, base)
	b := makePlan("c1", []model.Vessel{testVessel("B")}, model.PriorityNormal, time.Time{}, base.Add(time.Minute))
	if err := q.Add("c1", a); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", b); err != nil {
		t.Fatal(err)
	}
	entries := q.List("c1")
	if got := planIDs(entries); len(got) != 2 || got[0] != a.ID.String() || got[1] != b.ID.String() {
		t.Fatalf("expected first-come order, got %v", got)
	}
}

func TestHigherPriorityJumpsWaitingQueue(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	normal := makePlan("c1", []model.Vessel{testVessel("N")}, model.PriorityNormal, time.Time{}, base)
	high := makePlan("c1", []model.Vessel{testVessel("H")}, model.PriorityHigh, time.Time{}, base.Add(time.Hour))
	low := makePlan("c1", []model.Vessel{testVessel("L")}, model.PriorityLow, time.Time{}, base.Add(2*time.Hour))
	for _, plan := range []model.ConvoyPlan{normal, high, low} {
		if err := q.Add("c1", plan); err != nil {
			t.Fatal(err)
		}
	}
	entries := q.List("c1")
	want := []uuid.UUID{high.ID, normal.ID, low.ID}
	for index, id := range want {
		if entries[index].PlanID != id {
			t.Fatalf("position %d: want %s got %s", index, id, entries[index].PlanID)
		}
	}
}

func TestETATiesWithinPriority(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	earlier := makePlan("c1", []model.Vessel{testVessel("E")}, model.PriorityNormal, base.Add(time.Hour), base)
	later := makePlan("c1", []model.Vessel{testVessel("L")}, model.PriorityNormal, base.Add(2*time.Hour), base)
	unknown := makePlan("c1", []model.Vessel{testVessel("U")}, model.PriorityNormal, time.Time{}, base)
	for _, plan := range []model.ConvoyPlan{later, unknown, earlier} {
		if err := q.Add("c1", plan); err != nil {
			t.Fatal(err)
		}
	}
	got := planIDs(q.List("c1"))
	want := []string{earlier.ID.String(), later.ID.String(), unknown.ID.String()}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("position %d: want %s got %v", index, want, got)
		}
	}
}

func TestNewEntryCannotCrossReleasedVessel(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	first := makePlan("c1", []model.Vessel{testVessel("F")}, model.PriorityNormal, time.Time{}, base)
	second := makePlan("c1", []model.Vessel{testVessel("S")}, model.PriorityNormal, time.Time{}, base.Add(time.Minute))
	if err := q.Add("c1", first); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", second); err != nil {
		t.Fatal(err)
	}
	if !q.MarkReleased("c1", first.Vessels[0].ID) {
		t.Fatal("mark released failed")
	}
	urgent := makePlan("c1", []model.Vessel{testVessel("U")}, model.PriorityHigh, time.Time{}, base.Add(2*time.Minute))
	if err := q.Add("c1", urgent); err != nil {
		t.Fatal(err)
	}
	entries := q.List("c1")
	if entries[0].PlanID != first.ID || !entries[0].Released {
		t.Fatalf("released vessel must stay at head, got %+v", entries[0])
	}
	if entries[1].PlanID != urgent.ID {
		t.Fatalf("high priority vessel must be next, got %s", entries[1].PlanID)
	}
	if peek, ok := q.Peek("c1"); !ok || peek.PlanID != first.ID {
		t.Fatal("peek must still see the released head")
	}
}

func TestConvoyVesselsStayContiguous(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	convoy := makePlan("c1", []model.Vessel{testVessel("C1"), testVessel("C2")}, model.PriorityHigh, time.Time{}, base)
	other := makePlan("c1", []model.Vessel{testVessel("O")}, model.PriorityNormal, time.Time{}, base)
	if err := q.Add("c1", other); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", convoy); err != nil {
		t.Fatal(err)
	}
	entries := q.List("c1")
	if entries[0].PlanID != convoy.ID || entries[1].PlanID != convoy.ID || entries[2].PlanID != other.ID {
		t.Fatalf("convoy vessels must stay contiguous at the head: %v", planIDs(entries))
	}
}

func TestSetPlanScheduleReordersWaitingEntries(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	a := makePlan("c1", []model.Vessel{testVessel("A")}, model.PriorityNormal, time.Time{}, base)
	b := makePlan("c1", []model.Vessel{testVessel("B")}, model.PriorityNormal, time.Time{}, base.Add(time.Minute))
	if err := q.Add("c1", a); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", b); err != nil {
		t.Fatal(err)
	}
	ok, err := q.SetPlanSchedule("c1", b.ID, model.PlanPreferences{Priority: model.PriorityHigh})
	if err != nil || !ok {
		t.Fatalf("schedule update: ok=%v err=%v", ok, err)
	}
	entries := q.List("c1")
	if entries[0].PlanID != b.ID || entries[0].Priority != model.PriorityHigh {
		t.Fatalf("promoted plan must lead, got %s prio=%d", entries[0].PlanID, entries[0].Priority)
	}

	// With both plans back at normal priority, an earlier ETA must beat the
	// earlier first-come time.
	if _, err := q.SetPlanSchedule("c1", b.ID, model.PlanPreferences{Priority: model.PriorityNormal, ETA: base.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.SetPlanSchedule("c1", a.ID, model.PlanPreferences{Priority: model.PriorityNormal, ETA: base.Add(5 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	entries = q.List("c1")
	if entries[0].PlanID != b.ID || !entries[0].ETA.Equal(base.Add(-time.Hour)) {
		t.Fatalf("earlier ETA must lead the same-priority group, got %s eta=%v", entries[0].PlanID, entries[0].ETA)
	}
}

func TestSetPlanScheduleRejectsReleasedAndUnknownPlans(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	released := makePlan("c1", []model.Vessel{testVessel("R")}, model.PriorityNormal, time.Time{}, base)
	waiting := makePlan("c1", []model.Vessel{testVessel("W")}, model.PriorityNormal, time.Time{}, base.Add(time.Minute))
	if err := q.Add("c1", released); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", waiting); err != nil {
		t.Fatal(err)
	}
	q.MarkReleased("c1", released.Vessels[0].ID)
	if _, err := q.SetPlanSchedule("c1", released.ID, model.PlanPreferences{Priority: model.PriorityHigh}); err == nil {
		t.Fatal("released plan must not be reorderable")
	}
	// Waiting entries must be untouched after the rejected update.
	entries := q.List("c1")
	if entries[0].PlanID != released.ID || entries[1].PlanID != waiting.ID {
		t.Fatalf("queue changed after rejected update: %v", planIDs(entries))
	}
	if ok, err := q.SetPlanSchedule("c1", uuid.New(), model.PlanPreferences{Priority: model.PriorityHigh}); err != nil || ok {
		t.Fatalf("unknown plan: ok=%v err=%v", ok, err)
	}
	if _, err := q.SetPlanSchedule("c1", waiting.ID, model.PlanPreferences{Priority: model.PlanPriority(99)}); err == nil {
		t.Fatal("invalid priority must be rejected")
	}
}

func TestRemoveAndRestoreKeepsRankPositions(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	high := makePlan("c1", []model.Vessel{testVessel("H")}, model.PriorityHigh, time.Time{}, base)
	normal := makePlan("c1", []model.Vessel{testVessel("N")}, model.PriorityNormal, time.Time{}, base)
	if err := q.Add("c1", high); err != nil {
		t.Fatal(err)
	}
	if err := q.Add("c1", normal); err != nil {
		t.Fatal(err)
	}
	removed := q.RemovePlan("c1", high.ID)
	if len(removed) != 1 {
		t.Fatalf("removed %d entries", len(removed))
	}
	q.Restore("c1", removed)
	entries := q.List("c1")
	if entries[0].PlanID != high.ID || entries[0].Released {
		t.Fatalf("restored plan must resume its high-priority slot unreleased, got %+v", entries[0])
	}
}

func TestPlanOrderDeduplicatesConvoyVessels(t *testing.T) {
	q := NewQueue()
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	convoy := makePlan("c1", []model.Vessel{testVessel("A"), testVessel("B")}, model.PriorityNormal, time.Time{}, base)
	if err := q.Add("c1", convoy); err != nil {
		t.Fatal(err)
	}
	order := q.PlanOrder("c1")
	if len(order) != 1 || order[0] != convoy.ID {
		t.Fatalf("expected one deduplicated plan id, got %v", order)
	}
}
