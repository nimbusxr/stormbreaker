# Security Policy

## Supported versions

During the `0.x` beta, only the latest release receives security fixes.

## Reporting a vulnerability

Please **do not** open a public issue. Report privately through GitHub:
**Security → Report a vulnerability** on <https://github.com/nimbusxr/stormbreaker/security>.

We aim to acknowledge reports within 3 business days and to publish a fix and advisory
within 90 days, crediting the reporter unless they prefer otherwise.

## Scope notes

stormbreaker is built for shared test environments, never production. The orchestrator stores
the dispatchers' webhook secrets so that it can sign notifications, and dispatchers run the
prepare and cleanup code their teams give them, with that team's access to its own stores.
Neither is a vulnerability in itself; please report ways a component reaches data, hosts or
secrets that its configuration did not give it, or ways one team's run can act on another
team's data.
