package admission

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

func testPlan(t *testing.T, chamberID string, priority int, eta *time.Time, created time.Time, vesselName string) model.ConvoyPlan {
	t.Helper()
	vessel, err := model.NewVessel(vesselName, "CS-"+vesselName, 80)
	if err != nil {
		t.Fatalf("vessel: %v", err)
	}
	plan, err := model.NewConvoyPlan(chamberID, model.DirectionUpstream, []model.Vessel{vessel}, priority, eta, created)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return plan
}

func waitingOrder(entries []QueueEntry) []string {
	var names []string
	for _, entry := range entries {
		if !entry.Released {
			names = append(names, entry.Vessel.Name)
		}
	}
	return names
}

func TestQueueOrdersByPriorityThenArrivalThenFCFS(t *testing.T) {
	queue := NewQueue()
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	normal := testPlan(t, "chamber-1", 0, nil, base, "normal-first")
	normalLater := testPlan(t, "chamber-1", 0, nil, base.Add(time.Minute), "normal-second")
	urgent := testPlan(t, "chamber-1", 5, nil, base.Add(2*time.Minute), "urgent")
	for _, plan := range []model.ConvoyPlan{normal, normalLater, urgent} {
		if err := queue.Add(plan.ChamberID, plan); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	got := waitingOrder(queue.List("chamber-1"))
	want := []string{"urgent", "normal-first", "normal-second"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestQueueETAOverridesArrivalBasisWithinPriority(t *testing.T) {
	queue := NewQueue()
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	early := base.Add(30 * time.Minute)
	// Created first but arrives later; created second but arrives sooner.
	slow := testPlan(t, "chamber-1", 0, &early, base, "late-arrival")
	quick := testPlan(t, "chamber-1", 0, nil, base.Add(time.Minute), "early-arrival")
	for _, plan := range []model.ConvoyPlan{slow, quick} {
		if err := queue.Add(plan.ChamberID, plan); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	got := waitingOrder(queue.List("chamber-1"))
	if got[0] != "early-arrival" || got[1] != "late-arrival" {
		t.Fatalf("ETA should win within same priority, got %v", got)
	}
}

func TestQueueReleasedEntriesAreNeverReordered(t *testing.T) {
	queue := NewQueue()
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	first := testPlan(t, "chamber-1", 0, nil, base, "released-head")
	second := testPlan(t, "chamber-1", 0, nil, base.Add(time.Minute), "waiting")
	for _, plan := range []model.ConvoyPlan{first, second} {
		if err := queue.Add(plan.ChamberID, plan); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	head, ok := queue.Peek("chamber-1")
	if !ok || head.Vessel.Name != "released-head" {
		t.Fatalf("peek = %v, want released-head", head.Vessel.Name)
	}
	if !queue.MarkReleased("chamber-1", head.Vessel.ID) {
		t.Fatal("mark released failed")
	}
	// A high-priority plan arrives after the release.
	urgent := testPlan(t, "chamber-1", 9, nil, base.Add(2*time.Minute), "urgent")
	if err := queue.Add(urgent.ChamberID, urgent); err != nil {
		t.Fatalf("add: %v", err)
	}
	entries := queue.List("chamber-1")
	if !entries[0].Released || entries[0].Vessel.Name != "released-head" {
		t.Fatalf("released entry moved: %+v", entries[0])
	}
	got := waitingOrder(entries)
	if got[0] != "urgent" || got[1] != "waiting" {
		t.Fatalf("waiting tail not sorted: %v", got)
	}
	// Peek skips the released entry and follows the new order.
	next, ok := queue.Peek("chamber-1")
	if !ok || next.Vessel.Name != "urgent" {
		t.Fatalf("peek after release = %v, want urgent", next.Vessel.Name)
	}
	// Reorder of the started plan must not touch the released entry.
	first.Priority = 9
	queue.Reorder("chamber-1", first)
	entries = queue.List("chamber-1")
	if !entries[0].Released || entries[0].Vessel.Name != "released-head" {
		t.Fatalf("released entry reordered: %+v", entries[0])
	}
	if !queue.PlanStarted("chamber-1", first.ID) {
		t.Fatal("PlanStarted should report the released plan")
	}
	if queue.PlanStarted("chamber-1", second.ID) {
		t.Fatal("PlanStarted should be false for a waiting plan")
	}
}

func TestQueueReorderAppliesNewOrderKey(t *testing.T) {
	queue := NewQueue()
	base := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	a := testPlan(t, "chamber-1", 0, nil, base, "a")
	b := testPlan(t, "chamber-1", 0, nil, base.Add(time.Minute), "b")
	for _, plan := range []model.ConvoyPlan{a, b} {
		if err := queue.Add(plan.ChamberID, plan); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	b.Priority = 3
	queue.Reorder("chamber-1", b)
	got := waitingOrder(queue.List("chamber-1"))
	if got[0] != "b" || got[1] != "a" {
		t.Fatalf("reorder failed: %v", got)
	}
	if _, ok := queue.Peek("chamber-2"); ok {
		t.Fatal("unknown chamber should be empty")
	}
	if _, ok := queue.Peek(uuid.Nil.String()); ok {
		t.Fatal("nil chamber should be empty")
	}
}
