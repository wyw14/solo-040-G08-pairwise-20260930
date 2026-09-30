package telemetry

import (
	"context"
	"errors"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/monitor"
)

const (
	// ReasonFresh: heartbeat arrived in order with a current timestamp.
	ReasonFresh = "fresh"
	// ReasonLate: heartbeat arrived with a timestamp well behind server time.
	ReasonLate = "late"
	// ReasonReordered: sequence moved backwards but the observation is newer.
	ReasonReordered = "reordered"
	// ReasonStale: heartbeat is older than what was already accepted.
	ReasonStale = "stale"
	// ReasonDuplicate: identical observation was already accepted.
	ReasonDuplicate = "duplicate"
	// ReasonFuture: heartbeat claims to be observed too far in the future.
	ReasonFuture = "future"
)

// Config tunes heartbeat freshness decisions.
type Config struct {
	Timeout         time.Duration
	LateThreshold   time.Duration
	FutureTolerance time.Duration
}

func DefaultConfig() Config {
	return Config{
		Timeout:         15 * time.Second,
		LateThreshold:   5 * time.Second,
		FutureTolerance: 2 * time.Second,
	}
}

// ErrStale is returned when a late heartbeat must not overwrite newer data.
var (
	ErrStale     = errors.New("stale heartbeat: newer observation already applied")
	ErrDuplicate = errors.New("duplicate heartbeat: observation already applied")
	ErrFuture    = errors.New("heartbeat observed_at is too far in the future")
)

// IngestResult reports how a heartbeat was classified.
type IngestResult struct {
	Accepted    bool                 `json:"accepted"`
	Reason      string               `json:"reason"`
	ChamberID   string               `json:"chamber_id"`
	Device      model.DeviceKind     `json:"device"`
	Sequence    uint64               `json:"sequence"`
	ObservedAt  time.Time            `json:"observed_at"`
	ReceivedAt  time.Time            `json:"received_at"`
	Health      monitor.DeviceHealth `json:"health"`
	Observation *DeviceObservation   `json:"observation,omitempty"`
}

// Collector ingests gate/valve heartbeats, classifies their delivery and
// only lets proven-fresh observations reach health and device state.
type Collector struct {
	health       *monitor.HealthRegistry
	observations *ObservationStore
	gates        *gate.State
	config       Config
	now          func() time.Time
}

func NewCollector(health *monitor.HealthRegistry, observations *ObservationStore, gates *gate.State, config Config, now func() time.Time) *Collector {
	if now == nil {
		now = time.Now
	}
	if config.Timeout <= 0 || config.LateThreshold <= 0 || config.FutureTolerance < 0 {
		config = DefaultConfig()
	}
	return &Collector{
		health: health, observations: observations, gates: gates,
		config: config, now: now,
	}
}

// RegisterDevices marks expected chamber/device pairs as unknown until the
// first heartbeat proves them online.
func (c *Collector) RegisterDevices(chambers []string, devices []model.DeviceKind) {
	now := c.now().UTC()
	for _, chamberID := range chambers {
		for _, device := range devices {
			c.health.RegisterUnknown(chamberID, device, now)
		}
	}
}

func gateDirection(device model.DeviceKind) (model.Direction, bool) {
	switch device {
	case model.DeviceUpstreamGate:
		return model.DirectionUpstream, true
	case model.DeviceDownstreamGate:
		return model.DirectionDownstream, true
	default:
		return "", false
	}
}

// Ingest validates and applies one heartbeat. It never mutates device state
// for stale, duplicate or future-dated heartbeats.
func (c *Collector) Ingest(heartbeat model.DeviceHeartbeat) (IngestResult, error) {
	if err := heartbeat.Validate(); err != nil {
		return IngestResult{}, err
	}
	now := c.now()
	receivedAt := now.UTC()
	observedAt := heartbeat.ObservedAt.UTC()

	result := IngestResult{
		ChamberID: heartbeat.ChamberID, Device: heartbeat.Device,
		Sequence: heartbeat.Sequence, ObservedAt: observedAt, ReceivedAt: receivedAt,
	}

	if observedAt.After(now.Add(c.config.FutureTolerance)) {
		result.Reason = ReasonFuture
		result.Health, _ = c.health.Get(heartbeat.ChamberID, heartbeat.Device)
		return result, ErrFuture
	}

	if previous, exists := c.observations.Get(heartbeat.ChamberID, heartbeat.Device); exists {
		switch {
		case observedAt.Before(previous.ObservedAt):
			// Late delivery of an old sample: it must never cover new data.
			result.Reason = ReasonStale
			result.Health, _ = c.health.Get(heartbeat.ChamberID, heartbeat.Device)
			return result, ErrStale
		case observedAt.Equal(previous.ObservedAt) && heartbeat.Sequence <= previous.Sequence:
			result.Reason = ReasonDuplicate
			result.Health, _ = c.health.Get(heartbeat.ChamberID, heartbeat.Device)
			return result, ErrDuplicate
		}
	}

	reason := ReasonFresh
	if now.Sub(observedAt) > c.config.LateThreshold {
		reason = ReasonLate
	}
	if previous, exists := c.observations.Get(heartbeat.ChamberID, heartbeat.Device); exists &&
		heartbeat.Sequence < previous.Sequence {
		// Sequence went backwards while the observation time advanced:
		// reordered delivery. Timestamp wins, but flag it for operators.
		reason = ReasonReordered
	}

	observation := DeviceObservation{
		ChamberID: heartbeat.ChamberID, Device: heartbeat.Device,
		Status: heartbeat.Status, Position: heartbeat.Position,
		OpeningPct: heartbeat.OpeningPct, Sequence: heartbeat.Sequence,
		ObservedAt: observedAt, ReceivedAt: receivedAt,
	}
	if direction, isGate := gateDirection(heartbeat.Device); isGate && heartbeat.Position != "" {
		if err := c.gates.Observe(heartbeat.ChamberID, direction, heartbeat.Position, observedAt); err != nil {
			return IngestResult{}, err
		}
	}
	c.observations.Put(observation)
	c.health.ReportOnline(heartbeat.ChamberID, heartbeat.Device, heartbeat.Sequence,
		heartbeat.Status, observedAt, receivedAt, now)

	result.Accepted = true
	result.Reason = reason
	accepted := observation
	result.Observation = &accepted
	result.Health, _ = c.health.Get(heartbeat.ChamberID, heartbeat.Device)
	return result, nil
}

// DeviceStatus pairs current health with the last accepted observation.
type DeviceStatus struct {
	Health      monitor.DeviceHealth `json:"health"`
	Observation *DeviceObservation   `json:"observation,omitempty"`
}

// Devices reports status for every registered chamber/device pair.
func (c *Collector) Devices() []DeviceStatus {
	result := make([]DeviceStatus, 0)
	for _, health := range c.health.List() {
		entry := DeviceStatus{Health: health}
		if observation, ok := c.observations.Get(health.ChamberID, health.Device); ok {
			observationCopy := observation
			entry.Observation = &observationCopy
		}
		result = append(result, entry)
	}
	return result
}

// Sweep marks online devices offline when no heartbeat has been received
// within Timeout. Timing out uses server receive time, not device clocks.
// Unknown devices are left untouched. Returns devices that changed.
func (c *Collector) Sweep() []DeviceStatus {
	now := c.now().UTC()
	changed := make([]DeviceStatus, 0)
	for _, health := range c.health.List() {
		if health.Status != monitor.HealthOnline {
			continue
		}
		if now.Sub(health.ReceivedAt) <= c.config.Timeout {
			continue
		}
		updated, ok := c.health.MarkOffline(health.ChamberID, health.Device,
			"heartbeat timed out", now)
		if !ok {
			continue
		}
		entry := DeviceStatus{Health: updated}
		if observation, exists := c.observations.Get(health.ChamberID, health.Device); exists {
			observationCopy := observation
			entry.Observation = &observationCopy
		}
		changed = append(changed, entry)
	}
	return changed
}

// Maintain runs Sweep on a ticker until the context is cancelled.
func (c *Collector) Maintain(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = c.config.Timeout / 3
		if interval <= 0 {
			interval = 5 * time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sweep()
		}
	}
}
