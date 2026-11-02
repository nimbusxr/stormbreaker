---
title: Testing philosophy
description: Operational acceptance is a sibling of functional acceptance, not the next rung up; it is judged where only real infrastructure can answer.
---

## A sibling, not the next rung

Unit, integration and functional acceptance tests judge behavior: does the system do what it should? Operational acceptance judges something else: does the deployed system hold up, against SLAs and SLOs? It has two members:

- **Smoke tests.** The deployed environment is alive and wired.
- **Load and performance tests.** It holds its capacity, latency and reliability under volume.

It is not the next rung above functional acceptance. It is a sibling, with criteria of its own.

## Judge each where it can be judged

Functional criteria are judged where feedback is fastest: early, against a system started for the test, with its dependencies mocked. Operational criteria can only be judged on real infrastructure, and that is the only reason to test in a shared environment. Running the functional suite in staging adds latency, not information.

## Teams decide

Everything is opt-in. Nothing is mandated or owned centrally:

- **Taking part.** A team takes part in runs with a dispatcher, or does not.
- **A red verdict.** Whether it blocks a rollout or warns is the team's choice.
- **Ownership.** Teams own what they run, and what their systems create.

## Practice

The method is taught, not shipped:

- **Smokes are minimal acceptance scenarios**, with no volume data. Read-only and write-through smokes are both fine. A smoke run on a schedule is a continuous probe.
- **Verdicts name what they judged**: the environment, the region, the version.
- **Reference data is ordinary test authoring**, not a special mechanism.
- **Prove it locally first, then with one pilot team**, before asking anyone else to take part.
