# 0012: Failed and abandoned runs are cleaned up like any other

- Status: Accepted
- Date: 2026-11-01

## Context

A run can fail at its readiness gate after some dispatchers have prepared, or its suite can go away mid-traffic. Either way, data was written.

## Decision

- **A run that fails at its gate, or is abandoned, goes to cleanup at once.** `failed` and `abandoned` say why.
- **Such a run can close**, like any other, once every instance has completed or is unreachable.
- **A failing scenario gives its run up straight away** (`run.abandoned`), rather than waiting for its lease to expire.
- **An instance whose lease lapsed is unreachable**, so an instance that scaled away holds no run up.
