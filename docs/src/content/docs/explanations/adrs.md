---
title: Architecture decisions
description: The decisions that shaped stormbreaker, and what each was chosen over.
---

Stormbreaker records its significant decisions as ADRs in [`docs/adr`](https://github.com/nimbusxr/stormbreaker/tree/main/docs/adr). A change to a public contract needs one:

- step text;
- the orchestrator's API;
- the notification contract;
- the dispatcher SDK.

| ADR | Decision |
|---|---|
| [0001](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0001-build-on-axx-in-go.md) | Build in Go, on axx: suites are axx suites, steps are axx packs |
| [0002](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0002-coordinate-runs-through-an-orchestrator.md) | Coordinate runs through an orchestrator per environment, with webhooks |
| [0003](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0003-the-footprint-comes-from-declarations.md) | A run's footprint comes from declarations, in rounds, never from markers |
| [0004](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0004-streams-over-fixture-factories.md) | Volume data is streams over axx's fixture factories, deterministic per record |
| [0005](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0005-load-is-driven-from-the-suite.md) | Load is driven open-model from the suite, on the CI runner |
| [0006](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0006-metrics-merge-buckets.md) | Metrics checks merge regions' buckets and refuse short windows |
| [0007](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0007-mocks-through-axx-wiremock.md) | Partners are mocked with axx's WireMock image and data-only mock images |
| [0008](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0008-teams-steward-their-own-stores.md) | Teams steward their own stores: removed and verified, or clean by a date |
| [0009](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0009-standard-webhooks.md) | Notifications are Standard Webhooks |
| [0010](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0010-sqlite-orchestrator.md) | The orchestrator keeps its records in SQLite |
| [0011](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0011-dispatchers-are-defined-by-their-protocol.md) | A dispatcher is defined by its protocol, and held to a conformance kit |
| [0012](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0012-failed-and-abandoned-runs-are-cleaned-up.md) | Failed and abandoned runs are cleaned up like any other |
| [0013](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0013-rounds-settle-on-a-footprint-version.md) | Rounds settle on a footprint version, and the footprint streams |
| [0014](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0014-prometheus-services-span-apps-regions-and-global-views.md) | A Prometheus service spans apps, regions and global views |
| [0015](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0015-runs-never-overlap-on-a-dispatcher.md) | Runs never overlap on a dispatcher |
| [0016](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0016-the-orchestrators-store-is-sized-reserved-and-pruned.md) | The orchestrator's store is sized, reserved and pruned |
| [0017](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0017-a-dispatcher-reads-its-own-answers-from-the-footprint.md) | A dispatcher reads its own answers from the footprint |
| [0018](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0018-packs-are-how-stormbreaker-grows.md) | Packs are how stormbreaker grows, and what they build on is public |
| [0019](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0019-each-team-has-its-own-credentials.md) | Each team has its own credentials |
| [0020](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0020-operators-list-runs.md) | Operators list runs |
| [0021](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0021-dispatchers-say-who-they-are-and-what-they-speak.md) | Dispatchers say who they are, and what they speak |
| [0022](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0022-the-event-feed-is-read-by-group.md) | The event feed is read by group |
| [0023](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0023-a-cleanup-has-a-deadline.md) | A cleanup has a deadline |
| [0024](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0024-a-hook-walks-only-what-it-derives-from.md) | A hook walks only what it derives from |
| [0025](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0025-lease-renewals-are-written-together.md) | Lease renewals are written together |
| [0026](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0026-a-ui-of-its-own.md) | A UI of its own, beside the orchestrator |
| [0027](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0027-a-run-as-it-goes.md) | A run as it goes |
| [0028](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0028-the-docs-site-on-s3-and-cloudfront.md) | The docs site on S3 and CloudFront |
