// Where agents look for pages: /packs/stormbreaker-rest, /reference/packs/..., /guides/kafka.md,
// /steps/stormbreaker-core, /docs/... aliases() maps each such path to the page it means. The site
// redirects the pages' aliases (astro.config.mjs) and serves the Markdown twins at them too
// (src/pages/[...slug].md.ts), so a guess lands either way.

import fs from 'node:fs';
import path from 'node:path';

/** The topics agents name guides by, and the guide each means. */
export const guideTopics = {
	dispatcher: 'write-a-dispatcher',
	dispatchers: 'write-a-dispatcher',
	'dispatcher-sdk': 'write-a-dispatcher',
	orchestrator: 'deploy-the-orchestrator',
	runs: 'open-a-run',
	tags: 'open-a-run',
	streams: 'define-streams',
	'test-data': 'define-streams',
	fixtures: 'define-streams',
	rest: 'drive-rest-load',
	load: 'drive-rest-load',
	kafka: 'drive-kafka-load',
	prometheus: 'check-slos',
	slo: 'check-slos',
	slos: 'check-slos',
	metrics: 'check-slos',
	mocks: 'mock-partners',
	mock: 'mock-partners',
	wiremock: 'mock-partners',
	smoke: 'smoke-after-rollout',
	cleanup: 'clean-up-later',
	ci: 'run-in-ci',
};

/**
 * The aliases of the docs' pages, given the pages' slugs (references/packs/rest,
 * guides/seed-mongodb, ...): a Map of each alias to the slug it means.
 * @param {string[]} slugs
 * @returns {Map<string, string>}
 */
export function aliases(slugs) {
	const pages = new Set(slugs);
	const out = new Map();
	for (const slug of slugs) {
		// /packs/rest and /reference/packs/rest: the reference is /references/.
		if (slug === 'references/packs' || slug.startsWith('references/packs/')) out.set(slug.slice('references/'.length), slug);
		if (slug === 'references' || slug.startsWith('references/')) out.set(`reference${slug.slice('references'.length)}`, slug);
	}
	for (const [topic, guide] of Object.entries(guideTopics)) {
		const slug = `guides/${guide}`;
		if (!pages.has(slug)) throw new Error(`docs/src/lib/aliases.mjs: guides/${topic} leads to ${slug}, which is no page`);
		if (pages.has(`guides/${topic}`)) throw new Error(`docs/src/lib/aliases.mjs: guides/${topic} is a page of its own`);
		out.set(`guides/${topic}`, slug);
	}
	// /steps/kafka: a pack's steps are its reference page; /steps, all of them.
	for (const slug of slugs) {
		if (slug.startsWith('references/packs/')) out.set(`steps/${slug.slice('references/packs/'.length)}`, slug);
	}
	for (const [alias, slug] of Object.entries(pageAliases)) {
		if (slug !== 'index' && !pages.has(slug)) throw new Error(`docs/src/lib/aliases.mjs: ${alias} leads to ${slug}, which is no page`);
		if (pages.has(alias)) throw new Error(`docs/src/lib/aliases.mjs: ${alias} is a page of its own`);
		out.set(alias, slug);
	}
	// /docs and /docs/...: the whole site is the docs, so every page and alias
	// is there too (/docs/packs/sql).
	out.set('docs', 'index');
	for (const slug of slugs) {
		if (slug !== 'index') out.set(`docs/${slug}`, slug);
	}
	for (const [alias, slug] of [...out]) {
		if (!alias.startsWith('docs')) out.set(`docs/${alias}`, slug);
	}
	return out;
}

/** Other pages agents named, and the page each means ('index': the landing page). */
export const pageAliases = {
	steps: 'references/step-index',
	guides: 'index', // the guides have no index page: the landing page and its sidebar list them
	'references/configuration': 'references/config',
	'reference/configuration': 'references/config',
	'references/openapi': 'references/orchestrator-api',
	api: 'references/orchestrator-api',
};

/**
 * The slugs of the docs' pages, read from src/content/docs/: the generated ones
 * (references/_gen/, written by `npm run gen`) at the URLs gen.mjs gives them.
 * @param {string} docsDir
 * @returns {string[]}
 */
export function pageSlugs(docsDir) {
	const out = [];
	const walk = (dir) => {
		for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
			const file = path.join(dir, e.name);
			if (e.isDirectory()) walk(file);
			else if (/\.mdx?$/.test(e.name)) {
				const rel = path.relative(docsDir, file).split(path.sep).join('/');
				const slug = rel.replace(/\.mdx?$/, '').replace(/^references\/_gen(\/|$)/, 'references$1').replace(/(^|\/)index$/, '');
				if (slug) out.push(slug);
			}
		}
	};
	walk(docsDir);
	return out;
}
