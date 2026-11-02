# 0002: Coordinate runs through an orchestrator per environment

- Status: Accepted
- Date: 2026-11-01

## Context

A run must tell every team that takes part what is happening (opened, the footprint grew, traffic done, cleanup), and know when each has prepared and cleaned up. Environments are shared, long-lived and always concurrent, and teams own their namespaces.

## Decision

One small service per environment, the orchestrator, coordinates runs.

- **What it keeps.** It holds the registry of dispatchers, each run's readiness gate, footprint and journal.
- **How it tells.** It sends signed webhooks to every live instance of every dispatcher in a run's group, retried until they arrive. An event feed is the backstop.
- **It never blocks traffic itself.** The gate is a fact the suite reads.

## Alternatives

NATS, a Kafka topic, cloud pub/sub, Valkey streams, CloudEvents with Knative, a shared Postgres ledger, OpenTelemetry signals, or the suite orchestrating teams directly. Each was rejected for one of these reasons:

- it was too much to run per environment;
- it pushed offset mechanics onto teams;
- it removed discovery.

Every team already knows how to run a webhook receiver.
