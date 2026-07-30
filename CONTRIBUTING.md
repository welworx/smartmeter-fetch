# Contributing

This is a personal, educational-use project (see the [README
disclaimer](README.md#disclaimer)), but fixes and improvements are welcome.

## Before opening a PR

1. Tests pass: `go test -race ./...`
2. Code is formatted: `gofmt -l .` is empty
3. Linter passes: `golangci-lint run`

`.pre-commit-config.yaml` runs all three on commit if you want them enforced
automatically (`pip install pre-commit && pre-commit install`).

## Scope

Changes to provider scraping (`internal/provider/*`) are the most fragile
part of this codebase — grid operator portals aren't stable APIs, and any
pacing/retry workarounds are deliberate. If you're changing behavior there,
explain what portal behavior you observed and why the change is needed.

The `Provider` and `Store` interfaces (`internal/provider/provider.go`,
`internal/store/store.go`) and the `/v1` HTTP API are the contracts other
tools (notably [hass-smartmeter](https://github.com/welworx/hass-smartmeter))
depend on — changes there need a version bump or a backward-compatible path,
not a silent breaking change.

## Reporting bugs / requesting features

Open a GitHub issue. For security issues, see [SECURITY.md](SECURITY.md)
instead of filing a public issue.
