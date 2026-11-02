---
title: Watch it in the UI
description: Run the stormbreaker UI beside an environment's orchestrator, to watch each run as it goes, with its steps, its load and its checks, and the environment under test.
---

The UI shows an environment as it goes:

- **What runs now.** Each open run, in its scenario's own words: the step it is on, the load it sends and how that load fares, its readiness gate, and each dispatcher instance's part.
- **The environment.** Each group drawn with its dispatchers and their instances, and, while a run goes, the suite and the services under its load.
- **Every run before**, a page at a time, by group and state.
- **A run's page**: its steps, its load charted as it goes, its checks, its gate, its instances, the topology under test, its place in the lifecycle, and its journal.

It only reads, and it is optional: its own process, beside the orchestrator, or one UI for several environments ([ADR 0026](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0026-a-ui-of-its-own.md)).

## Run it

```sh
stormbreaker ui serve --environment staging=http://orchestrator:8080 --token "$UI_TOKEN"
```

It prints the link to open, as this machine reaches it: `stormbreaker UI: http://localhost:8095`.

| Flag | Variable | Default | What it is |
|---|---|---|---|
| `--environment` | `STORMBREAKER_UI_ENVIRONMENTS` | none | an environment to show, `name=url` of its orchestrator; repeat it, or separate them with commas, for several |
| `--token` | `STORMBREAKER_TOKEN` | none | a token that may read the orchestrators |
| `--addr` | `STORMBREAKER_UI_ADDR` | `:8095` | where it listens |

Or the image, `ghcr.io/nimbusxr/stormbreaker-ui`, which serves on 8095 and keeps nothing.

## Who may connect

- **The UI reads with a token that may only read.** Give it a principal of its own, marked `read-only`: the orchestrator refuses it anything else. The token stays on the UI's server, which passes on only the reads it shows; the browser never holds it.

  ```yaml title="principals.yaml"
  principals:
    - name: ui
      token-sha256: 3c1a…   # printf %s "$UI_TOKEN" | sha256sum
      read-only: true
  ```

- **Who may look is your ingress's business.** The UI has no users of its own: put it behind the sign-in your other internal tools use.

## What it costs the orchestrator

Nothing per viewer. The UI's server reads each orchestrator about once a second, and only while someone watches, and streams what it read to every browser. Reads of the same thing within a second are shared. A hundred people watching cost the orchestrator what one does ([ADR 0027](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0027-a-run-as-it-goes.md)).

## Where the steps and the load come from

The suite says how its run goes: stormbreaker-core sends the scenario's steps about once a second, stormbreaker-rest and stormbreaker-kafka add their load, and stormbreaker-prometheus its checks. The orchestrator keeps only the latest, in memory, and the final one with the run, so a closed run still shows how it went. Set `progress` in stormbreaker-core's configuration to send it less often, or `0` to send only the last.

The charts are drawn from what the UI saw while it watched. A run nobody watched shows its final figures, not its curve.

## Stopping a run

Cancel its CI job. The suite stops its heartbeat, the orchestrator abandons the run when its lease runs out, and its dispatchers clean up all the same. The UI starts and stops nothing.
