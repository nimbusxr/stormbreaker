# The stormbreaker lab

Two teams' services in a shared environment, and the suites that test them operationally:

- **parcels** (`parcels/`): the parcels service, where shops register parcels, in two regions. It
  publishes a ParcelRegistered event for each one and serves Prometheus metrics. Its admin door
  (PUT and DELETE) is how its dispatcher prepares and cleans up.
- **tracking** (`tracking/`): consumes ParcelRegistered, books a collection with the courier, a
  partner nobody in the environment owns (mocked, with axx's WireMock image), and writes a tracking
  record to Postgres.
- **dispatchers** (`dispatchers/`): each team's dispatcher, built with the dispatcher-sdk.
  - The parcels team's, one per region, upserts the parcels a run will register, and deletes and
    verifies them after.
  - The tracking team's derives the tracking records those parcels become, and deletes and
    verifies them after.
- **the suite** (`acceptance/`): axx features, run from the machine that runs CI:
  - a smoke check that runs alone;
  - a load run through the parcels service's front door;
  - a check of the service's own metrics across both regions;
  - an events run through tracking, and depot scans published in Avro.

  Its packs are stormbreaker's, by path.

This folder is a Go module of its own (`go.mod`), so its services' dependencies stay out of
stormbreaker's.

## On one machine

`lab/scripts/local.sh` runs these as processes, on the lab's ports:

- the orchestrator;
- the parcels service;
- the parcels team's dispatcher.

That is enough for the features not tagged `@lab`:

```sh
examples/lab/scripts/local.sh &
cd examples/acceptance && axx run --tags 'not @lab'
```

## On a kind cluster

`lab/scripts/up.sh` builds everything for the node's platform and loads it into a kind cluster
(context `kind-stormbreaker`). The cluster runs:

- the orchestrator;
- the parcels service in two regions behind one front door, with a dispatcher each;
- the tracking service with Postgres and its dispatcher;
- Kafka, and its Schema Registry;
- a Prometheus per region;
- the courier mock.

```sh
examples/lab/scripts/up.sh
cd examples/acceptance && axx run
examples/lab/scripts/down.sh
```

What the suite reaches is mapped to the host:

| Port | What |
|---|---|
| 18080 | the orchestrator |
| 18081 | the parcels service's front door |
| 18091, 18092 | Prometheus, east and west |
| 18093 | Kafka |
| 18094 | the Schema Registry |

Read a run's story with `curl localhost:18080/runs/<id>/journal`. The journal is also attached to
the scenario's report.
