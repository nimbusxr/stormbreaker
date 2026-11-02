---
title: Deploy the orchestrator
description: Run one orchestrator per shared environment, with its database on a volume sized to its runs, and a token for its API.
---

The orchestrator is the one stormbreaker service every environment needs. It holds:

- the registry of dispatchers;
- each run's readiness gate, footprint and journal;
- the notifications it sends.

It is small: SQLite on a volume, one replica.

## Run it

```sh
stormbreaker orchestrator serve --addr :8080 --db /data/orchestrator.db --token "$TOKEN"
```

Or the image, `ghcr.io/nimbusxr/stormbreaker-orchestrator`, which serves on 8080 and keeps its database in `/data`. In Kubernetes:

- **Run one replica, with the `Recreate` strategy.** One database means one writer.
- **Give it a persistent volume sized to its runs** ([below](#size-its-volume)). It stores identities and where they are, never payloads.
- **Expose it to the suites and to the dispatchers.** It reaches the dispatchers at the callback URLs they register.

The lab's manifest, `examples/lab/k8s/orchestrator.yaml`, is a starting point.

## Settings

Every flag has an environment variable:

| Flag | Variable | Default | What it is |
|---|---|---|---|
| `--addr` | `STORMBREAKER_ADDR` | `:8080` | where it listens |
| `--db` | `STORMBREAKER_DB` | `orchestrator.db` | its SQLite database |
| `--token` | `STORMBREAKER_TOKEN` | none | a bearer token with every grant: a lab's, or a team's on its own |
| `--principals` | `STORMBREAKER_PRINCIPALS` | none | a file of credentials, each with its grants ([below](#a-credential-per-team)) |
| `--lease` | `STORMBREAKER_LEASE` | `30s` | how long a run's or a dispatcher's lease lasts unrenewed |
| `--readiness-timeout` | `STORMBREAKER_READINESS_TIMEOUT` | `120s` | how long required dispatchers have to prepare |
| `--stale-after` | `STORMBREAKER_STALE_AFTER` | `30s` | how long a required dispatcher may stay silent while preparing |
| `--retention` | `STORMBREAKER_RETENTION` | `336h` | how long closed runs are kept |
| `--cleanup-hold` | `STORMBREAKER_CLEANUP_HOLD` | `15m` | how long a run in cleanup keeps holding its group's dispatchers before other runs may open beside it |
| `--cleanup-deadline` | `STORMBREAKER_CLEANUP_DEADLINE` | `24h` | how long a run may stay in cleanup before it closes unfinished, naming the instances that never completed |
| `--footprint-retention` | `STORMBREAKER_FOOTPRINT_RETENTION` | `72h` | how long a closed run keeps its identities; its journal stays for `--retention` |
| `--disk-reserve` | `STORMBREAKER_DISK_RESERVE` | `1GiB` | the room it keeps on its volume: below it, no new runs or declarations (`0` keeps none) |

Size `--readiness-timeout` to your largest run: sixty million identities took under 2.5 minutes on 2 CPUs ([scale](/explanations/scale/#sizing)). Give it credentials: without any, anyone who can reach it can use its API, and it says so in its log.

## A credential per team

One `--token` grants everything: fine for a lab, or one team on its own. Once several teams share the environment, give each its own credential in a principals file, mounted from a secret:

```yaml title="principals.yaml"
principals:
  - name: platform
    token-sha256: 5f2c…   # printf %s "$TOKEN" | sha256sum
    admin: true           # everything, groups included
  - name: delivery-ci
    token-sha256: 9b71…
    suites: [delivery]    # opens runs in delivery, and acts as their suite
  - name: parcels-team
    token-sha256: 0d4e…
    dispatchers: [parcels] # registers, joins groups and reports as parcels
  - name: ui
    token-sha256: 3c1a…
    read-only: true        # reads, and nothing else: the UI's
```

The file holds each token's SHA-256, never the token. A request outside its principal's grants is refused with `403`, naming what it lacks; reads are every principal's. A dispatcher's name belongs to one team: registering it as another team while it has live instances is refused with `409`, whatever the token. See [ADR 0019](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0019-each-team-has-its-own-credentials.md).

## Size its volume

What grows is the footprint: about 40 bytes for each claim ([ADR 0016](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0016-the-orchestrators-store-is-sized-reserved-and-pruned.md)). A run's claims are its identities times the statements made about each: the suite's declaration, each team's derived declaration, and each removal.

| A run of, with one team deriving | Claims | On disk |
|---|---|---|
| 1 million parcels | 3 million | 120 MB |
| 60 million parcels | 180 million | 7.2 GB |
| 1 billion parcels | 3 billion | 119 GB |

The volume holds the identities of every run closed within `--footprint-retention` (72 hours), and journals, which are small, for `--retention` (14 days). Add `--disk-reserve` and room to grow.

**Below the reserve, it takes no new runs and no declarations**, and answers `507`. Removals, statuses and closing always go through, so cleanup is never what a full disk stops. Alert before it gets there: on `stormbreaker_volume_free_bytes` against `stormbreaker_volume_reserve_bytes`.

**Back it up** with SQLite's online backup, or by streaming its write-ahead log to object storage (Litestream does this). Copying the file under a running orchestrator is not a backup.

## Watch it

`GET /metrics` gives its metrics in Prometheus's text format, as counts without names. Alert on:

- `stormbreaker_runs_cleanup_past_hold` above zero: a cleanup has not verified;
- `stormbreaker_runs_unfinished` growing: a cleanup ran past its deadline, and its journal says who did not finish;
- `stormbreaker_notifications{status="exhausted"}` growing: notifications are not reaching an instance, which stays on its runs only while it still reports through the event feed;
- `stormbreaker_dispatcher_instances{lease="lapsed"}` growing: registrations that went away;
- `stormbreaker_volume_free_bytes` nearing `stormbreaker_volume_reserve_bytes`;
- `rate(stormbreaker_writer_busy_seconds_total[5m])` above 0.7: its one writer is busy 70% of the time and requests start to queue for it. Add CPU or faster storage ([scale](/explanations/scale/#what-limits-it)).

## Its contract

It serves its own OpenAPI document at `/openapi.yaml`, `/healthz` for probes, and `/metrics`. The [reference](/references/orchestrator-api/) summarizes the API, and the notifications it sends are in the document's `webhooks`.
