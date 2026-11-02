# 0026: A UI of its own, beside the orchestrator

- Status: Accepted
- Date: 2026-11-01

## Context

Everything stormbreaker knows about an environment is in its orchestrator's API and in each run's journal: who takes part, which runs are open, what a run's gate waits for, what each instance said. Reading it takes `curl` and knowing where to look. People new to stormbreaker, and people watching a run they did not start, need to see it.

Not every environment wants a UI. Some keep everything they run to what they must, and some would rather not expose more than an API.

## Decision

- **The UI is its own component**: `stormbreaker ui serve`, in the same binary and as its own image. It is optional; the orchestrator does not serve it, and nothing depends on it.
- **It reads one or more orchestrators through their API**, one per environment, with a token that may only read: a principal marked `read-only`. It keeps the token on the server, proxies only the reads it shows, and sends nothing else.
- **It shows what is true**: the environment's dispatchers and their instances, its groups and runs, and a run as it happens: its state, its readiness gate and what holds it, its instances' statuses, and its journal. Nothing on it is drawn by hand.
- **It is proven by axx.** Its acceptance suite is axx features that drive it in a browser with axx's web pack, against the lab, while stormbreaker-core opens the runs it shows. The features come first.
- **It starts nothing yet.** Starting a run from it will trigger the team's CI, so that suites keep running on CI runners, never in the cluster; that is for later.

## Consequences

- An environment that wants no UI runs none. One that wants it runs one replica beside its orchestrator, or one for several environments.
- The UI's assets are built from TypeScript in `ui/`, with the docs' diagram engine, into `internal/ui/dist`, which the binary embeds. The built files are generated: `go generate ./internal/ui`.
- A topology drawn from truth needs the dispatchers to say what they read; that is its own decision.

## Alternatives

**Served by the orchestrator.** One thing fewer to deploy, but every environment would carry a UI and its surface, wanted or not, and the orchestrator would grow a front end to maintain.

**A static page that calls the orchestrator from the browser.** The token would live in the browser, and every orchestrator would need its CORS opened.
