# 0018: Packs are how stormbreaker grows, and what they build on is public

- Status: Accepted
- Date: 2026-11-01

## Context

Stormbreaker's traffic and its judgment are axx packs: stormbreaker-rest and stormbreaker-kafka send load, and stormbreaker-prometheus judges SLOs. Organizations send traffic over more than HTTP and Kafka (gRPC, AMQP, NATS), and keep their metrics in more than Prometheus (Datadog, CloudWatch, New Relic). Each of those is a pack still to write, by us or by a team that needs it.

Every such pack needs the same few things, and until now they were internal or inside one pack:

- **A run to belong to.** It must declare what its traffic creates before sending it, and hold its traffic at the readiness gate.
- **Open-model arrivals.** Arrival *i* is due at *i*/rate, with guardrails that stop a load that is no longer the one declared.
- **A window, and honest judgment.** It must judge the run's window, within a patience that waits out reporting delay, and merge latency histograms across sources rather than averaging percentiles.

## Decision

- **New traffic and new judgment are packs.** Stormbreaker's core does not grow a vendor integration. A pack depends on axx's public packages and on these of stormbreaker's, and on nothing internal to either.
- **What a pack can rely on is public, and kept stable:**
  - `packs/core` (stormbreaker-core's Go API): the scenario's run (`Current`), declaring identities (`Run.Declare`, `Run.Bind`, `Observe`), the readiness gate (`Ready`), and registering the steps that send traffic (`Traffic`), so the gate holds them back until the run is ready.
  - `arrival`: the open-model scheduler (`Run`, `Count`), its client view (`Result`, `Histogram`), and its guardrails: too many arrivals in flight (`ErrOverloaded`), arrivals leaving late for too long (`ErrBehind`), and a warm start for clients that connect on their first send.
  - `slo`: the run's window (`RunWindow`), refusing one shorter than a source's reporting interval; judging a check within a patience (`Judge`); and merging cumulative buckets (`Merge`) before interpolating a quantile (`Quantile`).
  - `streams`: generating a stream's records and identities.
- **A pack names itself `stormbreaker-<what>`** when it is ours. A third party's pack names itself as it likes, and is listed in a project's `axx-packs.yaml` by module path like any other.
- **The same rules hold for every pack.** Traffic is open-model, declared before it is sent, and held at the gate. Judgment is over the run's window, merged honestly, and refuses a window too short to judge.

## Alternatives

**Plugins inside the orchestrator.** The orchestrator never sends traffic or judges it; a plugin there would put test-shaped code in the environment's cluster.

**Copy the helpers into each pack.** Every copy would drift from the others, and the guarantees (open model, honest merging) would be only as good as the weakest copy.

**Keep the helpers internal until a second pack needs them.** The second pack is likely to be someone else's, and could not import them.
