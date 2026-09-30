package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wyw14/cry-96/internal/model"
	"github.com/wyw14/cry-96/internal/telemetry"
)

type heartbeatItem struct {
	ChamberID  string              `json:"chamber_id"`
	Device     model.DeviceKind    `json:"device"`
	Sequence   uint64              `json:"sequence"`
	Status     model.DeviceRunning `json:"status"`
	Position   model.GatePosition  `json:"position,omitempty"`
	OpeningPct *float64            `json:"opening_pct,omitempty"`
	ObservedAt string              `json:"observed_at"`
}

type heartbeatResponseItem struct {
	StatusCode int    `json:"-"`
	Accepted   bool   `json:"accepted"`
	Reason     string `json:"reason,omitempty"`
	Error      string `json:"error,omitempty"`
	telemetry.IngestResult
}

func decodeHeartbeatBody(r *http.Request) ([]heartbeatItem, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return nil, errors.New("request body is required")
	}
	var single heartbeatItem
	if err := json.Unmarshal(body, &single); err == nil && single.ChamberID != "" {
		return []heartbeatItem{single}, nil
	}
	var batch []heartbeatItem
	if err := json.Unmarshal(body, &batch); err != nil {
		return nil, errors.New("body must be a heartbeat object or an array of heartbeats")
	}
	if len(batch) == 0 {
		return nil, errors.New("at least one heartbeat is required")
	}
	if len(batch) > 256 {
		return nil, errors.New("at most 256 heartbeats per request")
	}
	return batch, nil
}

func heartbeatStatusCode(err error) int {
	switch {
	case errors.Is(err, telemetry.ErrStale), errors.Is(err, telemetry.ErrDuplicate):
		return http.StatusConflict
	case errors.Is(err, telemetry.ErrFuture):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusBadRequest
	}
}

func telemetryHeartbeatHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := decodeHeartbeatBody(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		responses := make([]heartbeatResponseItem, 0, len(items))
		anyRejected := false
		for _, item := range items {
			observedAt, parseErr := parseFlexibleTime(item.ObservedAt)
			entry := heartbeatResponseItem{}
			if parseErr == nil {
				heartbeat := model.DeviceHeartbeat{
					ChamberID: item.ChamberID, Device: item.Device, Sequence: item.Sequence,
					Status: item.Status, Position: item.Position, OpeningPct: item.OpeningPct,
					ObservedAt: observedAt,
				}
				entry.IngestResult, parseErr = runtime.Telemetry.Ingest(heartbeat)
			}
			if parseErr != nil {
				entry.Accepted = false
				if entry.IngestResult.Reason == "" {
					entry.Reason = "invalid"
				} else {
					entry.Reason = entry.IngestResult.Reason
				}
				entry.Error = parseErr.Error()
				entry.StatusCode = heartbeatStatusCode(parseErr)
				anyRejected = true
			} else {
				entry.Accepted = entry.IngestResult.Accepted
				entry.Reason = entry.IngestResult.Reason
				entry.ChamberID = item.ChamberID
				entry.Device = item.Device
			}
			responses = append(responses, entry)
		}
		if len(responses) == 1 {
			status := http.StatusOK
			if !responses[0].Accepted {
				status = responses[0].StatusCode
			}
			writeJSON(w, status, responses[0])
			return
		}
		status := http.StatusOK
		if anyRejected {
			status = http.StatusMultiStatus
		}
		writeJSON(w, status, responses)
	}
}

func telemetryDevicesHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, runtime.Telemetry.Devices())
	}
}
