package monitor

import (
	"sort"
	"sync"
	"time"

	"github.com/wyw14/cry-96/internal/model"
)

type HealthStatus string

const (
	// HealthOnline means a fresh heartbeat was received within the timeout.
	HealthOnline HealthStatus = "online"
	// HealthOffline means the device previously reported but its stream is stale.
	HealthOffline HealthStatus = "offline"
	// HealthUnknown means no heartbeat has ever been accepted for the device.
	HealthUnknown HealthStatus = "unknown"
)

func (s HealthStatus) worseThan(other HealthStatus) bool {
	return s.Severity() > other.Severity()
}

// Severity ranks offline above unknown above online for chamber roll-ups.
func (s HealthStatus) Severity() int {
	switch s {
	case HealthOffline:
		return 2
	case HealthUnknown:
		return 1
	default:
		return 0
	}
}

type DeviceHealth struct {
	ChamberID  string           `json:"chamber_id"`
	Device     model.DeviceKind `json:"device"`
	Status     HealthStatus     `json:"status"`
	Online     bool             `json:"online"`
	Message    string           `json:"message,omitempty"`
	Sequence   uint64           `json:"sequence,omitempty"`
	ObservedAt time.Time        `json:"observed_at,omitempty"`
	ReceivedAt time.Time        `json:"received_at,omitempty"`
	CheckedAt  time.Time        `json:"checked_at"`
}

// HealthRegistry provides current operational health, not historical reports.
type HealthRegistry struct {
	mu      sync.RWMutex
	records map[string]DeviceHealth
}

func NewHealthRegistry() *HealthRegistry {
	return &HealthRegistry{records: make(map[string]DeviceHealth)}
}

func healthKey(chamberID string, device model.DeviceKind) string {
	return chamberID + ":" + string(device)
}

// RegisterUnknown reserves a device slot as unknown before any heartbeat
// arrives, so operators can distinguish "never reported" from "offline".
func (r *HealthRegistry) RegisterUnknown(chamberID string, device model.DeviceKind, now time.Time) DeviceHealth {
	record := DeviceHealth{
		ChamberID: chamberID, Device: device, Status: HealthUnknown,
		Message: "awaiting telemetry", CheckedAt: now.UTC(),
	}
	r.mu.Lock()
	if _, exists := r.records[healthKey(chamberID, device)]; !exists {
		r.records[healthKey(chamberID, device)] = record
	}
	r.mu.Unlock()
	return record
}

// ReportOnline records a fresh, accepted heartbeat.
func (r *HealthRegistry) ReportOnline(chamberID string, device model.DeviceKind, sequence uint64, status model.DeviceRunning, observedAt, receivedAt, now time.Time) {
	message := "telemetry current"
	if status != model.RunningOK {
		message = "device reports " + string(status)
	}
	record := DeviceHealth{
		ChamberID: chamberID, Device: device, Status: HealthOnline, Online: true,
		Message: message, Sequence: sequence,
		ObservedAt: observedAt.UTC(), ReceivedAt: receivedAt.UTC(), CheckedAt: now.UTC(),
	}
	r.mu.Lock()
	r.records[healthKey(chamberID, device)] = record
	r.mu.Unlock()
}

// MarkOffline flips a known device to offline. Devices still unknown stay
// unknown because "never heard from" is not the same as "went silent".
func (r *HealthRegistry) MarkOffline(chamberID string, device model.DeviceKind, message string, now time.Time) (DeviceHealth, bool) {
	key := healthKey(chamberID, device)
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.records[key]
	if !exists || record.Status == HealthUnknown {
		return record, false
	}
	record.Status = HealthOffline
	record.Online = false
	record.Message = message
	record.CheckedAt = now.UTC()
	r.records[key] = record
	return record, true
}

func (r *HealthRegistry) Get(chamberID string, device model.DeviceKind) (DeviceHealth, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[healthKey(chamberID, device)]
	return record, ok
}

func (r *HealthRegistry) List() []DeviceHealth {
	r.mu.RLock()
	result := make([]DeviceHealth, 0, len(r.records))
	for _, record := range r.records {
		result = append(result, record)
	}
	r.mu.RUnlock()
	sort.Slice(result, func(i, j int) bool {
		if result[i].ChamberID == result[j].ChamberID {
			return result[i].Device < result[j].Device
		}
		return result[i].ChamberID < result[j].ChamberID
	})
	return result
}

// ChamberStatus rolls devices up to the worst reported status. A chamber
// without any registered device is unknown.
func (r *HealthRegistry) ChamberStatus(chamberID string) HealthStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	worst := HealthOnline
	found := false
	for _, record := range r.records {
		if record.ChamberID != chamberID {
			continue
		}
		found = true
		if record.Status.worseThan(worst) {
			worst = record.Status
		}
	}
	if !found {
		return HealthUnknown
	}
	return worst
}

// AllOnline is true only when every registered device is online.
func (r *HealthRegistry) AllOnline(chamberID string) bool {
	return r.ChamberStatus(chamberID) == HealthOnline
}
