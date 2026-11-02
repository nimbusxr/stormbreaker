# 0003: A run's footprint comes from declarations

- Status: Accepted
- Date: 2026-11-01

## Context

Test data must look real, so nothing in it says it is test data. Yet every record a run creates, in every team's stores, must be found and cleaned up, without any component touching another team's data.

## Decision

- **The suite declares first.** It declares every identity its streams will create, before any traffic.
- **Each dispatcher derives.** It declares what its own systems will derive from the footprint (a tracking record for a parcel), and may prepare for it.
- **Rounds run until one adds nothing.** Each declaration is a pure function of the footprint.
- **Dispatchers never derive across teams and never know the topology.** The cascade reaches downstream teams through the footprint alone.
- **The footprint is append-only.** It is a record of claims (who said which identity, where), merged when read.
- **An instance that repeats itself settles.** "Has this party said this here before" is one indexed read, on a digest of the report. The same statement again is recorded once. See [0013](0013-rounds-settle-on-a-footprint-version.md) for how the gate knows an instance has settled.

## Alternatives

A synthetic-marker header: a second channel that must be propagated through every hop, and that fails open. A proxy that observes the wire: no proxies are allowed.
