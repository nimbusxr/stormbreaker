# 0014: A Prometheus service spans apps, regions and global views

- Status: Accepted
- Date: 2026-11-01

## Context

[0006](0006-metrics-merge-buckets.md) made a Prometheus service one logical service across its regions: its `url` lists each region's server, and checks sum them. Organizations keep metrics in more ways than that:

- **Per app, per region.** Each app runs a Prometheus of its own in each region or zone, often as an HA pair.
- **Per region.** One shared Prometheus in each region scrapes every app there.
- **Global.** One view holds every region: Thanos, Mimir, or a central Prometheus.

Summing every series of a metric is right only when the servers hold the service and nothing else, and hold each series once. A shared or global server holds every app's series. An HA pair holds each series twice.

## Decision

- **`url` lists the servers whose series are summed.** One per region, one per app per region or zone, or the single global endpoint.
- **Replicas of one server are separated by `|`.** A check asks them in order and uses the first that answers. A pair is never added up.
- **A `selector` picks the service's series.** It is label matchers, like `app="parcels"`, applied to every query. Without one, a check reads every series of the metric.
- Merging is unchanged: counters are summed, buckets are summed before the quantile is interpolated, and percentiles are never averaged.

These are new properties of the registration step's table. No step text changes.

## Alternatives

**A region label instead of regional URLs.** A global view already holds every region; one URL and a selector judge it. A region matcher in the selector narrows it.

**Query every replica and deduplicate series.** That is what Thanos does with replica labels. A check asks one replica of a pair instead, which needs no labels and holds the same series.

**A step per topology.** The topology is where the metrics are, not what is judged, so it belongs in the service's registration. The checks stay the same everywhere.
