---
title: The orchestrator's API
description: The orchestrator's HTTP API and the notifications it sends, summarized; its OpenAPI 3.1 document is the contract.
---

The orchestrator serves its OpenAPI 3.1 document at `/openapi.yaml` and `/openapi.json`; this site has it at [/openapi/orchestrator.yaml](/openapi/orchestrator.yaml). The document is the contract: every route the orchestrator serves is in it, and its tests hold every answer to it. Every operation but `/healthz`, `/metrics` and the document asks for the bearer token, when the orchestrator has one. A refusal is an RFC 9457 problem.

| Operation | What it does |
|---|---|
| `POST /groups`, `GET /groups` | create a group; list the groups and their dispatchers |
| `GET /groups/{group}/dispatchers` | a group's dispatchers |
| `PUT`, `DELETE /groups/{group}/dispatchers/{name}` | add a dispatcher with a live instance to a group; take it out |
| `POST /dispatchers`, `GET /dispatchers` | register an instance or renew its lease; list them |
| `POST /runs` | open a run for a group: 409 when another run holds its dispatchers; 422 for no such group, a required dispatcher not in it or with no live instance, or a protocol it does not speak; 507 below the disk reserve |
| `GET /runs?state=&group=&before=&limit=` | list runs, newest first |
| `GET /runs/{id}` | a run, with its readiness gate, its `footprintVersion`, and when it went to cleanup and closed, without its footprint |
| `POST /runs/{id}/events` | the suite's events: `run.traffic-complete`, `run.settled`, `run.cleanup-ready`, `run.abandoned` |
| `POST /runs/{id}/heartbeat` | renew the suite's lease (410 once the run is being cleaned up early, or closed) |
| `POST /runs/{id}/streams` | record a stream the run generates, with its seed |
| `PUT /runs/{id}/progress`, `GET /runs/{id}/progress` | the suite says how its scenario goes, about once a second: its steps, its load, its checks; the orchestrator keeps only the latest, in memory, and the final one with the run ([ADR 0027](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0027-a-run-as-it-goes.md)). 413 over 64 KiB, 410 once the run closed |
| `POST /runs/{id}/dispatchers/{name}/status?instance=` | an instance's status: `prepared`, `in-progress`, `partial`, `blocked`, `completed`, with the `footprintVersion` its round began from |
| `POST /runs/{id}/footprint/{name}?instance=` | report footprint entries (`suite` for the suite's own); answers whether they grew the footprint |
| `GET /runs/{id}/footprint?after=&until=&reports=` | a page of the footprint: its reports as written, about 100,000 identities at most; a walk passes the first page's `until` to every later one. `dispatcher` (repeatable), `store` and `locator` narrow it to what a hook derives from. 410 once `--footprint-retention` has dropped the run's identities |
| `GET /runs/{id}/footprint/claims` | the footprint settled, for small runs: each claim with what its instance last said of it (410 likewise) |
| `GET /runs/{id}/journal` | the run told as a story |
| `GET /events?since=&limit=&group=` | the event feed, the backstop for missed notifications; `group` (repeatable) narrows it to those groups' runs |

## Notifications

The orchestrator notifies every live instance of every dispatcher in a run's group of the run's events. A notification still waiting to go out stands for the later events of the same run:

- `run.opened`
- `run.footprint-updated`
- `run.traffic-complete`
- `run.settled`
- `run.cleanup-ready`
- `run.failed`
- `run.abandoned`

A notification is a claim check, `{id, runId, type, time, ref}`: its receiver reads the run for the rest and acts on the run's state. Notifications are signed as [Standard Webhooks](https://www.standardwebhooks.com) with the instance's secret: `webhook-id`, `webhook-timestamp`, and a `webhook-signature` of `v1,` and the base64 HMAC-SHA256 of `<id>.<timestamp>.<body>`.

A notification is retried with backoff until it gets a 2xx. After ten attempts the journal says so; if the instance has also stopped reporting, it is unreachable for that run, while one still reporting through the event feed stays on it. Notifications can arrive more than once and out of order; their ids tell repeats apart.
