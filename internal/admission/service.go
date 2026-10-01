package admission

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/journal"
	"github.com/wyw14/cry-96/internal/model"
)

// Service records queue changes and delegates safe release decisions.
type Service struct {
	controller *Controller
	recorder   *journal.Recorder
	now        func() time.Time
}

func NewService(controller *Controller, recorder *journal.Recorder, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{controller: controller, recorder: recorder, now: now}
}

func (s *Service) Enqueue(ctx context.Context, plan model.ConvoyPlan) error {
	if err := s.controller.queue.Add(plan.ChamberID, plan); err != nil {
		return err
	}
	_, err := s.recorder.Record(ctx, plan.ChamberID, model.EventPlanCreated, plan.ID, 0, map[string]any{
		"plan_id": plan.ID, "queued": len(plan.Vessels), "at": s.now().UTC(),
	})
	return err
}

// SetSchedule reorders a waiting plan. Plans with a released vessel cannot be
// reordered; absence is reported rather than treated as success.
func (s *Service) SetSchedule(chamberID string, plan model.ConvoyPlan, preferences model.PlanPreferences) (bool, error) {
	return s.controller.queue.SetPlanSchedule(chamberID, plan.ID, preferences)
}

func (s *Service) Admit(ctx context.Context, cycle model.TransitCycle) (Decision, error) {
	decision, err := s.controller.Evaluate(ctx, cycle)
	if err != nil || !decision.Allowed {
		return decision, err
	}
	_, err = s.recorder.Record(ctx, cycle.ChamberID, model.EventSignalChanged, cycle.ID, cycle.Generation, decision)
	return decision, err
}

func (s *Service) RemovePlan(chamberID string, plan model.ConvoyPlan) []QueueEntry {
	return s.controller.queue.RemovePlan(chamberID, plan.ID)
}

func (s *Service) Restore(chamberID string, entries []QueueEntry) {
	s.controller.queue.Restore(chamberID, entries)
}

func (s *Service) List(chamberID string) []QueueEntry {
	return s.controller.queue.List(chamberID)
}

func (s *Service) PlanOrder(chamberID string) []uuid.UUID {
	return s.controller.queue.PlanOrder(chamberID)
}
