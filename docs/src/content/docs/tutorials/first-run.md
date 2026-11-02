---
title: Your first run
description: Point stormbreaker at your own service, with a stream of your own data, a dispatcher, and a load scenario.
---

This tutorial takes a service of yours from no load test at all to a load run that cleans up after itself. It assumes an orchestrator is running in the environment ([deploy one](/guides/deploy-the-orchestrator/), or use the lab's on `localhost:18080`).

## 1. Add the packs

In your acceptance suite's `axx-packs.yaml`:

```yaml title="axx-packs.yaml"
packs:
  - rest
  - github.com/nimbusxr/stormbreaker/packs/core@v0.1.0
  - github.com/nimbusxr/stormbreaker/packs/rest@v0.1.0
```

And tell stormbreaker-core where the orchestrator is, in `axx.yaml`:

```yaml title="axx.yaml"
packs:
  stormbreaker-core:
    url: ${env:STORMBREAKER_ORCHESTRATOR_URL}
    token: ${env:STORMBREAKER_TOKEN}
```

## 2. Describe the data

A stream is built on axx's [fixture factories](https://axx.nimbusxr.us/guides/fixture-factories/). Leave each record's identity out of its fixtures: every record then derives one of its own, as an ordinary UUID.

```yaml title="data/pickup.factory.yaml"
factory:
  family: json
identity:
  - path: pickupId
    format: uuid-name-based
```

```yaml title="data/standard.fixture.yaml"
factory: pickup
data:
  depot: DE-BER-1
  parcels: 1
```

```yaml title="data/pickups.stream.yaml"
count: 1000
mix:
  - fixture: pickup#standard
    weight: 1
```

## 3. Take part with a dispatcher

Your team's dispatcher prepares for what a run declares, and cleans up after it. With the [dispatcher-sdk](/guides/write-a-dispatcher/) it is two functions. Deploy it beside your service, in the run's group.

## 4. Write the scenario

```gherkin title="features/pickups-load.feature"
@stormbreaker-group/delivery @stormbreaker-requires/pickups
Feature: Pickups hold their load

  Scenario: 1000 pickups at 100 a second
    Given the pickups service with the following properties:
      | url | ${env:PICKUPS_URL} |
    And the pickups stream of POST requests to /pickups
    When the pickups stream is sent at 100 requests per second
    Then the load completes with no failed requests
    And the client's p99 latency of the load is at most 0.5 seconds
```

Run it with `axx run`. The run opens, declares the thousand pickups, and waits at its gate until your dispatcher has prepared. It sends them, and then waits for your dispatcher to clean up. If your dispatcher fails to clean up, the scenario fails.

## Next

- [Check SLOs](/guides/check-slos/) from your service's own metrics, across regions.
- [Mock partners](/guides/mock-partners/) your service calls that nobody in the environment owns.
