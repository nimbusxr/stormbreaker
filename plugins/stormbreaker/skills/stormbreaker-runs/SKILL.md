---
name: stormbreaker-runs
description: Write stormbreaker smoke and load runs as axx scenarios (tags that open a run, streams over fixture factories, REST and Kafka load at a declared rate, Prometheus SLO checks). Use when adding operational acceptance tests that run against a shared environment.
license: Apache-2.0
---

# Writing stormbreaker runs

A stormbreaker run is an axx scenario that runs against a shared environment and answers for every record it creates. Its steps come from stormbreaker's packs, listed in `axx-packs.yaml` beside axx's own.

## Checklist

```
- [ ] axx-packs.yaml lists rest (and kafka), and stormbreaker's packs by module:
      github.com/nimbusxr/stormbreaker/packs/{core,rest,kafka,prometheus}@<version>
- [ ] axx.yaml: packs.stormbreaker-core.url (the orchestrator), token, fixtures (where streams are)
- [ ] A smoke check that creates nothing: a plain axx scenario, no tags
- [ ] A run: tag @stormbreaker-group/<group>, and @stormbreaker-requires/<dispatcher>,... for the
      dispatchers whose preparation the traffic must wait for
- [ ] Volume data: <name>.stream.yaml (count + weighted mix of <factory>#<fixture>); leave identities
      out of fixtures and give the factory's identity format: uuid-name-based
- [ ] stormbreaker streams lint <name> --root <fixtures dir>
- [ ] Bind, then send: "the <stream> stream of POST requests to /path" then
      "the <stream> stream is sent at <n> requests per second"
- [ ] Judge: "the load completes with no failed requests", the client's quantile, and the
      platform view: "within 30s the p99 of <histogram> over the run is at most <s> seconds"
      after "the run's traffic is complete"
- [ ] axx validate && axx lint && axx run --compact
```

## Rules

- **Never invent step text.** The step index is https://stormbreaker.nimbusxr.us/references/step-index/ (axx's at https://axx.nimbusxr.us/references/step-index/); `axx steps` lists the project's.
- **A value means what it says** (axx's rule, in every pack): `42` is an integer, `1.5` a decimal, `"42"` the string, and checks are strict. In a step's own text, the string is `'"42"'`. `axx validate` names the form to write.
- **Declare before sending.** Binding a stream declares its identities. Hand-written requests and events join a run with "the requests are identified by <json paths>" or "the <topic> kafka events are identified by the payload <path>".
- **Data looks real.** Don't put "test" or a run id in identities. Derived UUIDs are the default.
- **Open model.** A rate is arrivals per second, not concurrency. Don't loop requests yourself.
- **One run at a time per group.** A run holds every dispatcher in its group until it closes; a scenario whose group is held waits its turn (`packs.stormbreaker-core.turn`, 30m). Don't split one test into parallel runs on the same group, and give unrelated teams groups of their own.
- **Point a Prometheus service at where the metrics are.** Its `url` lists the servers whose series are summed: the app's own Prometheus in each region or zone, each region's shared one, or one global view (Thanos, Mimir). Separate replicas of an HA pair with `\|` so they count once, and add a `selector` (`app="parcels"`) whenever a server holds other apps' series.
- **The verdict includes cleanup.** A scenario passes only when every dispatcher cleaned up. A failure at the readiness gate names the dispatcher it waited on: read the attached journal.
