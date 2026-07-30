// Package store defines the interface implemented by each storage backend
// (internal/store/jsonfile, and future backends as siblings).
package store

import (
	"context"
	"time"

	"github.com/welworx/smartmeter-fetch/internal/provider"
)

// Store persists and retrieves readings, keyed by provider name and
// metering point ID.
type Store interface {
	// Put writes readings for one provider/point. Implementations must be
	// safe to call again for a day that was already written — upstream
	// portals can publish data late, and a later call with the same
	// timestamps must replace, not duplicate, the earlier values.
	Put(ctx context.Context, providerName, pointID string, readings []provider.Reading) error

	// Get returns all readings for one provider/point with a timestamp at
	// or after since, ordered by timestamp ascending. Callers resume from
	// the last timestamp they successfully consumed, not from a fixed
	// offset from "now".
	Get(ctx context.Context, providerName, pointID string, since time.Time) ([]provider.Reading, error)
}
