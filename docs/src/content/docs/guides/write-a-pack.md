---
title: Write a pack
description: Add a traffic protocol or a metrics vendor to stormbreaker as an axx pack, built on stormbreaker-core and its public arrival and slo packages.
---

Stormbreaker's traffic and its judgment are axx packs: stormbreaker-rest and stormbreaker-kafka send load, and stormbreaker-prometheus judges SLOs. A protocol or a metrics vendor that stormbreaker does not cover yet is a pack too, yours or ours ([ADR 0018](https://github.com/nimbusxr/stormbreaker/blob/main/docs/adr/0018-packs-are-how-stormbreaker-grows.md)).

A pack is a Go module with a `Pack()` function, written against [axx's pack API](https://axx.nimbusxr.us/). What makes it a stormbreaker pack is what it builds on, and the rules it keeps:

| Package | What it gives a pack |
|---|---|
| `github.com/nimbusxr/stormbreaker/packs/core` | the scenario's run, declaring identities, the readiness gate, and registering steps that send traffic |
| `github.com/nimbusxr/stormbreaker/arrival` | open-model arrivals, the client view, and the guardrails that stop a load that is no longer the one declared |
| `github.com/nimbusxr/stormbreaker/slo` | the run's window, judging within a patience, and merging latency histograms honestly |
| `github.com/nimbusxr/stormbreaker/streams` | a stream's records and identities |

## A traffic pack

A traffic pack keeps three rules:

- **Declare before sending.** Bind the stream to the run: that declares every identity its records will create, before any is sent.
- **Wait at the gate.** Register the steps that send, so stormbreaker-core holds them back until the run is ready.
- **Open model.** Send with `arrival.Run`, at the declared rate, whatever the target is doing.

```go title="pack.go"
package grpcload

import (
	"context"

	"github.com/nimbusxr/axx/core"

	"github.com/nimbusxr/stormbreaker/arrival"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/streams"
)

const stepSend = "grpcload.stream.send"

func Pack() core.Pack {
	sbcore.Traffic(stepSend) // the readiness gate holds this step back
	return pack{}
}

type pack struct{}

func (pack) Manifest() core.Manifest {
	return core.Manifest{
		Name: "grpc-load", Namespace: "grpc-load", Requires: []string{"stormbreaker-core"},
		Steps: []core.StepDef{{
			ID: stepSend, Keyword: "When", Expr: "the {word} stream is sent to {word} at {int} calls per second",
			Run: send,
		}},
	}
}

func send(sc *core.Scenario, a core.Args) error {
	run, ok := sbcore.Current(sc)
	if !ok {
		return core.Failf("a load needs a stormbreaker run: tag the scenario @stormbreaker-group/<group>")
	}
	// Declares every identity the stream's records will create, at the service and method.
	stream, err := run.Bind(sc.Context(), a.String(0), a.String(1), "Register")
	if err != nil {
		return err
	}
	if err := sbcore.Ready(sc); err != nil {
		return err
	}
	// The run's progress shows the load as it goes, for whoever watches the run (ADR 0027).
	rate, live := float64(a.Int(2)), &arrival.Live{}
	run.Load(a.String(0), sbcore.Arrivals(a.String(0), "grpc", a.String(1), rate, stream.Records(), live))
	res, err := arrival.Run(sc.Context(), rate, stream.All(), func(ctx context.Context, rec *streams.Record) error {
		return call(ctx, a.String(1), rec.Data.CompactJSON())
	}, arrival.Options{Warm: true, Live: live})
	if res != nil {
		sc.Log("grpc-load: %d sent, %d failed", res.Sent, res.Failed)
	}
	return err
}
```

`run.Load` adds the stream to the run's progress, which the UI shows as the run goes: `arrival.Live` follows the arrivals, and `sbcore.Arrivals` reads it each time the progress goes out.

`arrival.Run` stops with `arrival.ErrOverloaded` when too many calls are in flight, and with `arrival.ErrBehind` when calls keep leaving late. Either way, the load it reports is not the load that was asked for, and the step fails. `Warm` sends the first call, and waits for it, before the clock starts, so a client that connects on its first call is connected before the rate applies.

## A metrics pack

A metrics pack keeps three rules too:

- **Judge the run's window.** `slo.RunWindow` gives it, and refuses a window shorter than the source's reporting interval.
- **Wait out reporting delay.** `slo.Judge` asks again until the check holds or its patience runs out. It never sleeps a guessed time.
- **Say how it went.** `run.Check` adds the check's verdict to the run's progress: `waiting` while it judges, then `passed` or `failed`, with what it found.
- **Merge, never average.** Sum each source's cumulative buckets with `slo.Merge`, then interpolate the quantile with `slo.Quantile`. The p99 of a fast region and a slow one averaged understates the slow one.

```go title="check.go"
func checkP99(sc *core.Scenario, a core.Args) error {
	patience, metric := a.Value(0).(time.Duration), a.String(1)
	limit, err := strconv.ParseFloat(a.Raw(2), 64) // axx gives {float} as a float32: parse its text
	if err != nil {
		return err
	}
	return slo.Judge(sc.Context(), patience, func(ctx context.Context) error {
		w, err := slo.RunWindow(sc, "the vendor's metrics", time.Minute)
		if err != nil {
			return err
		}
		var sources []map[float64]float64
		for _, region := range regions {
			buckets, err := fetchBuckets(ctx, region, metric, w.From, w.To) // the vendor's API
			if err != nil {
				return err
			}
			sources = append(sources, buckets)
		}
		p99, err := slo.Quantile(0.99, slo.Merge(sources...))
		if err != nil {
			return core.Failf("the p99 of %s over the run: %v", metric, err)
		}
		if p99 > limit {
			return core.Fail(fmt.Sprintf("the p99 of %s over the run is %gs, above %gs", metric, p99, limit), fmt.Sprintf("at most %gs", limit), fmt.Sprintf("%gs", p99))
		}
		return nil
	})
}
```

A vendor that only reports percentiles, and not buckets, cannot be merged honestly across sources. Judge each source on its own, and say so in the step's text.

## Use it

List it in the suite's `axx-packs.yaml` by module path, after stormbreaker-core:

```yaml title="axx-packs.yaml"
packs:
  - github.com/nimbusxr/stormbreaker/packs/core@v0.1.0
  - github.com/your-org/grpc-load@v1.0.0
```

A pack depends on axx's public packages and on these of stormbreaker's, and on nothing internal to either. Its step text is its public API, as stormbreaker's is: add steps, and do not change the ones people use.
