package telemetry

import (
	"errors"
	"testing"
	"time"

	"github.com/wyw14/cry-96/internal/gate"
	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/monitor"
)

const testChamber = "chamber-1"

type fixture struct {
	now time.Time
	c   *Collector
}

func newFixture(t *testing.T, config Config) *fixture {
	t.Helper()
	f := &fixture{now: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	health := monitor.NewHealthRegistry()
	observations := NewObservationStore()
	gates := gate.NewState(testChamber, "chamber-2")
	f.c = NewCollector(health, observations, gates, config, func() time.Time { return f.now })
	f.c.RegisterDevices([]string{testChamber}, []model.DeviceKind{
		model.DeviceUpstreamGate, model.DeviceFillValve,
	})
	return f
}

func heartbeat(device model.DeviceKind, sequence uint64, status model.DeviceRunning, observedAt time.Time) model.DeviceHeartbeat {
	hb := model.DeviceHeartbeat{
		ChamberID: testChamber, Device: device, Sequence: sequence,
		Status: status, ObservedAt: observedAt,
	}
	switch device {
	case model.DeviceUpstreamGate:
		hb.Position = model.GateOpen
	case model.DeviceFillValve:
		opening := 40.0
		hb.OpeningPct = &opening
	}
	return hb
}

func TestStartsUnknown(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	status := f.c.health.ChamberStatus(testChamber)
	if status != monitor.HealthUnknown {
		t.Fatalf("expected unknown before any heartbeat, got %q", status)
	}
	for _, entry := range f.c.Devices() {
		if entry.Health.Status != monitor.HealthUnknown || entry.Observation != nil {
			t.Fatalf("device %s should be unknown with no observation", entry.Health.Device)
		}
	}
}

func TestFreshHeartbeatComesOnlineAndAppliesGateObservation(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	result, err := f.c.Ingest(heartbeat(model.DeviceUpstreamGate, 1, model.RunningOK, f.now.Add(-time.Second)))
	if err != nil || !result.Accepted || result.Reason != ReasonFresh {
		t.Fatalf("expected fresh accept, got accepted=%v reason=%q err=%v", result.Accepted, result.Reason, err)
	}
	if result.Health.Status != monitor.HealthOnline {
		t.Fatalf("expected online, got %q", result.Health.Status)
	}
	position := f.c.gates.Snapshot(testChamber)[model.DirectionUpstream]
	if position != model.GateOpen {
		t.Fatalf("gate state should reflect heartbeat, got %q", position)
	}
}

func TestLateHeartbeatIsClassifiedButApplied(t *testing.T) {
	config := DefaultConfig()
	f := newFixture(t, config)
	late := f.now.Add(-(config.LateThreshold + time.Second))
	result, err := f.c.Ingest(heartbeat(model.DeviceFillValve, 1, model.RunningOK, late))
	if err != nil || !result.Accepted || result.Reason != ReasonLate {
		t.Fatalf("expected late accept, got accepted=%v reason=%q err=%v", result.Accepted, result.Reason, err)
	}
}

func TestStaleHeartbeatMustNotOverwriteNewerData(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	newer := heartbeat(model.DeviceUpstreamGate, 5, model.RunningOK, f.now.Add(-time.Second))
	newer.Position = model.GateOpen
	if _, err := f.c.Ingest(newer); err != nil {
		t.Fatal(err)
	}
	older := heartbeat(model.DeviceUpstreamGate, 6, model.RunningOK, f.now.Add(-10*time.Second))
	older.Position = model.GateClosed
	result, err := f.c.Ingest(older)
	if !errors.Is(err, ErrStale) || result.Reason != ReasonStale || result.Accepted {
		t.Fatalf("expected ErrStale, got reason=%q err=%v", result.Reason, err)
	}
	position := f.c.gates.Snapshot(testChamber)[model.DirectionUpstream]
	if position != model.GateOpen {
		t.Fatalf("stale heartbeat overwrote gate state with %q", position)
	}
	observation, ok := f.c.observations.Get(testChamber, model.DeviceUpstreamGate)
	if !ok || observation.Sequence != 5 {
		t.Fatalf("observation must remain the newer sequence 5, got %+v", observation)
	}
}

func TestDuplicateHeartbeatIsRejected(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	hb := heartbeat(model.DeviceFillValve, 2, model.RunningOK, f.now.Add(-time.Second))
	if _, err := f.c.Ingest(hb); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Ingest(hb); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestReorderedSequenceWithNewerTimestampIsAcceptedAndFlagged(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	first := heartbeat(model.DeviceFillValve, 10, model.RunningOK, f.now.Add(-2*time.Second))
	if _, err := f.c.Ingest(first); err != nil {
		t.Fatal(err)
	}
	// Delayed frame with a lower sequence but a later observation time.
	second := heartbeat(model.DeviceFillValve, 7, model.RunningOK, f.now.Add(-time.Second))
	result, err := f.c.Ingest(second)
	if err != nil || !result.Accepted {
		t.Fatalf("reordered-but-newer should be accepted, got %v", err)
	}
	if result.Reason != ReasonReordered {
		t.Fatalf("expected reordered reason, got %q", result.Reason)
	}
	observation, _ := f.c.observations.Get(testChamber, model.DeviceFillValve)
	if observation.Sequence != 7 {
		t.Fatalf("expected latest observation sequence 7, got %d", observation.Sequence)
	}
}

func TestFutureHeartbeatIsRejectedAndKeepsState(t *testing.T) {
	config := DefaultConfig()
	f := newFixture(t, config)
	current := heartbeat(model.DeviceFillValve, 1, model.RunningOK, f.now.Add(-time.Second))
	if _, err := f.c.Ingest(current); err != nil {
		t.Fatal(err)
	}
	future := heartbeat(model.DeviceFillValve, 2, model.RunningFault, f.now.Add(config.FutureTolerance+time.Minute))
	if _, err := f.c.Ingest(future); !errors.Is(err, ErrFuture) {
		t.Fatalf("expected ErrFuture, got %v", err)
	}
	observation, _ := f.c.observations.Get(testChamber, model.DeviceFillValve)
	if observation.Sequence != 1 || observation.Status != model.RunningOK {
		t.Fatalf("future heartbeat must not change state, got seq=%d status=%s", observation.Sequence, observation.Status)
	}
}

func TestTimeoutMarksOfflineThenHeartbeatRestoresOnline(t *testing.T) {
	config := Config{Timeout: 15 * time.Second, LateThreshold: 5 * time.Second, FutureTolerance: time.Second}
	f := newFixture(t, config)
	hb := heartbeat(model.DeviceFillValve, 1, model.RunningOK, f.now.Add(-time.Second))
	if _, err := f.c.Ingest(hb); err != nil {
		t.Fatal(err)
	}
	if changed := f.c.Sweep(); len(changed) != 0 {
		t.Fatalf("fresh device should not time out, got %d changes", len(changed))
	}
	// Advance past timeout; unknown upstream gate must stay unknown.
	f.now = f.now.Add(16 * time.Second)
	changed := f.c.Sweep()
	if len(changed) != 1 || changed[0].Health.Device != model.DeviceFillValve {
		t.Fatalf("expected exactly the valve to go offline, got %+v", changed)
	}
	if status := f.c.health.ChamberStatus(testChamber); status != monitor.HealthOffline {
		t.Fatalf("expected offline roll-up, got %q", status)
	}
	if health, _ := f.c.health.Get(testChamber, model.DeviceUpstreamGate); health.Status != monitor.HealthUnknown {
		t.Fatalf("never-seen gate must stay unknown, got %q", health.Status)
	}
	// Observation from before the outage remains visible, not overwritten.
	oldObservation, _ := f.c.observations.Get(testChamber, model.DeviceFillValve)
	if oldObservation.Sequence != 1 {
		t.Fatalf("last observation should be retained while offline")
	}
	f.now = f.now.Add(2 * time.Second)
	recovered := heartbeat(model.DeviceFillValve, 2, model.RunningOK, f.now.Add(-time.Second))
	result, err := f.c.Ingest(recovered)
	if err != nil || !result.Accepted {
		t.Fatalf("recovery heartbeat should be accepted, got %v", err)
	}
	if result.Health.Status != monitor.HealthOnline {
		t.Fatalf("expected back online, got %q", result.Health.Status)
	}
}

func TestFaultStatusStaysOnlineButCarriesMessage(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	result, err := f.c.Ingest(heartbeat(model.DeviceFillValve, 1, model.RunningFault, f.now.Add(-time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Health.Status != monitor.HealthOnline || result.Health.Message == "" {
		t.Fatalf("fault heartbeat is timely (online) but needs a message, got %+v", result.Health)
	}
}

func TestInvalidHeartbeat(t *testing.T) {
	f := newFixture(t, DefaultConfig())
	invalid := model.DeviceHeartbeat{ChamberID: testChamber, Device: "sluice", Status: model.RunningOK, ObservedAt: f.now}
	if _, err := f.c.Ingest(invalid); err == nil {
		t.Fatal("expected validation error for unknown device")
	}
}
