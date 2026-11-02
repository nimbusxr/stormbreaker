# 0001: Build in Go, on axx

- Status: Accepted
- Date: 2026-11-01

## Context

Operational acceptance tests (smoke and load) are acceptance scenarios with more at stake: they run in a shared environment and answer for the data they create. They need what an acceptance framework already gives: scenarios, services, fixture factories, mocks and reports. axx is an acceptance testing framework in Go, and its custom steps are Go packs (axx ADR 0009), so what operational acceptance adds can be packs beside axx's own.

## Decision

- **Stormbreaker is Go**, one module, `CGO_ENABLED=0`, set up as axx is (mise, golangci-lint, goreleaser, release-please, Starlight docs).
- **Its suites are axx suites, and its steps are axx packs.** Projects list stormbreaker-core, stormbreaker-rest, stormbreaker-kafka and stormbreaker-prometheus in `axx-packs.yaml` by module. They build only on axx's public packages: what a pack needs that axx keeps internal is a change to axx.
- **Its services are one binary.** `stormbreaker orchestrator serve`, `stormbreaker dispatcher serve`, and `stormbreaker streams`, shipped as images where they run in a cluster.

## Consequences

- One scenario can mix stormbreaker's steps with axx's: a load run can check a database row or a mock's journal.
- Stormbreaker follows axx's releases. The extension points it needed were added to axx: a public fixtures package, the kafka pack's producer and serializer, and the matched definition of a step.
