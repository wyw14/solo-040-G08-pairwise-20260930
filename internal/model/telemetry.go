package model

import (
	"errors"
	"time"
)

// DeviceRunning describes the operational state reported by field equipment.
type DeviceRunning string

const (
	// RunningOK is a healthy heartbeat carrying current device state.
	RunningOK DeviceRunning = "ok"
	// RunningFault is a timely heartbeat where the device reports a failure.
	RunningFault DeviceRunning = "fault"
	// RunningStopped means the device is intentionally stopped, still reachable.
	RunningStopped DeviceRunning = "stopped"
)

func (s DeviceRunning) Valid() bool {
	return s == RunningOK || s == RunningFault || s == RunningStopped
}

func ValidDeviceKind(device DeviceKind) bool {
	switch device {
	case DeviceUpstreamGate, DeviceDownstreamGate, DeviceFillValve, DeviceDrainValve:
		return true
	default:
		return false
	}
}

// DeviceHeartbeat is the telemetry envelope sent by gates and valves.
// ObservedAt is stamped by the device; ReceivedAt is assigned by the
// collector and is never supplied by the field.
type DeviceHeartbeat struct {
	ChamberID  string        `json:"chamber_id"`
	Device     DeviceKind    `json:"device"`
	Sequence   uint64        `json:"sequence"`
	Status     DeviceRunning `json:"status"`
	Position   GatePosition  `json:"position,omitempty"`
	OpeningPct *float64      `json:"opening_pct,omitempty"`
	ObservedAt time.Time     `json:"observed_at"`
}

func (h DeviceHeartbeat) Validate() error {
	if h.ChamberID == "" {
		return errors.New("chamber_id is required")
	}
	if !ValidDeviceKind(h.Device) {
		return errors.New("device must be a gate or valve")
	}
	if h.Status == "" || !h.Status.Valid() {
		return errors.New("status must be ok, fault or stopped")
	}
	if h.ObservedAt.IsZero() {
		return errors.New("observed_at is required")
	}
	if h.Device == DeviceUpstreamGate || h.Device == DeviceDownstreamGate {
		if h.Position != "" && h.Position != GateOpen && h.Position != GateOpening &&
			h.Position != GateClosed && h.Position != GateClosing {
			return errors.New("gate position is invalid")
		}
	}
	if h.OpeningPct != nil && (*h.OpeningPct < 0 || *h.OpeningPct > 100) {
		return errors.New("opening_pct must be between 0 and 100")
	}
	return nil
}

// IsGate reports whether the heartbeat originates from a gate.
func (h DeviceHeartbeat) IsGate() bool {
	return h.Device == DeviceUpstreamGate || h.Device == DeviceDownstreamGate
}
