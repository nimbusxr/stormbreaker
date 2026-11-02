---
title: Define streams
description: Declare a run's volume data as a stream over axx's fixture factories, deterministic, looks-real, and recomputable forever.
---

A stream is a run's volume data, declared as data. It has two parts:

- **a count of keys**;
- **the weighted mix of shapes those keys take.**

A shape is a fixture, or a series of fixtures that one key carries in order (registered, then booked for collection). The fixtures are [axx's fixture factories](https://axx.nimbusxr.us/guides/fixture-factories/). The factory owns what each message is; the stream owns only volume and order.

```yaml title="data/parcel-lifecycle.stream.yaml"
count: 10000
mix:
  - weight: 8
    series:
      - parcel#standard
      - parcel-collection#booked
  - weight: 2
    fixture: parcel#express
    set:
      recipient.country: FR
```

`set` overrides values of every message of that shape, at dotted paths, before anything resolves.

## Nothing is random

Record *i* is a pure function of the definition, the seed and the run. The run's id is the seed.

- **A key's shape** comes from a hash of the stream's name, the seed, the run and the key.
- **Every value** comes from the factory's own expressions, evaluated for that record.
- **The same everywhere.** The same stream, generated in another process or next month, gives the same records byte for byte.

That is what makes cleanup recomputable rather than recorded. It is also why any range of keys can be generated on its own: ranges reassemble exactly into the whole stream.

## Identities

Leave identities out of the fixtures, and every record derives its own from what it is:

```yaml title="data/parcel.factory.yaml"
factory:
  family: json
identity:
  - path: reference
    format: uuid-name-based
```

The result is an ordinary UUID. Nothing marks it as test data, so the services treat it as they treat any other, and only the run knows it. Records of one key can share an identity through a `$ref` to an earlier message's document. The streams of one run agree record for record, so a key stream and a value stream line up without referring to each other.

## Metadata

A fixture's `metadata` travels with its record: a Kafka key and headers, a request's path variables, query and headers. A pathless `$ref` in metadata names the record's own finished data:

```yaml title="data/parcel.prototype.yaml"
metadata:
  path:
    reference:
      $ref: "#/reference"
```

## Check a stream

```sh
stormbreaker streams lint parcel-lifecycle --root data          # generates, and checks no two keys share an identity
stormbreaker streams generate parcel-lifecycle --root data --run run-1 --to 3
stormbreaker streams ids parcel-lifecycle --root data --run run-1
```

Binding a stream in a scenario lints it too, for streams up to `lintLimit` keys (a million by default), and declares every identity before any traffic.

## Expression functions

Pure expression functions that a team's own Go pack registers, with `streams.Register`, are available to every stream. A recorded function answers only from the committed pairings lock. Prefer derived identities: they need no function, so `stormbreaker streams` can always recompute them.
