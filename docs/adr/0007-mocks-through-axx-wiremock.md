# 0007: Partners are mocked with axx's WireMock image

- Status: Accepted
- Date: 2026-11-01

## Context

Partners nobody in the environment owns must be stood in for. A mock's behavior must be reproducible and must never change at runtime. Teams must not build anything runnable for it.

## Decision

- **Stormbreaker ships no mock server.** Partners are mocked with axx's `axx-wiremock` image, whose OpenAPI validation checks every call against the partner's contract.
- **A mock's behavior is a data-only image** of WireMock mappings, pinned by digest, mounted read-only as an image volume (or copied by an init container). A profile is a folder of it, picked with `--root-dir`.
- **One mock per partner per environment**, inside the cluster only. Behavior changes by redeploying.
- **A mock is a proxy too.** Its stubs answer what they match. `--proxy-all` adds a catch-all at a lower priority than any stub, so everything else falls through to the real dependency, and the URL can stay pointed at the mock. Traffic outside the tests goes where it always went.
- **Validation stays on for what passes through.** A real dependency that breaks its contract is a failure someone should see.

## Consequences

A call no stub matches reaches the real dependency, load included, so a suite's stubs must cover its calls. A degraded profile changes only what its stubs match, but redeploying a mock interrupts all that passes through it. Its journal holds other teams' traffic, credentials included, so it is bounded and its admin API stays in the cluster. A per-pod mock was given up because WireMock keeps scenario state in one process. Priming a mock per run is open.
