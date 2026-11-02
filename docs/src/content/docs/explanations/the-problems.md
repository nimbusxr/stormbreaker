---
title: The problems
description: The problems operational testing in a shared environment has to solve, and the decision stormbreaker made for each.
---

Every decision in stormbreaker was checked against these problems.

## The problems

1. **Deployment and scoping.** What runs where, and who owns it.
2. **Execution and placement.** Where suites run, and how they reach what they test.
3. **Dependency emulation.** Standing in for dependencies with no shared mock state across regions or replicas.
4. **Config fidelity.** Testing production-shaped configuration.
5. **Synthetic data identification.** Test data must look real. How then does anyone know what is synthetic?
6. **Run lifecycle.** Runs are concurrent, and there are no reserved windows.
7. **Cleanup.** Knowing a run's whole footprint, without ever touching another team's data.
8. **Verification honesty.** Verdicts that mean what they say.
9. **Security and compliance.** Test data must stay looks-real but safe, admin surfaces stay out of reach, and nothing runs in production.
10. **Scale.** Protecting real external partners from test load.

## The decisions

| Hard problem | Decision |
|---|---|
| Signalling runs and coordinating cleanup, without a streaming platform | An orchestrator per environment, with signed webhooks and an event feed: every team already knows how to run a webhook receiver |
| Getting a mock its behavior, reproducibly, with no changes at runtime | Mock images: data-only, pinned by digest, mounted into axx's WireMock image; behavior changes by redeploying |
| Where suites run | On the CI runner, reaching the environment directly: nothing test-shaped in the cluster but the orchestrator, the dispatchers and the mocks |
| Judging SLOs honestly across regions | Named sources over every region; counters summed, histogram buckets merged, windows refused when too short |
| Valid, collision-free volume data, declared not invented | Streams over the fixture factories, deterministic per record, linted for collisions |
| Driving load at honest rates, over HTTP and Kafka, from the suite | An open-model scheduler in the suite, with the client view recorded |
| Knowing the footprint when data carries no marker | The run declares what its streams create; each team declares what its systems derive from it, in rounds; nobody guesses |
| The glue (per-environment facts, ownership, smoke data, reference data) | Mostly dissolved: environment variables, namespaces, ordinary test authoring |
| Stores where delete does not exist | Teams steward them: removed and verified, or clean by a date |

The [ADRs](/explanations/adrs/) record each decision, with what it was chosen over.
