package model

import (
	"errors"
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

// PlanPriority orders not-yet-started plans; smaller values admit first.
// Normal is the Go zero-value default applied when a caller omits priority.
type PlanPriority int

const (
	PriorityHigh   PlanPriority = 1
	PriorityNormal PlanPriority = 2
	PriorityLow    PlanPriority = 3
)

func (p PlanPriority) Valid() bool {
	return p >= PriorityHigh && p <= PriorityLow
}

// PlanPreferences captures the operator-controlled scheduling inputs.
type PlanPreferences struct {
	Priority PlanPriority
	// ETA is the expected arrival at the chamber; the zero value means unknown.
	ETA time.Time
}

// NormalizePlanPreferences falls back to normal priority when no value is given
// and stores every timestamp in UTC.
func NormalizePlanPreferences(preferences PlanPreferences) PlanPreferences {
	if preferences.Priority == 0 {
		preferences.Priority = PriorityNormal
	}
	if !preferences.ETA.IsZero() {
		preferences.ETA = preferences.ETA.UTC()
	}
	return preferences
}

type ConvoyPlan struct {
	ID         uuid.UUID    `json:"id"`
	ChamberID  string       `json:"chamber_id"`
	Direction  Direction    `json:"direction"`
	Vessels    []Vessel     `json:"vessels"`
	Priority   PlanPriority `json:"priority"`
	ETA        time.Time    `json:"eta,omitempty"`
	CreatedAt  time.Time    `json:"created_at"`
	Cancelled  bool         `json:"cancelled"`
	CancelNote string       `json:"cancel_note,omitempty"`
}

func NewConvoyPlan(chamberID string, direction Direction, vessels []Vessel, preferences PlanPreferences, now time.Time) (ConvoyPlan, error) {
	if chamberID == "" || !direction.Valid() || len(vessels) == 0 {
		return ConvoyPlan{}, errors.New("plan requires chamber, direction and vessels")
	}
	preferences = NormalizePlanPreferences(preferences)
	if !preferences.Priority.Valid() {
		return ConvoyPlan{}, errors.New("plan priority is out of range")
	}
	copyOfVessels := append([]Vessel(nil), vessels...)
	return ConvoyPlan{
		ID: uuid.New(), ChamberID: chamberID, Direction: direction,
		Vessels: copyOfVessels, Priority: preferences.Priority, ETA: preferences.ETA,
		CreatedAt: now.UTC(),
	}, nil
}

func (p ConvoyPlan) Clone() ConvoyPlan {
	p.Vessels = append([]Vessel(nil), p.Vessels...)
	return p
}

// DispatchRank is the ordering basis the admission queue serves plans by.
// Released vessels always precede waiting vessels (they are already past the
// signal and cannot be reordered); among waiting vessels the ordering is
// priority, then expected arrival, then first-come time, with plan ID as the
// final deterministic tie-breaker.
type DispatchRank struct {
	Priority  PlanPriority
	ETA       time.Time
	ArrivedAt time.Time
	PlanID    uuid.UUID
}

// RankBefore reports whether a should be served before b.
func RankBefore(a, b DispatchRank) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	if !a.ETA.Equal(b.ETA) {
		// A known ETA always sorts ahead of an unknown one.
		if a.ETA.IsZero() {
			return false
		}
		if b.ETA.IsZero() {
			return true
		}
		return a.ETA.Before(b.ETA)
	}
	if !a.ArrivedAt.Equal(b.ArrivedAt) {
		return a.ArrivedAt.Before(b.ArrivedAt)
	}
	return a.PlanID.String() < b.PlanID.String()
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
