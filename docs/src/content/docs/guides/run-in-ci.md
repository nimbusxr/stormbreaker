---
title: Run in CI
description: Run operational suites from the CI runner against the environment, with the orchestrator's address and token as secrets.
---

Operational suites run on the CI runner, never in the cluster: the runner reaches the services' front doors, their topics, their metrics and the orchestrator directly, with no proxy, port-forward or tunnel. That reach is a requirement of the environment.

```yaml title=".github/workflows/smoke.yml"
jobs:
  smoke:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v5
      - run: curl -fsSL https://axx.nimbusxr.us/install.sh | sh
      - run: axx run
        working-directory: acceptance
        env:
          STORMBREAKER_ORCHESTRATOR_URL: ${{ vars.STAGING_ORCHESTRATOR_URL }}
          STORMBREAKER_TOKEN: ${{ secrets.STAGING_ORCHESTRATOR_TOKEN }}
```

- **Cache axx's builds.** axx builds itself with the suite's packs once per list of packs; keep its cache (`~/.cache/axx`) between runs.
- **The results are axx's.** That means its exit code, JUnit and Cucumber reports, with each run's journal and client view attached.
- **Size the runner for the load.** One runner sends about 20,000 HTTP requests a second for each core, 100,000 or more on a large machine ([scale](/explanations/scale/)); beyond that, a run needs several ([roadmap](/explanations/roadmap/)).
