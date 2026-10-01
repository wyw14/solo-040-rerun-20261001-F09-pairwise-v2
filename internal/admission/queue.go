package admission

import (
	"errors"
	"sort"
	"sync"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

type QueueEntry struct {
	Vessel   model.Vessel        `json:"vessel"`
	PlanID   uuid.UUID           `json:"plan_id"`
	Released bool                `json:"released"`
	Order    model.DispatchOrder `json:"order"`
}

// Queue keeps released vessels pinned in place while the unreleased tail
// stays sorted by dispatch order (priority, expected arrival, then FCFS).
type Queue struct {
	mu      sync.RWMutex
	entries map[string][]QueueEntry
}

func NewQueue() *Queue { return &Queue{entries: make(map[string][]QueueEntry)} }

func (q *Queue) Add(chamberID string, plan model.ConvoyPlan) error {
	if chamberID == "" || plan.ID == uuid.Nil || len(plan.Vessels) == 0 {
		return errors.New("queue addition requires chamber, plan and vessels")
	}
	q.mu.Lock()
	order := plan.DispatchOrder()
	for _, vessel := range plan.Vessels {
		q.entries[chamberID] = append(q.entries[chamberID], QueueEntry{Vessel: vessel, PlanID: plan.ID, Order: order})
	}
	q.sortUnreleased(chamberID)
	q.mu.Unlock()
	return nil
}

// Peek returns the next vessel awaiting release; released vessels keep their
// recorded positions but no longer participate in admission decisions.
func (q *Queue) Peek(chamberID string) (QueueEntry, bool) {
	q.mu.RLock()
	entries := q.entries[chamberID]
	q.mu.RUnlock()
	for _, entry := range entries {
		if !entry.Released {
			return entry, true
		}
	}
	return QueueEntry{}, false
}

func (q *Queue) MarkReleased(chamberID string, vesselID uuid.UUID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for index := range q.entries[chamberID] {
		if q.entries[chamberID][index].Vessel.ID == vesselID {
			q.entries[chamberID][index].Released = true
			return true
		}
	}
	return false
}

// Reorder applies a plan's new dispatch order to its unreleased entries and
// re-sorts the waiting tail. Released entries are never moved.
func (q *Queue) Reorder(chamberID string, plan model.ConvoyPlan) {
	q.mu.Lock()
	order := plan.DispatchOrder()
	for index := range q.entries[chamberID] {
		entry := &q.entries[chamberID][index]
		if entry.PlanID == plan.ID && !entry.Released {
			entry.Order = order
		}
	}
	q.sortUnreleased(chamberID)
	q.mu.Unlock()
}

// PlanStarted reports whether any vessel of the plan has been released,
// which makes the plan ineligible for reordering.
func (q *Queue) PlanStarted(chamberID string, planID uuid.UUID) bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	for _, entry := range q.entries[chamberID] {
		if entry.PlanID == planID && entry.Released {
			return true
		}
	}
	return false
}

func (q *Queue) RemovePlan(chamberID string, planID uuid.UUID) []QueueEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := q.entries[chamberID]
	kept := entries[:0]
	var removed []QueueEntry
	for _, entry := range entries {
		if entry.PlanID == planID {
			removed = append(removed, entry)
		} else {
			kept = append(kept, entry)
		}
	}
	q.entries[chamberID] = append([]QueueEntry(nil), kept...)
	return removed
}

func (q *Queue) Restore(chamberID string, entries []QueueEntry) {
	q.mu.Lock()
	current := q.entries[chamberID]
	restored := make([]QueueEntry, 0, len(entries)+len(current))
	for _, entry := range entries {
		entry.Released = false
		restored = append(restored, entry)
	}
	q.entries[chamberID] = append(restored, current...)
	q.sortUnreleased(chamberID)
	q.mu.Unlock()
}

func (q *Queue) List(chamberID string) []QueueEntry {
	q.mu.RLock()
	result := append([]QueueEntry(nil), q.entries[chamberID]...)
	q.mu.RUnlock()
	return result
}

// sortUnreleased stable-sorts only the unreleased entries back into the
// unreleased slots, leaving every released entry at its exact position.
func (q *Queue) sortUnreleased(chamberID string) {
	entries := q.entries[chamberID]
	waiting := make([]QueueEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.Released {
			waiting = append(waiting, entry)
		}
	}
	sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].Order.Less(waiting[j].Order) })
	next := 0
	for index := range entries {
		if !entries[index].Released {
			entries[index] = waiting[next]
			next++
		}
	}
	q.entries[chamberID] = entries
}
