package telemetry

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

// DeviceObservation is the latest accepted device-side state. It is only
// ever written from a heartbeat that passed the freshness checks.
type DeviceObservation struct {
	ChamberID  string              `json:"chamber_id"`
	Device     model.DeviceKind    `json:"device"`
	Status     model.DeviceRunning `json:"status"`
	Position   model.GatePosition  `json:"position,omitempty"`
	OpeningPct *float64            `json:"opening_pct,omitempty"`
	Sequence   uint64              `json:"sequence"`
	ObservedAt time.Time           `json:"observed_at"`
	ReceivedAt time.Time           `json:"received_at"`
}

// ObservationStore keeps one current observation per chamber/device.
type ObservationStore struct {
	mu      sync.RWMutex
	records map[string]DeviceObservation
}

func NewObservationStore() *ObservationStore {
	return &ObservationStore{records: make(map[string]DeviceObservation)}
}

func observationKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

// Put replaces the current observation. Callers must have already proven
// the incoming heartbeat is fresher than the stored one.
func (s *ObservationStore) Put(observation DeviceObservation) {
	s.mu.Lock()
	s.records[observationKey(observation.ChamberID, observation.Device)] = observation
	s.mu.Unlock()
}

func (s *ObservationStore) Get(chamberID string, device model.DeviceKind) (DeviceObservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	observation, ok := s.records[observationKey(chamberID, device)]
	return observation, ok
}

func (s *ObservationStore) List() []DeviceObservation {
	s.mu.RLock()
	result := make([]DeviceObservation, 0, len(s.records))
	for _, observation := range s.records {
		result = append(result, observation)
	}
	s.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].ChamberID == result[j].ChamberID {
			return result[i].Device < result[j].Device
		}
		return result[i].ChamberID < result[j].ChamberID
	})
	return result
}
