package api

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/wyw14/cry-96/internal/monitor"
)

func pageHandler(webDirectory, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(webDirectory, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			writeError(w, http.StatusNotFound, os.ErrNotExist)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		http.ServeFile(w, r, path)
	}
}

func healthHandler(runtime *Runtime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httpStatus := http.StatusOK
		overall := "ok"
		for _, chamberID := range runtime.Chambers {
			switch runtime.Health.ChamberStatus(chamberID) {
			case monitor.HealthOffline:
				httpStatus = http.StatusServiceUnavailable
				overall = "offline"
			case monitor.HealthUnknown:
				if httpStatus == http.StatusOK {
					httpStatus = http.StatusServiceUnavailable
				}
				if overall == "ok" {
					overall = "unknown"
				}
			}
		}
		writeJSON(w, httpStatus, map[string]any{
			"status": overall, "request_id": requestID(r.Context()),
			"devices": runtime.Health.List(), "time": runtime.Now().UTC(),
		})
	}
}
