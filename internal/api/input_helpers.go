package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func chiURLParam(r *http.Request, name string) string { return chi.URLParam(r, name) }

func parseUUID(value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, errors.New("uuid value is required")
	}
	return uuid.Parse(value)
}

func durationSeconds(value int) time.Duration {
	if value <= 0 {
		value = 30
	}
	if value > 3600 {
		value = 3600
	}
	return time.Duration(value) * time.Second
}

// parseFlexibleTime accepts RFC3339 timestamps (with or without fractional
// seconds) as field devices commonly emit them.
func parseFlexibleTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("observed_at is required")
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("observed_at must be an RFC3339 timestamp")
}
