---
title: Quickstart
description: Run the stormbreaker lab on your machine, drive load through it with axx, and read the story of the run.
---

The lab is two teams' services, each with its dispatcher, an orchestrator, and an acceptance suite. In ten minutes you will run a load scenario against it and read what the run did: what it declared, who prepared, what was sent, and what was cleaned up.

## You need

- [axx](https://axx.nimbusxr.us/guides/install/), which runs the suite.
- Go, at the version the repository pins (`mise install` gives it to you).
- A clone of the repository: `git clone https://github.com/nimbusxr/stormbreaker`.

## Start the lab

From the repository, start the lab on one machine:

```sh
examples/lab/scripts/local.sh
```

It builds and starts three things, and leaves them running until you press Ctrl-C:

- the orchestrator, on port 18080;
- the parcels service, on 18081;
- the parcels team's dispatcher, which registers with the orchestrator and joins the `delivery` group.

The full lab, on a kind cluster with Kafka, Postgres, Prometheus and a second team, is `examples/lab/scripts/up.sh`.

## Run the suite

In another terminal:

```sh
cd examples/acceptance
axx run --tags 'not @lab'
```

The first run takes a little longer: axx builds itself with the suite's packs, axx's `rest` and `kafka` and stormbreaker's four. Two scenarios pass:

- **A smoke check.** It runs in a bubble: no run, nothing to clean up.
- **A load run.** It registers 250 parcels at 50 a second.

## Read the run

The load scenario's report has the run's journal attached. The orchestrator serves it too:

```sh
curl -s localhost:18080/runs/<run id>/journal
```

```text title="abridged"
dispatchers on this run
  parcels              local        required  completed

what happened
  +0.000s    orchestrator     the run opens; every instance of every dispatcher in the group is notified
  +0.004s    suite/suite      declares 250 identities at parcels:/parcels
  +0.083s    parcels/local    declares 250 identities at parcels-mem:local
  +0.097s    parcels/local    reports prepared: a prepare round added nothing; settled
  +5.142s    orchestrator     the suite says its traffic is done; the window closes
  +5.142s    orchestrator     the suite says downstream has settled
  +5.143s    orchestrator     cleanup opens; every instance removes what it claimed
  +5.207s    parcels/local    reports 250 identities at parcels-mem:local (removed-verified)
  +5.207s    orchestrator     the run closes: every instance completed and the footprint stopped growing, with 750 identities answered for
```

Before a single parcel was registered:

1. The suite declared all 250.
2. The parcels team's dispatcher prepared for them and said so.
3. Only then did the readiness gate let the traffic through.

After the traffic, the dispatcher deleted every one and checked each was gone. The scenario passed only once that cleanup was verified.

## Next

- [Your first run](/tutorials/first-run/): your own service, stream and scenario.
- [Why stormbreaker](/explanations/why-stormbreaker/): what a shared environment asks of a load test.
