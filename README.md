# smartmeter-fetch

[![CI](https://github.com/welworx/smartmeter-fetch/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/welworx/smartmeter-fetch/actions/workflows/ci.yml)
[![CodeQL](https://github.com/welworx/smartmeter-fetch/actions/workflows/codeql.yml/badge.svg?branch=main)](https://github.com/welworx/smartmeter-fetch/actions/workflows/codeql.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/welworx/smartmeter-fetch)](go.mod)
[![License: GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue.svg)](LICENSE)

Logs into grid operator smart meter web portals (starting with
[Netz NÖ](https://smartmeter.netz-noe.at/)) and fetches quarter-hourly
consumption and production readings, stores them locally, and serves them
over a small versioned HTTP API for other tools to consume. Companion to
[hass-smartmeter](https://github.com/welworx/hass-smartmeter), a Home
Assistant integration that reads from that API — this tool only fetches and
stores, it doesn't know or care about Home Assistant.

> **Disclaimer:** Built for **personal, educational use only.** This is an
> independent, unofficial tool, not affiliated with, endorsed by, or
> supported by Netz NÖ or any other grid operator. It drives the operator's
> web portal using the same requests your browser makes, which may not be
> permitted under that portal's Terms of Service — check those before using
> it. Use is entirely at your own risk; see [Disclaimer](#disclaimer) below.

## Status

The EVN/Netz NÖ provider, the JSON file store, and a CLI to drive them
(`list-points`, `fetch`, `get`, `profile`) are implemented. `fetch`
persists readings to `-data-dir` without printing them by default (pass
`-json` for that). `get` ensures a date range is fetched, then reads it
back — as a table, JSON, or CSV, optionally aggregated (`-sample
hour|day|week|month|quarter`) and optionally exported to templated file(s)
via `-out`. A day already present in `-data-dir` is skipped rather than
re-fetched, unless `-force` is passed. The query API is not yet
implemented.

Each reading carries a `quality` code when the portal reports one: `L2`
(substitute value, final) or `L3` (substitute value, still provisional —
may change on a later fetch). Measured values leave `quality` empty for
now — the portal's own UI calls these `L1`, but that code isn't present
anywhere in the API response, only inferred from context, so it isn't
hardcoded; see `internal/provider/evn/evn.go`'s `dayRecord` comment.

```bash
export SMARTMETER_USER=you@example.com
export SMARTMETER_PASSWORD=hunter2

smartmeter-fetch list-points
smartmeter-fetch fetch -point <id> -day 2024-01-15
smartmeter-fetch fetch -point <id> -from 2024-01-01 -to 2024-01-31  # date range
smartmeter-fetch fetch -point <id> -from -30 -to -20                # or days before today; -to defaults to today
smartmeter-fetch fetch -since-latest                      # resume every point from its last stored day
smartmeter-fetch fetch -point <id> -day 2024-01-15 -log-level debug
smartmeter-fetch fetch -point <id> -day 2024-01-15 -verbose         # shorthand for -log-level debug
smartmeter-fetch get -point <id> -day 2024-01-15                          # read back a day (fetches first if needed)
smartmeter-fetch get -point <id> -from -365 -sample day -format csv       # a year of daily totals as CSV
smartmeter-fetch get -sample day -out "data/<profile>/<zaehlerpunkt_id>/<yyyy>.csv"  # export every point/year, no real IDs in paths
```

Credentials can also be stored once, encrypted under a master passphrase
(argon2id + AES-256-GCM, in `credentials.enc` under the OS config dir), via
`smartmeter-fetch profile add <name>` — verified live against the portal
before saving, and recheckable later with `smartmeter-fetch profile verify`.
See `smartmeter-fetch help` for the full `profile` command reference.

## Architecture

Two small interfaces are the whole design:

```go
type Provider interface {
    Name() string
    ListPoints(ctx context.Context) ([]Point, error)
    FetchDay(ctx context.Context, pointID string, day time.Time) ([]Reading, error)
}

type Store interface {
    Put(ctx context.Context, provider, pointID string, readings []Reading) error
    Get(ctx context.Context, provider, pointID string, since time.Time) ([]Reading, error)
    Latest(ctx context.Context, provider, pointID string) (day time.Time, found bool, err error)
    Has(ctx context.Context, provider, pointID string, day time.Time) (bool, error)
}
```

- `internal/provider/evn` — the first `Provider` implementation, for
  smartmeter.netz-noe.at. More grid operators can be added as siblings here
  without touching storage or the API.
- `internal/store/jsonfile` — the first `Store` implementation: one JSON
  file per provider/metering point/day (`data/<provider>/<point>/<date>.json`,
  default `-data-dir`), written atomically so a delayed/backfilled day never
  gets read half-written. Other backends (Postgres, MariaDB, SQLite) can be
  added later behind the same interface if a single user ever needs them —
  not built speculatively.
- `internal/api` — a versioned (`/v1`) HTTP query API. This is the only
  thing consumers like hass-smartmeter ever talk to; they never see which
  provider or storage backend is behind it.

### Query API (v1)

- `GET /v1/points` — discover known metering points across all configured
  providers.
- `GET /v1/readings?point=<id>&since=<RFC3339>` — cursor-based read, so
  consumers can resume from their own last-seen timestamp instead of
  re-fetching everything.

## Disclaimer

This project is not affiliated with, endorsed by, or supported by Netz NÖ,
EVN, or any other grid operator. It is provided "as is", without warranty
of any kind. Scraping a provider's web portal may violate its Terms of
Service — you are responsible for checking and complying with those terms.
Use at your own risk.
