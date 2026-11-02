---
title: Configuration
description: Every setting stormbreaker reads, from axx.yaml, the orchestrator's flags and the dispatcher's environment.
---

## In axx.yaml

The packs' settings are under `packs`, each under its name. The packs' pages give each setting:

- [stormbreaker-core](/references/packs/stormbreaker-core/): the orchestrator's `url` and `token`, how long to wait for the run's turn, at the gate and for cleanup, and where the fixture factories and streams are.
- [stormbreaker-rest](/references/packs/stormbreaker-rest/): how requests reach the service, their timeout, and the guardrails that stop a load the service or the driver cannot keep up with (`maxInFlight`, `maxDrift`).
- [stormbreaker-kafka](/references/packs/stormbreaker-kafka/): the guardrails that stop a publish the cluster or the driver cannot keep up with (`maxInFlight`, `maxDrift`).

Every string value can use axx's `${env:...}` and `${sys:...}`. The integer settings (`lintLimit`, `maxConnections`, `maxInFlight`) take plain numbers.

## Tags

| Tag | What it does |
|---|---|
| `@stormbreaker-group/<group>` | opens a run for the group before the scenario's first step |
| `@stormbreaker-requires/<dispatcher>,...` | the dispatchers the run's readiness gate waits for |

## The orchestrator

`stormbreaker orchestrator serve` reads flags, or their `STORMBREAKER_` variables: see [Deploy the orchestrator](/guides/deploy-the-orchestrator/#settings).

## A dispatcher

A dispatcher built with the dispatcher-sdk, and the stock image, read `STORMBREAKER_DISPATCHER`, `STORMBREAKER_INSTANCE`, `STORMBREAKER_TEAM`, `STORMBREAKER_CALLBACK_URL`, `STORMBREAKER_ORCHESTRATOR`, `STORMBREAKER_TOKEN`, `STORMBREAKER_GROUPS`, `STORMBREAKER_SECRET`, `STORMBREAKER_ADDR` and `STORMBREAKER_REASSERT`: see [Write a dispatcher](/guides/write-a-dispatcher/#run-it).

## A stream

`<name>.stream.yaml`, under the fixtures directory:

| Key | What it is |
|---|---|
| `count` | how many keys the stream generates |
| `mix` | the shapes keys take, each with a `weight` |
| `mix[].fixture` | the one fixture a key of this shape carries, `<factory>#<fixture>` |
| `mix[].series` | the fixtures a key of this shape carries, in order |
| `mix[].set` | values set on each message of this shape, by dotted path, before anything resolves |
