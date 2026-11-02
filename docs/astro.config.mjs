// @ts-check
import { fileURLToPath } from 'node:url';
import { satteri } from '@astrojs/markdown-satteri';
import starlight from '@astrojs/starlight';
import { ExpressiveCodeTheme } from '@astrojs/starlight/expressive-code';
import { defineConfig } from 'astro/config';
import starlightLinksValidator from 'starlight-links-validator';
import starlightLlmsTxt from 'starlight-llms-txt';
import { consoleLang } from './src/lib/console.mjs';
import { gherkinSteps } from './src/lib/gherkin.mjs';
import { aliases, pageSlugs } from './src/lib/aliases.mjs';
import { docsBuild, siteBase } from './src/lib/site-base.mjs';
import { stepParams } from './src/lib/step-params.mjs';
import { stepTokensPlugin } from './src/lib/step-tokens-plugin.mjs';
import { poseEditor } from './scripts/pose-editor.mjs';

const site = 'https://stormbreaker.nimbusxr.us';

/** Chrome's WebMCP origin trial (feature WebMCP, https://nimbusxr.us and subdomains, until 2027-03-30). */
const webmcpTrialToken =
	'Au6+vW+zV403Rr8nG3rt7t4kt0d4TKhCO//DVEhDvH2tcoxBvC/HCQ7Bk5aJRg6KMMKAdc82K1Eux4vwRQl1SQ0AAABeeyJvcmlnaW4iOiJodHRwczovL25pbWJ1c3hyLnVzOjQ0MyIsImZlYXR1cmUiOiJXZWJNQ1AiLCJleHBpcnkiOjE4MDYzNjQ4MDAsImlzU3ViZG9tYWluIjp0cnVlfQ==';

// The latest release's docs, at the root, or main's, at /next/ (src/lib/site-base.mjs).
const build = docsBuild();
/** A site-absolute path below the base. */
const at = (path) => build.base.replace(/\/$/, '') + path;

/** Where the parameter values of the docs' steps are (scripts/step-tokens.mjs). */
const stepTokens = fileURLToPath(new URL('./src/lib/_gen/step-tokens.json', import.meta.url));

/** The sidebar entries of the reference pages of packs. */
const packsOf = (names) => names.map((name) => `references/packs/${name}`);

export default defineConfig({
	site,
	base: build.base,
	trailingSlash: 'ignore',
	// Astro puts a redirect below the base, but not where it leads. The aliases are
	// where agents look for pages (/packs/rest, /reference/..., /guides/mongodb).
	redirects: {
		...Object.fromEntries([...aliases(pageSlugs(fileURLToPath(new URL('./src/content/docs/', import.meta.url))))].map(([alias, slug]) => [`/${alias}`, at(slug === 'index' ? '/' : `/${slug}/`)])),
	},
	vite: { plugins: [poseEditor()], define: { __SB_DOCS__: JSON.stringify(build) } },
	markdown: { processor: satteri({ mdastPlugins: [stepParams(stepTokens), siteBase(build.base)] }) },
	integrations: [
		starlight({
			title: 'stormbreaker',
			description:
				'Stormbreaker is an open-source framework for operational acceptance testing: smoke and load tests in shared environments, where every team answers for the data its systems create. Built on axx.',
			favicon: '/favicon.svg',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/nimbusxr/stormbreaker' }],
			editLink: { baseUrl: 'https://github.com/nimbusxr/stormbreaker/edit/main/docs/' },
			customCss: ['./src/styles/custom.css', './src/styles/diagrams.css'],
			components: {
				Header: './src/components/Header.astro',
				Hero: './src/components/Hero.astro',
				Footer: './src/components/Footer.astro',
				PageTitle: './src/components/PageTitle.astro',
				ThemeProvider: './src/components/ThemeProvider.astro',
				ThemeSelect: './src/components/ThemeSelect.astro',
			},
			expressiveCode: {
				useDarkModeMediaQuery: false,
				// The data of steps: parameter values in each theme's variable.parameter color, the names of a
				// table in its property-name color, and a table's pipes in its punctuation color.
				plugins: [
					stepTokensPlugin(stepTokens, [
						{ value: '#ffa76b', name: '#6cc3ff', punctuation: '#7d8aa3' },
						{ value: '#a2400a', name: '#1450b8', punctuation: '#5f6b85' },
					]),
				],
				shiki: {
					langs: [consoleLang, gherkinSteps],
					langAlias: { console: 'sb-console' },
				},
				themes: [new ExpressiveCodeTheme({
					name: 'sb-thunderhead',
					type: 'dark',
					colors: {
						'editor.background': '#0a101c',
						'editor.foreground': '#a9b6cc',
						'editor.selectionBackground': '#1d2b45',
						'editorError.foreground': '#ff7d7d',
						'editorWarning.foreground': '#ffad5c',
						'editorInfo.foreground': '#6cc3ff',
						'terminal.ansiRed': '#ff7d7d',
						'terminal.ansiGreen': '#6fd3a3',
						'terminal.ansiYellow': '#ffad5c',
						'terminal.ansiBlue': '#6cc3ff',
						'terminal.ansiMagenta': '#b49cff',
						'terminal.ansiCyan': '#5ad1d1',
						'terminal.ansiBlack': '#1e273a',
						'terminal.ansiWhite': '#a9b6cc',
						'terminal.ansiBrightBlack': '#5f6d88',
						'terminal.ansiBrightRed': '#ffa8a8',
						'terminal.ansiBrightGreen': '#9fe5c2',
						'terminal.ansiBrightYellow': '#ffc98f',
						'terminal.ansiBrightBlue': '#a8dbff',
						'terminal.ansiBrightMagenta': '#cfc0ff',
						'terminal.ansiBrightCyan': '#8be4e4',
						'terminal.ansiBrightWhite': '#d9e2f1',
					},
					tokenColors: [
						{ scope: ['comment'], settings: { foreground: '#5f6d88' } },
						{ scope: ['keyword', 'storage'], settings: { foreground: '#b49cff' } },
						{ scope: ['string'], settings: { foreground: '#9fd88a' } },
						{ scope: ['constant'], settings: { foreground: '#ffad5c' } },
						{ scope: ['entity', 'support.function'], settings: { foreground: '#6cc3ff' } },
						{ scope: ['entity.name.type', 'support.type', 'support.class'], settings: { foreground: '#5ad1d1' } },
						{ scope: ['variable.parameter', 'variable.other.constant'], settings: { foreground: '#ffa76b' } },
						{ scope: ['support.type.property-name', 'entity.name.tag', 'string.unquoted.plain.out.yaml'], settings: { foreground: '#6cc3ff' } },
						{ scope: ['punctuation'], settings: { foreground: '#7d8aa3' } },
						{ scope: ['markup.inserted'], settings: { foreground: '#6fd3a3' } },
						{ scope: ['markup.deleted', 'invalid'], settings: { foreground: '#ff7d7d' } },
						{ scope: ['markup.warning'], settings: { foreground: '#ffad5c' } },
					],
				}), new ExpressiveCodeTheme({
					name: 'sb-overcast',
					type: 'light',
					colors: {
						'editor.background': '#dae1eb',
						'editor.foreground': '#1e2a40',
						'editor.selectionBackground': '#bccbe2',
						'editorError.foreground': '#b0213a',
						'editorWarning.foreground': '#9a4a00',
						'editorInfo.foreground': '#1450b8',
						'terminal.ansiRed': '#b0213a',
						'terminal.ansiGreen': '#0b6b3a',
						'terminal.ansiYellow': '#9a4a00',
						'terminal.ansiBlue': '#1450b8',
						'terminal.ansiMagenta': '#5b34b8',
						'terminal.ansiCyan': '#00666a',
						'terminal.ansiBlack': '#b6c0d2',
						'terminal.ansiWhite': '#1e2a40',
						'terminal.ansiBrightBlack': '#5f6b85',
						'terminal.ansiBrightRed': '#941a30',
						'terminal.ansiBrightGreen': '#075a30',
						'terminal.ansiBrightYellow': '#833d00',
						'terminal.ansiBrightBlue': '#0f4bb0',
						'terminal.ansiBrightMagenta': '#4c2a9e',
						'terminal.ansiBrightCyan': '#00585c',
						'terminal.ansiBrightWhite': '#111a2b',
					},
					tokenColors: [
						{ scope: ['comment'], settings: { foreground: '#5f6b85' } },
						{ scope: ['keyword', 'storage'], settings: { foreground: '#5b34b8' } },
						{ scope: ['string'], settings: { foreground: '#2f6b12' } },
						{ scope: ['constant'], settings: { foreground: '#9a4a00' } },
						{ scope: ['entity', 'support.function'], settings: { foreground: '#1450b8' } },
						{ scope: ['entity.name.type', 'support.type', 'support.class'], settings: { foreground: '#00666a' } },
						{ scope: ['variable.parameter', 'variable.other.constant'], settings: { foreground: '#a2400a' } },
						{ scope: ['support.type.property-name', 'entity.name.tag', 'string.unquoted.plain.out.yaml'], settings: { foreground: '#1450b8' } },
						{ scope: ['punctuation'], settings: { foreground: '#5f6b85' } },
						{ scope: ['markup.inserted'], settings: { foreground: '#0b6b3a' } },
						{ scope: ['markup.deleted', 'invalid'], settings: { foreground: '#b0213a' } },
						{ scope: ['markup.warning'], settings: { foreground: '#9a4a00' } },
					],
				})],
				styleOverrides: {
					codeBackground: ['#0a101c', '#dae1eb'],
					borderColor: ['#1e273a', '#b9c3d4'],
					borderRadius: '0.75rem',
					codeFontSize: '0.8125rem',
					textMarkers: {
						markBackground: ['#6cc3ff14', '#1859c922'],
						markBorderColor: ['#6cc3ff55', '#1859c977'],
						insBackground: ['#6fd3a314', '#0b6b3a22'],
						insBorderColor: ['#6fd3a355', '#0b6b3a77'],
						insDiffIndicatorColor: ['#6fd3a3', '#0b6b3a'],
						delBackground: ['#ff7d7d14', '#b0213a22'],
						delBorderColor: ['#ff7d7d55', '#b0213a77'],
						delDiffIndicatorColor: ['#ff7d7d', '#b0213a'],
					},
					frames: {
						editorActiveTabBackground: ['#0f1626', '#d3dbe7'],
						editorActiveTabForeground: ['#b3c0d6', '#33405a'],
						editorTabBarBackground: ['#0c1220', '#cfd7e3'],
						terminalBackground: ['#0a101c', '#dae1eb'],
						terminalTitlebarBackground: ['#0f1626', '#d3dbe7'],
						terminalTitlebarForeground: ['#b3c0d6', '#33405a'],
						tooltipSuccessBackground: ['#0d2019', '#c5e3d1'],
						tooltipSuccessForeground: ['#9fe5c2', '#075a30'],
					},
				},
			},
			routeMiddleware: './src/route-data.ts',
			head: [
				{ tag: 'meta', attrs: { name: 'theme-color', content: '#070b14' } },
				{ tag: 'meta', attrs: { name: 'color-scheme', content: 'dark light' } },
				{ tag: 'link', attrs: { rel: 'preload', href: at('/fonts/inter-latin.woff'), as: 'font', type: 'font/woff', crossorigin: 'anonymous' } },
				{
					tag: 'link',
					attrs: { rel: 'alternate', type: 'text/plain', title: 'llms.txt', href: at('/llms.txt') },
				},
				// The docs' tools for agents that browse them, through WebMCP (public/webmcp.js),
				// which Chrome offers in an origin trial: its token, for https://nimbusxr.us and
				// its subdomains, expires on 2027-03-30 (renew it at
				// https://developer.chrome.com/origintrials before then).
				{ tag: 'meta', attrs: { 'http-equiv': 'origin-trial', content: webmcpTrialToken } },
				{ tag: 'script', attrs: { type: 'module', src: at('/webmcp.js') } },
				// Main's docs are a preview: search engines keep to the release's.
				...(build.channel === 'next' ? [{ tag: 'meta', attrs: { name: 'robots', content: 'noindex' } }] : []),
			],
			sidebar: [
				{
					label: 'Tutorials',
					items: ['tutorials/quickstart', 'tutorials/first-run'],
				},
				{
					label: 'Guides',
					items: [
						{ label: 'Set up', items: ['guides/roll-it-out', 'guides/see-your-topology', 'guides/install', 'guides/deploy-the-orchestrator', 'guides/watch-in-the-ui', 'guides/write-a-dispatcher', 'guides/run-in-ci'] },
						{ label: 'Test', items: ['guides/smoke-after-rollout', 'guides/open-a-run', 'guides/define-streams', 'guides/drive-rest-load', 'guides/drive-kafka-load', 'guides/check-slos', 'guides/mock-partners', 'guides/write-a-pack'] },
						{ label: 'Clean up', items: ['guides/clean-up-later'] },
					],
				},
				{
					label: 'References',
					items: [
						// The packs' pages and the step index are generated (scripts/gen.mjs).
						{ label: 'Packs', items: packsOf(['stormbreaker-core', 'stormbreaker-rest', 'stormbreaker-kafka', 'stormbreaker-prometheus']) },
						'references/step-index',
						'references/config',
						'references/orchestrator-api',
						'references/cli',
					],
				},
				{
					label: 'Explanations',
					items: [
						'explanations/why-stormbreaker',
						'explanations/around-your-applications',
						'explanations/testing-philosophy',
						'explanations/the-problems',
						'explanations/run-lifecycle',
						'explanations/topology',
						'explanations/organization',
						'explanations/measurement-honesty',
						'explanations/guarantees',
						'explanations/scale',
						'explanations/roadmap',
						'explanations/adrs',
					],
				},
			],
			plugins: [
				starlightLinksValidator({
					// Files served from public/ are not pages. And the home page's hero links, which are
					// relative to it to be right in the release's docs and in main's (/next/): front
					// matter is not rebased.
					exclude: [
						'/openapi/**', '/webmcp.js', '/llms.txt', '/llms-full.txt', '/llms-small.txt', '/**/*.md', '/index.md',
						'tutorials/quickstart/', 'explanations/why-stormbreaker/',
					],
				}),
				starlightLlmsTxt({
					projectName: 'Stormbreaker',
					// A diagram reaches text-only readers as its description (src/components/diagrams).
					customSelectors: { all: ['.dg-svg', '.dg-legend'] },
					description:
						"Stormbreaker (github.com/nimbusxr/stormbreaker) is an open-source framework for operational acceptance testing, smoke and load tests, in shared environments where every team answers for the data its systems create. It is built on axx: suites are axx suites and its steps are axx packs; an orchestrator per environment coordinates runs, and each team's dispatcher prepares before a run and cleans up after it.",
					details: [
						"- With axx installed, in a project that lists stormbreaker's packs, axx answers for their steps, offline and for its version, in less than these pages take: `axx steps` (every step of the project's packs), `axx steps show <id>` (a step's documentation), `axx explain <code>` (an error). Prefer them to this site. `stormbreaker --help` says what its own command does.",
						'- Every page is also available as Markdown: append `.md` to its path (for example `https://stormbreaker.nimbusxr.us/tutorials/quickstart.md`).',
						"- Never invent step text: the step index (`/references/step-index/`) lists every step of stormbreaker's packs, or run `axx steps`, then check with `axx validate`; axx's own are at https://axx.nimbusxr.us.",
						"- The orchestrator's HTTP contract: `/openapi/orchestrator.yaml`.",
						'- Stormbreaker is pre-release (v0.x): interfaces may change before v1.0.0.',
					].join('\n'),
					customSets: [
						{ label: 'Tutorials', paths: ['tutorials/**'], description: 'the local lab, and a first run of your own' },
						{ label: 'Guides', paths: ['guides/**'], description: 'install, the orchestrator, dispatchers, runs, streams, REST and Kafka load, SLO checks, mocks, smoke after a rollout, cleaning up later, CI' },
						{ label: 'References', paths: ['references/**'], description: "the packs (generated), the step index, configuration, the orchestrator's API, the command line" },
						{ label: 'Explanations', paths: ['explanations/**'], description: 'why stormbreaker exists, its philosophy, the problems it answers, the run lifecycle, topology, measurement honesty, guarantees, scale' },
					],
					promote: ['index*', 'explanations/why-stormbreaker', 'tutorials/**'],
					demote: ['explanations/{roadmap,adrs}'],
					exclude: ['explanations/adrs'],
				}),
			],
		}),
	],
});
