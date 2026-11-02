---
title: Drive Kafka load
description: Publish a stream's records as whole Kafka records through a real topic at a declared rate, serialized as axx's publish steps serialize them.
---

stormbreaker-kafka publishes a stream's records as whole Kafka records (key, value, headers), through a Kafka service that axx's kafka pack registered:

```gherkin
Given the events kafka service with the following properties:
  | brokers | ${sys:kafka.brokers} |
And the scans stream of depot-scans kafka events
And the scans stream kafka event keys are the key metadata
And the scans stream kafka event headers are the headers metadata
When the scans stream is published at 200 events per second
Then the publish completes with no failed events
```

## Open model

Event *i* is due at *i*/rate seconds, whatever the events before it are doing, as [REST load](/guides/drive-rest-load/#open-model) is. The first event is sent, and waited for, before the clock starts: the producer connects to the partitions' leaders, and an Avro serializer registers its schema, before the rate applies.

Two guardrails stop a publish that is no longer the one declared:

- **The cluster is not keeping up.** When more than `maxInFlight` events (default 50,000) are in flight, the publish stops and fails.
- **The driver is not keeping up.** When events leave more than `maxDrift` (default 1s) after their due time, for five seconds in a row, the publish stops and fails. One process publishes 200,000 JSON events a second, or 150,000 Avro ([scale](/explanations/scale/)). `maxDrift: off` turns this guardrail off.

```yaml title="axx.yaml"
packs:
  stormbreaker-kafka:
    maxInFlight: 100000
    maxDrift: 2s
```

## Serialization

Events are serialized as axx's publish steps serialize them, by the topic client's settings:

- **An Avro factory's records** are Avro, under the subject the producer's strategy names, with the ID its Schema Registry gives, in the Confluent wire format.
- **Any other factory's records** are JSON text. A JSON factory with a JSON Schema publishes JSON: the family decides, not whether a schema is declared.
- **An explicit schema** publishes as Avro whatever the factory: `the scans stream is published using schema schemas/depot-scan.avsc at 200 events per second`.

Keys are text: a scalar metadata entry, or by default the record's first identity. Avro record keys are not supported yet.

## Let downstream drain

What consumes the topic does so in its own time. Before the scenario ends, and so before cleanup starts, wait for it to catch up:

```gherkin
Then the tracking consumer group catches up on the depot-scans topic within 60s
```

A consumer group's lag is the topic's end offsets less its committed ones. The step waits until that lag is zero on every partition, and fails if it is not zero in time. Without it, a cleanup can run before the last downstream records are written, and miss them.

## Events drafted by hand

Events drafted with axx's kafka steps can join a run: name where their identities are, and they are declared before each is published.

```gherkin
Given the depot-scans kafka events are identified by the payload $.scanId
Given the depot-scans kafka events are identified by the header trace-id
```
