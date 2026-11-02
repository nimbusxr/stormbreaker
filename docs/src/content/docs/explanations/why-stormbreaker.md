---
title: Why stormbreaker
description: Why smoke and load tests need their own framework in a shared environment, and what stormbreaker does about it.
---

Some questions about a service can only be answered where it really runs, among the services it depends on and the ones that depend on it:

- Does it hold its load?
- Is it alive and wired after this rollout?
- What happens when a partner slows down?

Those environments are shared. Every team's services live there, with every team's data. A load test in a shared environment creates thousands of records, in every store the traffic reaches, belonging to every team downstream. And the records look real, because they must: synthetic data that announces itself takes paths production never does.

So the hard part of operational testing is not sending load. It is these:

- **Knowing what a run created**, across teams, when the data carries no marker.
- **Letting every team prepare** for a run before its traffic arrives, and clean up after it, in its own stores, with its own tools.
- **Never touching another team's data**, and never asking a team that takes no part to do anything.
- **Judging honestly**: open-model load, percentiles merged across regions, and windows long enough to mean something.
- **Keeping real partners out of it**: load stops at a mock, never at a carrier's sandbox, and everything else passes through it to the real one.

## What stormbreaker does

- **A run declares before it sends.** Binding a stream declares every identity its records will create, and the run's footprint collects them. Teams downstream derive what their own systems will create, and declare that too, in rounds, until nothing new comes up.
- **Every team answers for its own.** Each team's dispatcher prepares and cleans up what its systems created, and proves it. The orchestrator records every claim. A scenario passes only when every dispatcher has answered for what it claimed: removed and verified, or promised clean by a date.
- **Traffic waits at a gate.** The run's readiness gate opens only when every required dispatcher has seen everything the run declared. It fails closed.
- **The data is looks-real and recomputable.** Streams come from your fixture factories, with ordinary UUIDs derived per record. A run's records can be generated again, byte for byte, days later.
- **It is axx.** Suites are axx suites, so stormbreaker's steps sit beside axx's: a load run can check a database row or a mock's journal in the same scenario.

## What it is not

- **Not for production.** It is built for staging environments that are shared and long-lived.
- **Not a load generator for its own sake.** Rate and fidelity matter, but the point is a verdict a team can trust, in an environment it shares.
- **Not a mandate.** Everything is opt-in. A team that takes no part is never asked to do anything, and never finds a run's data in its stores without a way to know it.
