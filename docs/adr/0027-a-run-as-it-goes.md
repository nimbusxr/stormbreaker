# 0027: A run as it goes

- Status: Accepted
- Date: 2026-11-01

## Context

The orchestrator knows a run's lifecycle: its gate, its dispatchers' reports, its footprint and its journal. It does not know how the suite's scenario is going: which step it is on, what load it sends and how that load fares, and what its checks found. All of that lives in the axx process on the CI runner, and reaches people in axx's report, after the run.

A run in a shared environment is watched: by the team that started it, and by every team whose service it goes through. They want to see it as it goes, in the UI (ADR 0026), without asking the team that started it.

The orchestrator's job is coordination: durable transitions, the gate, notifications, the footprint. Whatever shows a run as it goes must not add to that job more than it has to.

## Decision

- **The suite says how its run is going**: `PUT /runs/{id}/progress`, as the run's suite, about once a second while the run is open, and at least every ten seconds. It says:
  - its scenario;
  - its steps, each with its status;
  - the load each stream sends: the declared rate, what is planned, sent, in flight and failed, and the latency the suite sees;
  - its checks, each with its verdict.

  Its last, marked final, comes before the suite closes the run.
- **The orchestrator holds the latest, in memory, and nothing else.** No history, no aggregation, and nothing written, but for the final one, which it keeps with the run, with a line in its journal. A snapshot nobody has renewed for a minute is dropped. A restart loses the live one, and the next post brings it back.
- **Nothing waits on progress.** The gate, closing and cleanup never read it. A suite that sends none loses nothing but the view.
- **`GET /runs/{id}/progress` reads it**: the live one while the suite sends it, the final one after. A run read on its own says when it went to cleanup and when it closed, as the list of runs does.
- **The UI shows runs live, and pays for its viewers itself.** Its server reads each orchestrator about once a second, and only while someone watches. It pushes what it read to every browser over Server-Sent Events, and keeps the history its charts draw. A hundred viewers cost the orchestrator what one does.
- **Who may look is the ingress's business.** The UI has no login of its own. Put it behind the single sign-on the environment already has. It reads with a read-only principal.
- **Stopping a run stays in CI.** Cancelling the job stops the suite's heartbeat, and the run is abandoned and cleaned up, as it always was. The UI starts and stops nothing.

## Consequences

- stormbreaker-core sends the progress. The other packs add to it through stormbreaker-core's Go API: stormbreaker-rest and stormbreaker-kafka report their load, and stormbreaker-prometheus reports its checks.
- The orchestrator does one more write per run, the final progress. It holds one snapshot per open run in memory, of at most 64 KiB.
- Dispatchers are untouched: the route is the suite's.
- A suite written another way may send progress or not. Nothing depends on it.

## Alternatives

**Suites send progress to the UI.** The UI is optional, and there may be several. Suites would need to know where it is, and with none deployed, nobody could see progress at all.

**Progress in the heartbeat.** A heartbeat is a durable write, every ten seconds. Sending one a second would multiply the orchestrator's writes by ten for every open run, to carry something it never decides on.

**Every snapshot kept.** A history of progress is for charts, which are the UI's. Kept on the orchestrator, it would grow the store with data nothing coordinates on.

**WebSockets.** Two-way, which the UI does not need, and more code on both ends. Server-Sent Events are plain HTTP: they pass proxies and reconnect by themselves.
