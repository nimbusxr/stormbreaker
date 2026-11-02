---
title: Drive REST load
description: Send a stream's records as HTTP requests through a service's real front door, open-model, at a declared rate, and judge the client's view.
---

stormbreaker-rest turns a stream's records into requests to a service that axx's rest pack registered:

```gherkin
Given the parcels service with the following properties:
  | url | ${sys:parcels.url} |
And the collections stream of POST requests to /parcels/{reference}/collection
And the collections stream path variables are the path metadata
And the collections stream headers are the headers metadata
And the collections stream query parameter source is "load-test"
When the collections stream is sent at 25 requests per second
Then the load completes with no failed requests
And the client's p99 latency of the load is at most 0.5 seconds
```

Each record's data is a request's JSON body. Its address comes from the service's URL and the path, with these filled in:

- **path variables** from a metadata map of the record (a variable can name a nested value);
- **the query and headers** from metadata maps of their own;
- **values every request shares**, set by a step, over what any record says.

## Open model

Request *i* is due at *i*/rate seconds, whatever the requests before it are doing. A service that slows down gets the same arrivals, with more of them in flight, as it would in production; a closed loop would send fewer and hide the slowdown. Requests are built as they come due, so a stream larger than memory is fine. Each request has a timeout of its own (`timeout`, default 30s).

Two guardrails stop a load that is no longer the one declared:

- **The target is not keeping up.** When more than `maxInFlight` requests (default 50,000) are in flight, the load stops and fails. A service that cannot keep up does not drown the driver.
- **The driver is not keeping up.** When requests leave more than `maxDrift` (default 1s) after their due time, for five seconds in a row, the load stops and fails. The machine sending them has run out of CPU, and the rate it reports would no longer be the rate asked for. `maxDrift: off` turns this guardrail off.

## Connections

Requests share a pool of connections, at most `maxConnections` (default 512). A load balancer spreads hundreds of connections across the service's replicas as it spreads its users'. Past the pool, a request waits for a connection, and its latency says so. One process sends 100,000 requests a second this way ([scale](/explanations/scale/)).

`connections: fresh` opens a connection for each request instead. It runs out of the machine's ports past a few thousand requests a second.

```yaml title="axx.yaml"
packs:
  stormbreaker-rest:
    maxConnections: 1024
    maxInFlight: 100000
    maxDrift: 2s
    timeout: 10s
```

## The client view

Each load attaches what the client saw to the report:

- the requests sent and failed, and the answers by status;
- the latency quantiles;
- how far the arrivals drifted from their due times.

It is the client view, labelled apart from what the service's own metrics say ([check those](/guides/check-slos/)).

## Requests written by hand

A smoke test written with axx's rest steps can join a run too. Name where its payloads' identities are, and they are declared before each request is sent:

```gherkin
Given the requests on parcels are identified by $.reference,$.pieces[*].id
```

Without a value, a bodyless load sends the same request many times:

```gherkin
When 1000 GET requests are sent to /ping at 100 requests per second
```
