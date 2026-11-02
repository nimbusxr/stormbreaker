# 0006: Metrics checks merge buckets and refuse short windows

- Status: Accepted
- Date: 2026-11-01

## Decision

- **A Prometheus service is one logical service across its regions.** Its `url` lists each region's server. [0014](0014-prometheus-services-span-apps-regions-and-global-views.md) extends it to per-app servers, HA pairs and global views.
- **Merge as one Prometheus would.** Counters are summed. Histogram buckets are summed, then the quantile is interpolated. Percentiles are never averaged.
- **The window is the run's.** A check judges the window from the run's opening to when its traffic was complete, queried at its end.
- **Short windows are refused.** A window shorter than the service's minimum (its scrape interval) fails the check.
- **Checks poll within their patience**, so scrape lag is waited out.
