#!/usr/bin/env node
// Generates the reference material from stormbreaker's code, the single source of truth. Run by
// `npm run build` and `npm run dev`.
//
//   src/content/docs/references/_gen/   the packs' pages and the step index (internal/tools/docsgen)
//   public/openapi/orchestrator.yaml   the orchestrator's OpenAPI document (api/)
//   src/lib/_gen/step-tokens.json      the parameter values of the docs' steps, for the site to
//                                      color (none yet: the blocks are colored by their grammar)
//
// All outputs are gitignored. The script fails if any of them comes out empty: a docs site that
// silently publishes an empty reference is worse than no build.

import { execFileSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const docsDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoDir = path.resolve(docsDir, '..');
const genDir = path.join(docsDir, 'src/content/docs/references/_gen');

const log = (msg) => console.log(`gen: ${msg}`);
function fail(msg) {
	console.error(`gen: error: ${msg}`);
	process.exit(1);
}

fs.rmSync(genDir, { recursive: true, force: true });
log('writing the packs\' pages and the step index (go run ./internal/tools/docsgen)');
try {
	execFileSync('go', ['run', './internal/tools/docsgen', '-out', genDir], { cwd: repoDir, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0' } });
} catch {
	fail('`go run ./internal/tools/docsgen` failed. Use the Go version the repository pins (`mise exec -- npm run build`).');
}

const openapiDir = path.join(docsDir, 'public/openapi');
fs.mkdirSync(openapiDir, { recursive: true });
fs.copyFileSync(path.join(repoDir, 'api/orchestrator.openapi.yaml'), path.join(openapiDir, 'orchestrator.yaml'));

const tokensDir = path.join(docsDir, 'src/lib/_gen');
fs.mkdirSync(tokensDir, { recursive: true });
fs.writeFileSync(path.join(tokensDir, 'step-tokens.json'), JSON.stringify({ blocks: {} }) + '\n');

const packs = fs.readdirSync(path.join(genDir, 'packs')).filter((n) => n.endsWith('.md'));
for (const [what, items] of [
	['pack pages (references/_gen/packs/*.md)', packs],
	['the step index', fs.existsSync(path.join(genDir, 'step-index.md')) ? ['step-index.md'] : []],
	['the orchestrator\'s OpenAPI document', fs.statSync(path.join(openapiDir, 'orchestrator.yaml')).size > 0 ? ['orchestrator.yaml'] : []],
]) {
	if (items.length === 0) fail(`no ${what}`);
	log(`${items.length} ${what}`);
}
