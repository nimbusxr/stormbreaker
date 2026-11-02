# AGENTS.md: working on the stormbreaker codebase

stormbreaker (github.com/nimbusxr/stormbreaker) is an open-source framework for **operational
acceptance testing**, meaning smoke and load tests, in shared environments. In those environments
every team is answerable for the data its systems create. It is built on
[axx](https://github.com/nimbusxr/axx): suites are axx suites, and stormbreaker's steps are axx packs.

This file is for agents *contributing to stormbreaker*.

## The pieces

- **orchestrator** (`internal/orchestrator`, `stormbreaker orchestrator serve`): one service per shared
  environment. It holds:
  - the registry of dispatchers, and their leases;
  - runs, and the **readiness gate** each run must pass before traffic flows;
  - each run's **footprint**: the append-only claims of what the run created;
  - the notifications (signed webhooks) it fans out to dispatchers, plus the event feed as a backstop;
  - each run's journal.

  Its HTTP contract is `api/orchestrator.openapi.yaml`, embedded and served at `/openapi.yaml`.
- **dispatcher** (`internal/dispatcher`, `stormbreaker dispatcher serve`): one per team app per
  environment. It owns the protocol: registration, lease, signature checks, dedupe, the event-feed
  backstop, and passes per run that coalesce. On the team's behalf it calls the team's `Prepare` and
  `Cleanup` hooks.

  The Go hooks API is the **dispatcher-sdk** (`dispatcher/sdk`). The protocol defines what a dispatcher
  is, not this implementation: a dispatcher conformance kit (axx features, still to come) will check any dispatcher,
  in any language.
- **ui** (`internal/ui`, `stormbreaker ui serve`, ADR 0026, 0027): optional, beside the orchestrator. It reads one or
  more orchestrators through their API, shares those reads among viewers, and streams each environment
  live (Server-Sent Events, one reader while anyone watches). It serves the web app built from `ui/`
  (TypeScript, with the docs' diagram engine; `npm --prefix ui run check` type-checks it) into
  `internal/ui/dist`, which is generated: `go generate ./internal/ui`. Its acceptance suite is
  `examples/ui-acceptance`: axx's web pack drives it while stormbreaker-core opens runs, which watch themselves.
- **progress** (ADR 0027): a run's suite says how its scenario goes, about once a second: stormbreaker-core sends
  the steps, and the other packs add their load (`Run.Load`, `sbcore.Arrivals`) and checks (`Run.Check`).
  The orchestrator keeps only the latest, in memory, and the final one with the run; nothing waits on it.
- **protocol** (`protocol`): the wire types, Standard Webhooks signing, and the orchestrator's Go
  client. stormbreaker-core and the dispatcher both use it.
- **streams** (`streams`, `stormbreaker streams`): `*.stream.yaml`, a count plus a weighted mix of axx
  fixture-factory fixtures, generated deterministically.
  - Record *i* is a pure function of (definition, seed, run, *i*). There is no PRNG.
  - Key ranges reassemble exactly into the whole stream.
- **packs** (`packs/*`): axx packs that projects list in their `axx-packs.yaml` by module path.
  - `packs/core` (`stormbreaker-core`): ties a tagged scenario to a run on the orchestrator: open,
    readiness, heartbeat, close, journal. The other packs build on its Go API.
  - `packs/rest` (`stormbreaker-rest`) and `packs/kafka` (`stormbreaker-kafka`): a stream sent as HTTP
    requests or published as Kafka events, at a declared arrival rate (open model; `arrival`). They
    build on axx's `rest` and `kafka` packs.
  - `packs/prometheus` (`stormbreaker-prometheus`): SLO checks over the run's window, across regional
    Prometheus servers. Counters are summed and histogram buckets merged (`slo`).
  - More packs will follow, for other metrics vendors and traffic protocols, ours or third parties'
    (ADR 0018). What they build on is public: stormbreaker-core's Go API, `arrival` (the open-model
    scheduler and its guardrails), `slo` (the run's window, judging within a patience, merging
    buckets) and `streams`. Keep these stable; a change to them is a change to every pack.
- **Mocks**: stormbreaker has none of its own. Dependencies nobody in the environment owns are mocked
  with axx's `ghcr.io/nimbusxr/axx-wiremock`, fed a data-only OCI image of WireMock mappings. A mock is a proxy too: its stubs answer what they match, and
  `--proxy-all` passes everything else on to the real dependency, contract checked (ADR 0007).
- `examples/`: a Go module of its own, so it stays out of the module that projects' axx builds
  download. It holds:
  - the lab's services (`parcels`, `tracking`) and their teams' dispatchers;
  - the kind lab (`lab/`; `lab/scripts/local.sh` runs a subset on one machine);
  - the acceptance suite (`acceptance/`), which lists stormbreaker's packs by path.
- `internal/labtest`: an orchestrator and dispatchers in one process, for the packs' tests.
  `internal/catalog` holds the frozen step catalog's test.
- `docs/`: the Starlight site, set up as axx's is, with the same hero axe.
  - `docs/scripts/gen.mjs` runs `internal/tools/docsgen`, which writes the packs' reference pages and
    the step index from the packs' manifests.
  - `docs/adr`: the decisions.
  - Diagrams are drawn in characters, in steps a reader clicks through: `docs/src/lib/ascii-diagram.ts`
    is the engine, and the scenes are in `docs/src/components/diagrams/scenes*.ts`. Each diagram's
    description is its text in the Markdown twins and llms.txt. The topology builder
    (`docs/src/lib/topology.ts`) draws a reader's own environment with the same engine.
  - The site is an S3 bucket behind CloudFront (ADR 0028): `docs/cloudfront/router.js` gives agents a page's
    Markdown twin at its own address, and `docs/scripts/deploy.sh` publishes it from the docs workflow.
    `docs/public/webmcp.js` offers the docs to agents that browse them.
- `plugins/stormbreaker`, `.claude-plugin`: the skills for coding agents, as a Claude Code plugin.

## Rules

- Step expression text is public API. Never change existing step text by accident; add new steps
  instead.
  - `testdata/steps.json` is the frozen catalog of every pack's step text: **never edit it by hand**.
  - A new step joins it with `go test ./internal/catalog -run TestStepCatalog -update`, which only adds.
  - Removing one is a decision (`-prune`).
- The orchestrator's API (`api/orchestrator.openapi.yaml`), the notification contract and the
  dispatcher SDK are public contracts too. Changing one needs an ADR. Every orchestrator route is in
  the spec, and the handler tests validate their responses against it.
- The orchestrator never blocks traffic itself: the readiness gate is a fact the suite reads. Every
  transition is durable before it is fanned out, and every phase is idempotent.
- A dispatcher's declaration must be a pure function of the footprint, or prepare rounds never
  converge.
- Nothing test-shaped runs in the environment's cluster except the orchestrator, the dispatchers and
  the mocks. The suite runs on the CI runner.
- Never hand-edit generated files.
- Packs depend on axx's public packages only (`core`, `app`, the packs' public contexts). Something a
  pack needs that axx keeps internal is a change to axx, not a copy.
- A step's `{string}` that sets or checks something is a value, by axx's rule (its ADR 0014): declare it in `TextArgs` or
  `TypedArgs`. `TestEveryStringSaysWhatItHolds` holds the packs to it.
- Keep `CGO_ENABLED=0` builds working: pure-Go dependencies only.
- No CI job runs past 10 minutes, and each test step's timeout says so (`-timeout 10m`).
- The `hygiene` check (`scripts/hygiene.sh`) must pass. It blocks references to unrelated
  organizations and internal infrastructure.

## Commands

```sh
mise run test          # go test -race ./...
mise run lint          # golangci-lint run
mise run generate      # go generate ./...
mise run hygiene       # denylist + gitleaks
go build -o bin/stormbreaker ./cmd/stormbreaker
go test ./internal/catalog -run TestStepCatalog -update   # a new step joins the catalog
cd docs && npm ci && npm run build                        # the docs site (generates its reference first)
cd docs && npm run check:gherkin                          # every Gherkin block in the docs and skills: real steps, readable values
examples/lab/scripts/local.sh                             # the lab on one machine; then axx run in examples/acceptance
scripts/scale-enterprise.sh                               # an enterprise's peak hour against a 2-CPU orchestrator container (Docker)
```
