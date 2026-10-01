package admission

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
)

type QueueEntry struct {
	Vessel    model.Vessel       `json:"vessel"`
	PlanID    uuid.UUID          `json:"plan_id"`
	Priority  model.PlanPriority `json:"priority"`
	ETA       time.Time          `json:"eta,omitempty"`
	ArrivedAt time.Time          `json:"arrived_at"`
	Released  bool               `json:"released"`
}

func rankOf(entry QueueEntry) model.DispatchRank {
	return model.DispatchRank{
		Priority: entry.Priority, ETA: entry.ETA,
		ArrivedAt: entry.ArrivedAt, PlanID: entry.PlanID,
	}
}

// Queue preserves a two-region order per chamber: released vessels form an
// immutable prefix (a vessel that already passed the signal cannot be
// reordered), while waiting vessels are ordered by dispatch rank
// (priority, then ETA, then first-come time).
type Queue struct {
	mu      sync.RWMutex
	entries map[string][]QueueEntry
}

func NewQueue() *Queue { return &Queue{entries: make(map[string][]QueueEntry)} }

// insertionIndex returns the waiting-region position for a new rank. Released
// entries are never crossed, so the scan starts after the released prefix.
func insertionIndex(entries []QueueEntry, rank model.DispatchRank) int {
	for index, entry := range entries {
		if entry.Released {
			continue
		}
		if model.RankBefore(rank, rankOf(entry)) {
			return index
		}
	}
	return len(entries)
}

func newEntries(plan model.ConvoyPlan) []QueueEntry {
	rank := model.DispatchRank{
		Priority: plan.Priority, ETA: plan.ETA,
		ArrivedAt: plan.CreatedAt, PlanID: plan.ID,
	}
	entries := make([]QueueEntry, 0, len(plan.Vessels))
	for _, vessel := range plan.Vessels {
		entries = append(entries, QueueEntry{
			Vessel: vessel, PlanID: plan.ID,
			Priority: rank.Priority, ETA: rank.ETA, ArrivedAt: rank.ArrivedAt,
		})
	}
	return entries
}

func (q *Queue) Add(chamberID string, plan model.ConvoyPlan) error {
	if chamberID == "" || plan.ID == uuid.Nil || len(plan.Vessels) == 0 {
		return errors.New("queue addition requires chamber, plan and vessels")
	}
	incoming := newEntries(plan)
	q.mu.Lock()
	defer q.mu.Unlock()
	existing := q.entries[chamberID]
	index := insertionIndex(existing, rankOf(incoming[0]))
	updated := make([]QueueEntry, 0, len(existing)+len(incoming))
	updated = append(updated, existing[:index]...)
	updated = append(updated, incoming...)
	updated = append(updated, existing[index:]...)
	q.entries[chamberID] = updated
	return nil
}

func (q *Queue) Peek(chamberID string) (QueueEntry, bool) {
	q.mu.RLock()
	entries := q.entries[chamberID]
	q.mu.RUnlock()
	if len(entries) == 0 {
		return QueueEntry{}, false
	}
	return entries[0], true
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

// Restore reinserts entries (typically cancellation compensation) back into
// their rank-ordered waiting positions without crossing released vessels.
func (q *Queue) Restore(chamberID string, entries []QueueEntry) {
	q.mu.Lock()
	defer q.mu.Unlock()
	current := q.entries[chamberID]
	for _, entry := range entries {
		entry.Released = false
		index := insertionIndex(current, rankOf(entry))
		current = append(current[:index], append([]QueueEntry{entry}, current[index:]...)...)
	}
	q.entries[chamberID] = current
}

// SetPlanSchedule updates priority and ETA for a plan that has not started
// moving. It reports false when the plan is absent; released plans are rejected
// so vessels that already passed the signal keep their position.
func (q *Queue) SetPlanSchedule(chamberID string, planID uuid.UUID, preferences model.PlanPreferences) (bool, error) {
	preferences = model.NormalizePlanPreferences(preferences)
	if !preferences.Priority.Valid() {
		return false, errors.New("plan priority is out of range")
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := q.entries[chamberID]
	present := false
	for index := range entries {
		if entries[index].PlanID != planID {
			continue
		}
		if entries[index].Released {
			return false, errors.New("released plan cannot be reordered")
		}
		entries[index].Priority = preferences.Priority
		entries[index].ETA = preferences.ETA
		present = true
	}
	if !present {
		return false, nil
	}
	// Only the waiting suffix is reordered; the released prefix is untouched
	// and stable sort preserves a plan's internal vessel order.
	prefix := 0
	for prefix < len(entries) && entries[prefix].Released {
		prefix++
	}
	sort.SliceStable(entries[prefix:], func(i, j int) bool {
		return model.RankBefore(rankOf(entries[prefix+i]), rankOf(entries[prefix+j]))
	})
	q.entries[chamberID] = entries
	return true, nil
}

func (q *Queue) List(chamberID string) []QueueEntry {
	q.mu.RLock()
	result := append([]QueueEntry(nil), q.entries[chamberID]...)
	q.mu.RUnlock()
	return result
}

// PlanOrder returns plan IDs in the exact order they appear in the chamber
// queue, deduplicated (a convoy's vessels occupy consecutive positions).
func (q *Queue) PlanOrder(chamberID string) []uuid.UUID {
	q.mu.RLock()
	defer q.mu.RUnlock()
	seen := make(map[uuid.UUID]struct{})
	var order []uuid.UUID
	for _, entry := range q.entries[chamberID] {
		if _, ok := seen[entry.PlanID]; ok {
			continue
		}
		seen[entry.PlanID] = struct{}{}
		order = append(order, entry.PlanID)
	}
	return order
}
