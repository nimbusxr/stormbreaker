---
title: Smoke after a rollout
description: Check that a deployed service is alive and wired after every rollout, with a plain axx scenario and no run at all.
---

A smoke test asks one thing: is the service, as deployed, alive and wired? It is a small acceptance scenario, run against the environment after each rollout. It needs nothing of stormbreaker's coordination when it creates nothing:

```gherkin title="features/smoke/parcels-answers.feature"
Feature: The parcels service answers

  Scenario: The parcels service is up
    Given the parcels service with the following properties:
      | url | ${env:PARCELS_URL} |
    And a GET request to /ping
    When the request is executed
    Then the response status code is 200
```

Without stormbreaker's tags it opens no run: it runs in a bubble, and nobody can tell it ran.

## When a smoke writes

A smoke test that writes (it registers a parcel, say) creates data in a shared environment. Tag it, and say where its identities are, and it is declared and cleaned up like any run:

```gherkin nocheck
@stormbreaker-group/delivery
Scenario: A parcel is registered
  Given the parcels service with the following properties:
    | url | ${env:PARCELS_URL} |
  And the requests are identified by $.reference
  And a POST request to /parcels
  ...
```

## Practice

- **The verdict names what it judged.** Say the environment, the region and the version in the report: tag scenarios, or pass them as properties.
- **Block or warn: the team decides.** Whether a red smoke stops a rollout is the team's call, in its pipeline.
- **Run it on a schedule too.** The same scenario, run every few minutes, is a continuous probe.
- **Keep it small.** A smoke checks that the service is alive and wired, not every behavior: functional acceptance belongs where feedback is fastest, before deployment.
