# 0028: The docs site on S3 and CloudFront

- Status: Accepted
- Date: 2026-11-01

## Context

Coding agents read these docs. Their fetchers rank `text/markdown` first, and a page has a Markdown twin that is a fraction of its HTML, so one address should answer in the format a request prefers. A page that moved should say so with a real redirect, not a page that refreshes with a 200. Both take a host that chooses for each request. axx's site is hosted the same way (its ADR 0005).

## Decision

- **The site is an S3 bucket behind CloudFront**, at `stormbreaker.nimbusxr.us`.
- **A page's address answers in the format the request prefers.** A browser gets the page, and a request that ranks `text/markdown` first gets its Markdown twin. A CloudFront function (`docs/cloudfront/router.js`) chooses, and sends a 301 for a page that moved, from a KeyValueStore the deploy keeps.
- **The home page's twin points agents at axx.** It says what stormbreaker is and that axx answers for its steps offline, with no list of pages to follow: in axx's evaluations, agents followed those instead of asking axx. `llms.txt` lists every page.
- **Agents that browse get tools.** `docs/public/webmcp.js` offers the packs, the steps, search and pages through WebMCP, in Chrome's origin trial; other browsers are left alone.
- **The docs workflow publishes the site** (`docs/scripts/deploy.sh`): the files with their content types, the moved pages into the store, the router when it changed, then an invalidation. It waits for `STORMBREAKER_PUBLISH`, like every release.
- **The deploy has no stored AWS keys.** The job's OIDC token assumes a role that trusts this repository's `main` only, for an hour.

## Consequences

- The AWS resources live outside this repository, and have to exist before the first deploy: the bucket, the distribution with its certificate and DNS, the router function with its KeyValueStore, and the role.
- The distribution sends `Vary: Accept`, and answers a missing page with the site's 404.
- Repository variables say where the site goes: `DOCS_ROLE`, `DOCS_BUCKET`, `DOCS_DISTRIBUTION`, `DOCS_ROUTER` and `DOCS_MOVED`.
- `docs/cloudfront/router_test.mjs` runs in the docs build.
- The WebMCP token covers `nimbusxr.us` and its subdomains, and expires on 2027-03-30.

## Alternatives

**GitHub Pages.** It serves one file per address, whatever a request asks for: an agent would read a page's HTML, sidebar included, and a moved page could only refresh.

**AWS's credentials action.** The exchange is two commands in the job, and this way no third party's code handles the token.
