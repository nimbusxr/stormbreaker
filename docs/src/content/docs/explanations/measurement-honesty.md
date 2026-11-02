---
title: Measurement honesty
description: "Open-model load, merged buckets, refused windows and labelled views: what stormbreaker does so a verdict means what it says."
---

A verdict is only worth what its measurement is. These rules keep stormbreaker's honest.

- **Load is open-model.** Arrival *i* is due at *i*/rate seconds, whatever earlier arrivals are doing. A closed loop sends less when the service slows, and so hides exactly the slowness the test is for. Throttling arrivals to the service's pace is a correctness failure, not a tuning choice.
- **Percentiles come from merged buckets.** Across regions and instances, histogram buckets are summed and the quantile interpolated from the sum. Averaging per-region percentiles understates the slow region, every time.
- **Windows too short to judge are refused.** A window shorter than the scrape interval says nothing honest. The check fails, rather than widening the window.
- **The client view and the platform view are labelled apart.** What the load's client saw (its latencies, failures and drift) is recorded and attached as the client view. What the service's own metrics say is the platform view. Neither stands in for the other.
- **Say what was not exercised.** When a filter at the front door short-circuits some inputs, the verdict should say so, rather than claim a path was tested.
- **Criteria stay honest under concurrent traffic.** Other runs share the environment, so judge what is yours: your window, your services.
