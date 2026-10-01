package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/signal"
)

type vesselInput struct {
	Name     string  `json:"name"`
	CallSign string  `json:"call_sign"`
	LengthM  float64 `json:"length_m"`
}

type planInput struct {
	ChamberID string          `json:"chamber_id"`
	Direction model.Direction `json:"direction"`
	Vessels   []vesselInput   `json:"vessels"`
	Priority  int             `json:"priority,omitempty"`
	ETA       string          `json:"eta,omitempty"`
}

// nullableTime distinguishes a field left out (Set == false) from an explicit
// JSON null (Clear == true) and an RFC3339 timestamp.
type nullableTime struct {
	Set   bool
	Clear bool
	Value time.Time
}

func (t *nullableTime) UnmarshalJSON(data []byte) error {
	t.Set = true
	if string(data) == "null" {
		t.Clear = true
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return errors.New("eta must be an RFC3339 timestamp")
	}
	t.Value = parsed
	return nil
}

type scheduleInput struct {
	Priority *int         `json:"priority"`
	ETA      nullableTime `json:"eta"`
}

func createPlanHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input planInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		vessels := make([]model.Vessel, 0, len(input.Vessels))
		for _, item := range input.Vessels {
			vessel, err := model.NewVessel(item.Name, item.CallSign, item.LengthM)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			vessels = append(vessels, vessel)
		}
		preferences := model.PlanPreferences{Priority: model.PlanPriority(input.Priority)}
		if input.ETA != "" {
			eta, err := time.Parse(time.RFC3339, input.ETA)
			if err != nil {
				writeError(w, http.StatusBadRequest, errors.New("eta must be an RFC3339 timestamp"))
				return
			}
			preferences.ETA = eta
		}
		plan, err := runtime.Schedule.Create(r.Context(), input.ChamberID, input.Direction, vessels, preferences)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		cycleValue, err := runtime.CycleService.Start(r.Context(), plan)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"plan": plan, "cycle": cycleValue})
	}
}

func plansHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := make(map[string]any, len(runtime.Chambers))
		for _, chamberID := range runtime.Chambers {
			result[chamberID] = runtime.Schedule.Active(chamberID)
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// updatePlanScheduleHandler adjusts priority/ETA of a plan that has not
// started moving. Released vessels keep their position, so such updates are
// rejected with 409.
func updatePlanScheduleHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID, err := uuid.Parse(chi.URLParam(r, "planID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var input scheduleInput
		if err := decodeJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if input.Priority == nil && !input.ETA.Set {
			writeError(w, http.StatusBadRequest, errors.New("priority or eta is required"))
			return
		}
		if input.Priority != nil && !model.PlanPriority(*input.Priority).Valid() {
			writeError(w, http.StatusBadRequest, errors.New("priority must be one of 1 (high), 2 (normal), 3 (low)"))
			return
		}
		var update struct {
			priority model.PlanPriority
			eta      *time.Time
		}
		if input.Priority != nil {
			update.priority = model.PlanPriority(*input.Priority)
		}
		if input.ETA.Set {
			if input.ETA.Clear {
				cleared := time.Time{}
				update.eta = &cleared
			} else {
				update.eta = &input.ETA.Value
			}
		}
		if _, ok := runtime.Planner.Get(planID); !ok {
			writeError(w, http.StatusNotFound, errors.New("plan not found"))
			return
		}
		plan, err := runtime.Schedule.SetSchedule(r.Context(), planID, update.priority, update.eta)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusOK, plan)
	}
}

func publishSignalsHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID, err := uuid.Parse(chi.URLParam(r, "planID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		plan, ok := runtime.Planner.Get(planID)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("plan not found"))
			return
		}
		cycleValue, ok := runtime.Cycles.Current(plan.ChamberID)
		if !ok {
			writeError(w, http.StatusConflict, errors.New("active transit not found"))
			return
		}
		publication, err := runtime.Schedule.Publish(r.Context(), planID, cycleValue)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		writeJSON(w, http.StatusAccepted, publication)
	}
}

func cancelPlanHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		planID, err := uuid.Parse(chi.URLParam(r, "planID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		plan, ok := runtime.Planner.Get(planID)
		if !ok {
			writeError(w, http.StatusNotFound, errors.New("plan not found"))
			return
		}
		cycleValue, ok := runtime.Cycles.Current(plan.ChamberID)
		publication := signal.Publication{
			CycleID: cycleValue.ID, Generation: cycleValue.Generation,
			Previous: make(map[uuid.UUID]model.SignalState),
		}
		if ok {
			for _, vessel := range plan.Vessels {
				if _, exists := runtime.SignalStore.Get(vessel.ID); exists {
					publication.Published = append(publication.Published, vessel.ID)
				}
			}
		}
		result, err := runtime.Schedule.Cancel(r.Context(), planID, "operator cancellation", publication)
		if err != nil {
			writeError(w, http.StatusConflict, err)
			return
		}
		if ok {
			_, _ = runtime.Cycles.Cancel(r.Context(), plan.ChamberID, "operator cancellation")
		}
		writeJSON(w, http.StatusOK, result)
	}
}
