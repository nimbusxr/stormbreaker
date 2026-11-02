---
title: Clean up later
description: Find everything a run created, days after it ran, from its footprint and its streams, and clean up what could not be cleaned up then.
---

Cleanup usually happens during the run: the scenario waits for every dispatcher to complete. Some of it cannot happen then.

- **A store that cannot delete now** answers with a promise: a `clean-by` date. Examples are a compacted topic, a search index, or an aggregate rebuilt nightly.
- **A dispatcher that was unreachable** never cleaned up at all.
- **An abandoned run** was cleaned up by whoever was there.

All of it stays answerable.

## The footprint is the record

A run's identities are kept for 72 hours after it closes (`--footprint-retention`):

- who claimed which identity;
- where it is;
- how it was resolved: expected, removed and verified, or clean by a date.

After that the footprint answers `410 Gone`. The journal, which still counts them by place, is kept for 14 days (`--retention`), and the run's streams can generate its identities again.

Read it a page at a time:

```sh
curl -s "localhost:18080/runs/<run id>/footprint?after=0&reports=64"
```

The journal summarizes it:

- the dispatchers on the run, and the state each ended in;
- what happened, in order;
- what the run answers for, counted by place.

A `clean-by` entry is a promise to keep: by that date, the identities it names are gone.

## Recompute what a run created

A run's streams are recorded on it, with their seeds. Their records are a pure function of the definition, the seed and the run, so the identities can be generated again at any time, from the same definitions:

```sh
stormbreaker streams ids parcels --root data --run <run id>
```

That is the backstop when a record is lost: what a run created is recomputable, not merely recorded.

## Hard stores

Teams steward their own hard stores; these are common answers, not rules:

| Store | Usually |
|---|---|
| a topic | retention ages the records out: `clean-by` the retention's end |
| a compacted topic | a tombstone for each key, then `clean-by` the next compaction |
| a cache | eviction or a TTL: `clean-by` the TTL's end |
| a search index | delete by query, then `removed-verified` once a search finds nothing |
| an aggregate | rebuild it without the run, or declare it uncorrectable and `clean-by` the rebuild |
