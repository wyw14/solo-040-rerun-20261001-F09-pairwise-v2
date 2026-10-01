package schedule

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/admission"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// Planner owns convoy plans and their admission queue membership.
type Planner struct {
	mu        sync.RWMutex
	plans     map[uuid.UUID]model.ConvoyPlan
	admission *admission.Service
	recorder  *journal.Recorder
	now       func() time.Time
}

func NewPlanner(admissionService *admission.Service, recorder *journal.Recorder, now func() time.Time) *Planner {
	if now == nil {
		now = time.Now
	}
	return &Planner{
		plans: make(map[uuid.UUID]model.ConvoyPlan), admission: admissionService,
		recorder: recorder, now: now,
	}
}

func (p *Planner) Create(ctx context.Context, chamberID string, direction model.Direction, vessels []model.Vessel, preferences model.PlanPreferences) (model.ConvoyPlan, error) {
	plan, err := model.NewConvoyPlan(chamberID, direction, vessels, preferences, p.now())
	if err != nil {
		return model.ConvoyPlan{}, err
	}
	if err := p.admission.Enqueue(ctx, plan); err != nil {
		return model.ConvoyPlan{}, err
	}
	p.mu.Lock()
	p.plans[plan.ID] = plan
	p.mu.Unlock()
	if _, err := p.recorder.Record(ctx, chamberID, model.EventPlanCreated, plan.ID, 0, plan); err != nil {
		p.mu.Lock()
		delete(p.plans, plan.ID)
		p.mu.Unlock()
		p.admission.RemovePlan(chamberID, plan)
		return model.ConvoyPlan{}, err
	}
	return plan.Clone(), nil
}

// ScheduleUpdate carries partial adjustments to a waiting plan. A zero
// Priority leaves priority unchanged; a nil ETA leaves ETA unchanged, while a
// pointer to the zero time clears it.
type ScheduleUpdate struct {
	Priority model.PlanPriority
	ETA      *time.Time
}

// SetSchedule adjusts priority/ETA of a plan that has not started moving and
// replans the admission queue so the change is reflected in admission order.
// Released and cancelled plans are rejected.
func (p *Planner) SetSchedule(ctx context.Context, planID uuid.UUID, update ScheduleUpdate) (model.ConvoyPlan, error) {
	plan, err := p.require(planID)
	if err != nil {
		return model.ConvoyPlan{}, err
	}
	if plan.Cancelled {
		return model.ConvoyPlan{}, errors.New("cancelled plan cannot be rescheduled")
	}
	preferences := model.PlanPreferences{Priority: plan.Priority, ETA: plan.ETA}
	if update.Priority != 0 {
		if !update.Priority.Valid() {
			return model.ConvoyPlan{}, errors.New("plan priority is out of range")
		}
		preferences.Priority = update.Priority
	}
	if update.ETA != nil {
		preferences.ETA = update.ETA.UTC()
	}
	queued, err := p.admission.SetSchedule(plan.ChamberID, plan, preferences)
	if err != nil {
		return model.ConvoyPlan{}, err
	}
	if !queued {
		return model.ConvoyPlan{}, errors.New("plan is not waiting in the admission queue")
	}
	previous := model.PlanPreferences{Priority: plan.Priority, ETA: plan.ETA}
	plan.Priority, plan.ETA = preferences.Priority, preferences.ETA
	p.replace(plan)
	if _, err := p.recorder.Record(ctx, plan.ChamberID, model.EventPlanScheduleChanged, plan.ID, 0, map[string]any{
		"plan_id": plan.ID, "priority": plan.Priority, "eta": plan.ETA, "at": p.now().UTC(),
	}); err != nil {
		// Roll the queue and plan back so memory state stays consistent.
		p.admission.SetSchedule(plan.ChamberID, plan, previous)
		plan.Priority, plan.ETA = previous.Priority, previous.ETA
		p.replace(plan)
		return model.ConvoyPlan{}, err
	}
	return plan.Clone(), nil
}

func (p *Planner) Get(planID uuid.UUID) (model.ConvoyPlan, bool) {
	p.mu.RLock()
	plan, ok := p.plans[planID]
	p.mu.RUnlock()
	return plan.Clone(), ok
}

func (p *Planner) replace(plan model.ConvoyPlan) {
	p.mu.Lock()
	p.plans[plan.ID] = plan.Clone()
	p.mu.Unlock()
}

func (p *Planner) Active(chamberID string) []model.ConvoyPlan {
	p.mu.RLock()
	var result []model.ConvoyPlan
	for _, plan := range p.plans {
		if plan.ChamberID == chamberID && !plan.Cancelled {
			result = append(result, plan.Clone())
		}
	}
	p.mu.RUnlock()
	// Mirror the admission queue so the plan list and admission decisions
	// always agree; plans absent from the queue (fully admitted) trail behind.
	order := p.admission.PlanOrder(chamberID)
	position := make(map[uuid.UUID]int, len(order))
	for index, planID := range order {
		position[planID] = index
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, leftOK := position[result[i].ID]
		right, rightOK := position[result[j].ID]
		switch {
		case leftOK && rightOK:
			return left < right
		case leftOK:
			return true
		case rightOK:
			return false
		default:
			return result[i].CreatedAt.Before(result[j].CreatedAt)
		}
	})
	return result
}

func (p *Planner) require(planID uuid.UUID) (model.ConvoyPlan, error) {
	plan, ok := p.Get(planID)
	if !ok {
		return model.ConvoyPlan{}, errors.New("convoy plan not found")
	}
	return plan, nil
}
