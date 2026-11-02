---
title: Scale
description: What one orchestrator and one runner handle, measured in a closed environment, and what to plan for in yours.
---

One orchestrator serves a whole environment, and a scenario's traffic comes from the CI runner that runs it.

## What it handles

| | Measured in a closed environment | Plan for in yours |
|---|---|---|
| Runs an hour, one orchestrator | 7,200 smoke runs and 180 integration runs at an hour's pace, every one clean, on 2 CPUs | 2,400 an hour on 2 vCPUs, with room to grow |
| Runs open at once | 262 | over 100 |
| Dispatcher instances | 6,900 | several thousand |
| Time stormbreaker adds to a run | 0.3 s for the gate to open, 0.5 s to close after cleanup | about a second, plus your dispatchers' own work |
| Largest run | 1 billion identities | hundreds of millions |
| Traffic from one runner | 140,000 HTTP requests a second; 200,000 Kafka events a second (150,000 in Avro) | about 20,000 HTTP requests a second for each core |
| Orchestrator memory | under 900 MiB | 2 GiB |

The closed environment is one machine, an Apple M4 Max, with the orchestrator in a Linux container limited to 2 CPUs and 2 GiB. Thousands of dispatcher instances reach it over a container network, but they are stand-ins, not your services. In a real environment, cloud vCPUs are slower, network storage takes longer to commit a write, and your dispatchers' prepare and cleanup take as long as your systems do. The planning column leaves room for all three.

## What limits it

- **The orchestrator's one writer.** Every change is a transaction on one database connection. At 2,400 smoke runs an hour it was busy 15% of the time, and 28% at three times that. Watch `rate(stormbreaker_writer_busy_seconds_total[5m])`, and add CPU or faster storage as it nears 0.7. Past what one writer does, the store moves to Postgres ([roadmap](/explanations/roadmap/)).
- **A group takes one run at a time.** Runs never share a dispatcher, so a 2-minute smoke run holds its group for about 3 minutes: about 20 runs an hour. A service in every team's group would hold all of them up. Leave it out of the smoke groups, and mock it there ([mock partners](/guides/mock-partners/)).
- **A runner's CPU.** Past what one runner sends, a run needs several. Sharding a run across runners is on the [roadmap](/explanations/roadmap/).

## Sizing

- **The orchestrator:** 2 vCPUs, 2 GiB and an SSD volume serve the environment above. Size the volume for about 40 bytes for each claim, kept for 72 hours: a run of 60 million identities with one team deriving from them comes to about 7 GB ([size its volume](/guides/deploy-the-orchestrator/#size-its-volume)).
- **The readiness timeout:** your largest run's. Sixty million identities opened their gate in under 2.5 minutes on 2 CPUs; allow 5 to 10 minutes for each hundred million.
- **Runners:** one core for each 20,000 HTTP requests a second.

## How it was measured

The enterprise test runs a large organization's busiest hour against an orchestrator in its own container. It has 2,000 services, each with a dispatcher in three regions, and a smoke run on every deploy, 40 a minute. Each smoke run has 1,000 identities and 2 minutes of traffic. It also has an integration run every minute, and a load run of 60 million identities in the middle. Run it yourself with Docker:

```sh
scripts/scale-enterprise.sh                                    # the peak hour
STORMBREAKER_SCALE_SMOKE_RATE=120 STORMBREAKER_SCALE_PEAK=10m scripts/scale-enterprise.sh   # three times it
```

Each part also has a scale test of its own, behind the `scale` build tag: `go test -tags scale -run <Test> -v ./internal/scale ./packs/...`. The tests are `DispatcherAtScale` (identities in one run), `OrganizationAtScale`, `RESTDriverCeiling`, `RESTSustained` and `PublishAtScale` (Kafka, against a broker of your own).
