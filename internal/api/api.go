// Package api implements the versioned (/v1) HTTP query API that
// consumers such as hass-smartmeter use to read readings without knowing
// which provider or store backend produced them:
//
//	GET /v1/points
//	GET /v1/readings?point=<id>&since=<RFC3339>
//	GET /openapi.json
//
// No authentication — this matches the rest of smartmeter-fetch's
// single-user/LAN trust model; put an auth layer (e.g. a reverse proxy)
// in front before exposing it beyond a trusted network.
package api

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
	"github.com/welworx/smartmeter-fetch/internal/store"
)

//go:embed openapi.json
var openAPISpec []byte

// NewHandler returns the /v1 API's http.Handler, backed by st.
func NewHandler(st store.Store, log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/points", handlePoints(st, log))
	mux.HandleFunc("GET /v1/readings", handleReadings(st, log))
	mux.HandleFunc("GET /openapi.json", handleOpenAPI)
	return mux
}

func handleOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(openAPISpec); err != nil {
		return
	}
}

func handlePoints(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		points, err := st.ListPoints(r.Context())
		if err != nil {
			log.Error("listing points failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if points == nil {
			points = []store.PointRef{}
		}
		writeJSON(w, points)
	}
}

func handleReadings(st store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pointID := r.URL.Query().Get("point")
		if pointID == "" {
			http.Error(w, "missing required query parameter: point", http.StatusBadRequest)
			return
		}

		since := time.Time{}
		if s := r.URL.Query().Get("since"); s != "" {
			parsed, err := time.Parse(time.RFC3339, s)
			if err != nil {
				http.Error(w, "invalid since (want RFC3339): "+err.Error(), http.StatusBadRequest)
				return
			}
			since = parsed
		}

		points, err := st.ListPoints(r.Context())
		if err != nil {
			log.Error("listing points failed", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		providerName := ""
		found := false
		for _, p := range points {
			if p.ID == pointID {
				providerName = p.Provider
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "unknown point", http.StatusNotFound)
			return
		}

		readings, err := st.Get(r.Context(), providerName, pointID, since)
		if err != nil {
			log.Error("getting readings failed", "provider", providerName, "point", pointID, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if readings == nil {
			readings = []provider.Reading{}
		}
		writeJSON(w, readings)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	if err := enc.Encode(v); err != nil {
		return
	}
}
