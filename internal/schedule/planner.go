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

func (p *Planner) Create(ctx context.Context, chamberID string, direction model.Direction, vessels []model.Vessel, priority int, eta *time.Time) (model.ConvoyPlan, error) {
	plan, err := model.NewConvoyPlan(chamberID, direction, vessels, priority, eta, p.now())
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

// Adjust updates the scheduling attributes of a plan that has not started
// and keeps the admission queue consistent with the new dispatch order.
func (p *Planner) Adjust(ctx context.Context, planID uuid.UUID, priority int, eta *time.Time) (model.ConvoyPlan, error) {
	if err := model.CheckPriority(priority); err != nil {
		return model.ConvoyPlan{}, err
	}
	plan, err := p.require(planID)
	if err != nil {
		return model.ConvoyPlan{}, err
	}
	if plan.Cancelled {
		return model.ConvoyPlan{}, errors.New("cancelled plans cannot be adjusted")
	}
	if p.admission.PlanStarted(plan.ChamberID, plan.ID) {
		return model.ConvoyPlan{}, errors.New("plan already started; released vessels cannot be reordered")
	}
	plan.Priority = priority
	if eta != nil {
		arrival := eta.UTC()
		plan.ETA = &arrival
	} else {
		plan.ETA = nil
	}
	p.replace(plan)
	p.admission.ReorderPlan(plan.ChamberID, plan)
	if _, err := p.recorder.Record(ctx, plan.ChamberID, model.EventPlanUpdated, plan.ID, 0, map[string]any{
		"plan_id": plan.ID, "priority": plan.Priority, "eta": plan.ETA, "at": p.now().UTC(),
	}); err != nil {
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

// Active lists non-cancelled plans in the same dispatch order the admission
// queue uses, so the plan list and the queue never disagree.
func (p *Planner) Active(chamberID string) []model.ConvoyPlan {
	p.mu.RLock()
	var result []model.ConvoyPlan
	for _, plan := range p.plans {
		if plan.ChamberID == chamberID && !plan.Cancelled {
			result = append(result, plan.Clone())
		}
	}
	p.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		return result[i].DispatchOrder().Less(result[j].DispatchOrder())
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
