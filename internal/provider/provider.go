// Package provider defines the interface implemented by each grid operator
// integration (internal/provider/evn, and future operators as siblings).
package provider

import (
	"context"
	"time"
)

// Point is a single metering point ("Zählpunkt") as reported by a provider.
// A prosumer account typically has one Point per direction (consumption and
// production/feed-in each get their own Point).
type Point struct {
	ID   string
	Name string
}

// Reading is a single interval's energy value.
type Reading struct {
	// Timestamp is the start of the interval, in UTC.
	Timestamp time.Time
	ValueWh   float64
}

// Unit is the unit of measurement of every Reading.ValueWh, for every
// provider.
const Unit = "Wh"

// Provider fetches readings from one grid operator's web portal.
type Provider interface {
	// Name identifies this provider, e.g. "evn".
	Name() string

	// ListPoints returns the metering points visible to this account.
	ListPoints(ctx context.Context) ([]Point, error)

	// FetchDay returns the readings for one metering point on one day.
	// The portal may only have partial or no data yet for recent days —
	// callers should not assume "yesterday" is complete.
	FetchDay(ctx context.Context, pointID string, day time.Time) ([]Reading, error)
}
