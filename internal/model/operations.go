package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Vessel struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	CallSign string    `json:"call_sign"`
	LengthM  float64   `json:"length_m"`
}

func NewVessel(name, callSign string, lengthM float64) (Vessel, error) {
	if name == "" || callSign == "" || lengthM <= 0 {
		return Vessel{}, errors.New("vessel name, call sign and positive length are required")
	}
	return Vessel{ID: uuid.New(), Name: name, CallSign: callSign, LengthM: lengthM}, nil
}

// MaxPriority bounds the operator-assignable dispatch priority (0 = normal).
const MaxPriority = 9

type ConvoyPlan struct {
	ID         uuid.UUID  `json:"id"`
	ChamberID  string     `json:"chamber_id"`
	Direction  Direction  `json:"direction"`
	Vessels    []Vessel   `json:"vessels"`
	Priority   int        `json:"priority"`
	ETA        *time.Time `json:"eta,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Cancelled  bool       `json:"cancelled"`
	CancelNote string     `json:"cancel_note,omitempty"`
}

func NewConvoyPlan(chamberID string, direction Direction, vessels []Vessel, priority int, eta *time.Time, now time.Time) (ConvoyPlan, error) {
	if chamberID == "" || !direction.Valid() || len(vessels) == 0 {
		return ConvoyPlan{}, errors.New("plan requires chamber, direction and vessels")
	}
	if err := CheckPriority(priority); err != nil {
		return ConvoyPlan{}, err
	}
	copyOfVessels := append([]Vessel(nil), vessels...)
	plan := ConvoyPlan{
		ID: uuid.New(), ChamberID: chamberID, Direction: direction,
		Vessels: copyOfVessels, Priority: priority, CreatedAt: now.UTC(),
	}
	if eta != nil {
		arrival := eta.UTC()
		plan.ETA = &arrival
	}
	return plan, nil
}

func CheckPriority(priority int) error {
	if priority < 0 || priority > MaxPriority {
		return fmt.Errorf("priority must be between 0 and %d", MaxPriority)
	}
	return nil
}

// DispatchOrder is the sorting basis shared by the admission queue and plan
// listings: higher priority first, then earlier expected arrival, then
// first-come-first-served by creation time.
type DispatchOrder struct {
	Priority int       `json:"priority"`
	Arrival  time.Time `json:"arrival"`
	Created  time.Time `json:"created"`
	PlanID   uuid.UUID `json:"plan_id"`
}

// DispatchOrder derives the plan's sort key; ETA overrides the arrival basis.
func (p ConvoyPlan) DispatchOrder() DispatchOrder {
	arrival := p.CreatedAt
	if p.ETA != nil {
		arrival = *p.ETA
	}
	return DispatchOrder{Priority: p.Priority, Arrival: arrival, Created: p.CreatedAt, PlanID: p.ID}
}

// Less orders by descending priority, then ascending expected arrival, then
// FCFS insertion order with the plan id as final deterministic tiebreak.
func (o DispatchOrder) Less(other DispatchOrder) bool {
	if o.Priority != other.Priority {
		return o.Priority > other.Priority
	}
	if !o.Arrival.Equal(other.Arrival) {
		return o.Arrival.Before(other.Arrival)
	}
	if !o.Created.Equal(other.Created) {
		return o.Created.Before(other.Created)
	}
	return o.PlanID.String() < other.PlanID.String()
}

func (p ConvoyPlan) Clone() ConvoyPlan {
	p.Vessels = append([]Vessel(nil), p.Vessels...)
	if p.ETA != nil {
		eta := *p.ETA
		p.ETA = &eta
	}
	return p
}

type Lease struct {
	VesselID     uuid.UUID `json:"vessel_id"`
	Owner        string    `json:"owner"`
	FencingToken uint64    `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (l Lease) Active(now time.Time) bool {
	return l.Owner != "" && l.FencingToken > 0 && now.Before(l.ExpiresAt)
}

type SignalAspect string

const (
	SignalStop    SignalAspect = "stop"
	SignalProceed SignalAspect = "proceed"
	SignalHold    SignalAspect = "hold"
)

type SignalState struct {
	VesselID   uuid.UUID    `json:"vessel_id"`
	CycleID    uuid.UUID    `json:"cycle_id"`
	Generation uint64       `json:"generation"`
	Aspect     SignalAspect `json:"aspect"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

type EmergencyStatus string

const (
	EmergencyIdle      EmergencyStatus = "idle"
	EmergencyDraining  EmergencyStatus = "draining"
	EmergencyCompleted EmergencyStatus = "completed"
	EmergencyFailed    EmergencyStatus = "failed"
)

type EmergencyRecord struct {
	ID          uuid.UUID       `json:"id"`
	ChamberID   string          `json:"chamber_id"`
	Status      EmergencyStatus `json:"status"`
	RequestedAt time.Time       `json:"requested_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	Message     string          `json:"message,omitempty"`
}
