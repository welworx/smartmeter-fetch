# CLAUDE.md

This file provides guidance to Claude Code when working in this repository.

## What this is

Fetches quarter-hourly smart meter readings (consumption + production) from
grid operator web portals — starting with
[Netz NÖ](https://smartmeter.netz-noe.at/) — stores them locally, and serves
them over a small versioned HTTP API. Companion to
[hass-smartmeter](https://github.com/welworx/hass-smartmeter), which is the
*only* intended consumer of that API right now; this repo has no knowledge
of Home Assistant and should stay that way.

## Architecture

- `internal/provider/` — one `Provider` implementation per grid operator
  portal (`evn/` first). Adding a new operator means adding a new package
  here, never touching storage or the API.
- `internal/store/` — one `Store` implementation per backend (`jsonfile/`
  first: `data/<provider>/<point>/<date>.json`, atomic write via temp file +
  rename). Additional backends (Postgres, MariaDB, SQLite) are deliberately
  not built until a real need shows up — see `README.md#status`.
- `internal/api/` — the versioned (`/v1`) HTTP query API. This is the
  cross-repo contract with hass-smartmeter: `GET /v1/points` and
  `GET /v1/readings?point=<id>&since=<RFC3339>`. Treat changes here as a
  breaking-change surface, not an internal implementation detail.
- `internal/config/` — the encrypted credential profile store
  (`credentials.enc`: argon2id-derived-key AES-256-GCM, atomic write). A
  `Profile` is `{Name, Provider, Username, Password}`; driven by the CLI's
  `profile` command (`main.go`, `cli_profile.go`).

## Critical constraint: delayed data

The upstream portal can take several days to publish a day's readings.
`Store.Put` must be safe to call again for a day that's already been
written (a "day" file gets *replaced*, not appended to) and callers should
never assume "yesterday" is complete — always resume reads from the last
successfully consumed timestamp, not from a fixed offset from "today".

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .              # must be empty
golangci-lint run
go test -race ./...
```

## Disclaimer

Personal, educational-use tool. Not affiliated with or endorsed by any grid
operator. Portal scraping may not be permitted under a given portal's Terms
of Service — check before using. See `README.md#disclaimer`.
