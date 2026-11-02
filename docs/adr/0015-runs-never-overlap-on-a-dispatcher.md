# 0015: Runs never overlap on a dispatcher

- Status: Accepted
- Date: 2026-11-01

## Context

A run is a claim on a shared environment for a while: its load lands in the services' metrics, its dispatchers prepare data for it, and its cleanup removes that data. Two runs at once on the same services spoil each other:

- **Their measurements mix.** An SLO check reads the service's own metrics over its run's window, so another run's load in that window is in its numbers, and nothing can take it out afterwards.
- **Their dispatchers interleave.** A dispatcher preparing for one run while it cleans up another, against the same stores, is work nobody can reason about.
- **Their capacity is shared.** A load test sized for the environment is not sized for two.

On the lab, two load runs in the same group both opened, ran and passed, and each one's numbers held the other's load.

## Decision

- **A run holds every dispatcher of its group**, from its opening until it closes.
- **The orchestrator refuses a run that would overlap one that holds.** A run in a group that is held, or in a group that shares a dispatcher with one that is, is refused with `409`. The problem's `conflictsWith` names the run it would overlap. The check and the opening are one transaction, so two suites can never both win.
- **A suite waits its turn.** stormbreaker-core tries again until the run that holds closes, for up to its `turn` setting (30 minutes by default), and says in the scenario's log what it is waiting for. Then it fails, naming the run.
- **A cleanup that never ends does not hold forever.** Once a run's cleanup has run longer than the orchestrator's cleanup hold (`--cleanup-hold`, 15 minutes by default), the run stops holding: it goes on cleaning up, other runs may open beside it, and its journal says so. The run's view shows whether it holds (`holds`).
- **Groups are the scope.** The orchestrator knows dispatchers, not services. Teams whose services meet should be in one group, or in groups that share their dispatchers; a team testing alone gets a group of its own.

## Alternatives

**Warn and let runs overlap.** A warning after the fact cannot unmix measurements.

**Queue runs in the orchestrator.** A queue there holds suites that may have gone away, and needs an order nobody chose. The suite already waits for its gate; waiting for its turn is the same thing, and the refusal says why.

**Lock per service.** The orchestrator does not know services, and asking every scenario to name them is a list that goes stale. Groups already say who takes part.

**Hold until closed, however long.** One stuck cleanup would stop every run in its group. The hold bounds it, and the journal says what is still unfinished.
