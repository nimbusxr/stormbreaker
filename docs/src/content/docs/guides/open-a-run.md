---
title: Open a run
description: Tag a scenario to open a run for a group, name the dispatchers its readiness gate waits for, and let the run go through cleanup when it ends.
---

A run is one scenario's coordinated test. You tag the scenario and stormbreaker-core does the rest:

1. It opens the run before the first step.
2. It holds the traffic at the run's readiness gate.
3. When the scenario ends, it closes the run through cleanup.

```gherkin
@stormbreaker-group/delivery @stormbreaker-requires/parcels,tracking
Scenario: Every parcel registered is tracked
```

- **`@stormbreaker-group/<group>`** opens a run for a group. Every dispatcher in the group is notified, and takes part.
- **`@stormbreaker-requires/<dispatcher>,...`** names the dispatchers whose every instance must be prepared before any traffic flows. A run that requires a dispatcher with no live instance is refused. The others take part and clean up, but never hold the gate.

A scenario without these tags opens no run. That is right for a smoke check that creates nothing.

## One run at a time

A run holds every dispatcher in its group until it closes. While it does, the orchestrator refuses any other run in that group, or in a group that shares one of its dispatchers. Load tests on the same services never overlap, so neither's measurements carry the other's load.

A scenario whose group is held waits its turn: stormbreaker-core tries again until the run holding the group closes, for up to `turn` (30 minutes), and its log names the run it is waiting for. A cleanup that runs past the orchestrator's cleanup hold (15 minutes) stops holding, and goes on cleaning up beside the next run.

## The readiness gate

Before every step that sends traffic, the gate waits until every required instance has prepared for everything the run has declared. Steps that only describe something run straight away. The gate:

- **fails the step** if a required dispatcher reports itself blocked, goes silent or unreachable, or misses the readiness timeout;
- **opens again when it has to**: a run that declares more (a second stream, a request written by hand) waits again before its next traffic.

Wait at it explicitly with `Given the run is ready`.

## When the scenario ends

- **A passing scenario's run** is told that its traffic is done (which closes its window, unless `the run's traffic is complete` closed it earlier), that it has settled, and that cleanup can begin. The scenario then waits for the run to close, and **passes only when every dispatcher completed its cleanup**.
- **A failing scenario's run** is abandoned, and cleaned up all the same.

Either way, the run's journal is attached to the scenario's report.

## Settings

```yaml title="axx.yaml"
packs:
  stormbreaker-core:
    url: ${env:STORMBREAKER_ORCHESTRATOR_URL}
    token: ${env:STORMBREAKER_TOKEN}
    readiness: 120s   # how long to wait at the gate
    close: 180s       # how long to wait for cleanup
    turn: 30m         # how long to wait while another run holds the group
    fixtures: data    # where the fixture factories and streams are
```
