# 0005: Load is driven open-model from the suite

- Status: Accepted
- Date: 2026-11-01

## Context

Load must arrive at honest rates over HTTP and Kafka, from where the suite runs, and leave a client's view to judge.

## Decision

Load is driven inside the suite, on the CI runner, by an open-model scheduler: arrival *i* is due at *i*/rate seconds, computed from *i*, so drift never accumulates.

- **Requests are built as they come due**, a bounded look-ahead ahead of the schedule.
- **Each send runs on its own**, with its own timeout.
- **The client's latencies, failures and drift are recorded** as the client view.

The scheduler holds 100,000 arrivals a second, with a p99 drift in tens of microseconds.

## Alternatives

Gatling, k6 and JMeter, as subprocesses with toolchains of their own, outside the scenario that declares and judges the run.
