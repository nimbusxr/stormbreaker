---
title: Roadmap
description: "What is open: what a closed run left, draining, a store beyond SQLite, dispatchers in other languages, runs larger than one runner, and more packs."
---

Stormbreaker is in beta. These are the open problems, roughly in order.

## Cleanup that holds when something fails

- **What a closed run left, in its view.** A run's view names each unreachable instance, and a run closed at the cleanup deadline is marked `unfinished`. A run that closes with unreachable instances is not marked: its view should say so and count the claims never answered for, so that an abandoned run, which has no suite to fail, can be alerted on.
- **Draining before settled.** The suite can wait for a consumer group to catch up, but publishing `settled` only once drain conditions hold is still to come. Until then, a consumer down longer than its dispatcher's grace can write records after cleanup.

## Operating it

- **A store beyond one SQLite file.** One writer serves an enterprise's environment with room to spare ([scale](/explanations/scale/)). More than that in one environment, or an orchestrator that must survive losing its node without waiting for its volume, needs a store on Postgres, in its own ADR.
- **Registrations and groups that end.** A long-lapsed registration stays in the registry, and a group cannot be deleted.

## Larger and further

- **Dispatchers in other languages**, for teams that write cleanup in Java, Python or a script, held to a conformance kit that any dispatcher must pass.
- **Runs larger than one runner.** Streams already generate any range of keys, and ranges reassemble into the whole. Still missing: whatever decides the shards, starts them and gathers one verdict.
- **Distinct identities at large scale.** A cleanup that must count distinct identities, such as one waiting for every declared record to appear, holds them in memory today.
- **Guardrails on errors and latency.** A load already stops when its target or its driver falls behind. Stopping on an error rate or a latency is still to come, as is a rate check before a run starts.
- **Mocks primed per run**, with their stubs in the footprint.
- **Avro record keys** for stormbreaker-kafka.

## More packs

Traffic and judgment are packs, built on stormbreaker-core and its public `arrival`, `slo` and `streams` packages ([write a pack](/guides/write-a-pack/)). Likely next:

- **Metrics vendors:** Datadog, CloudWatch and New Relic.
- **Traffic protocols:** gRPC, AMQP and NATS.
