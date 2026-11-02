import type { CollectionEntry } from 'astro:content';
import { DIAGRAMS, type DiagramName } from '../components/diagrams/descriptions';
import { ORG_DIAGRAMS } from '../components/diagrams/scenes-org';
import { docs, withBase } from './docs-build';

export const SITE = 'https://stormbreaker.nimbusxr.us';

/** Where this build's pages are: the root, or /next/ for main's docs. */
const ROOT = SITE + docs.base.replace(/\/$/, '');

/** Path of the Markdown twin of a docs page: `/tutorials/quickstart.md`, `/index.md`. */
export function markdownPath(id: string): string {
	return withBase(`/${id || 'index'}.md`);
}

/**
 * Turns MDX component markup into plain Markdown. Only the handful of
 * Starlight components the site uses are handled; everything else passes through.
 */
function mdxToMarkdown(body: string): string {
	return body
		.replace(/^import\s.+?from\s+['"].+?['"];?[ \t]*$/gm, '')
		.replace(/^\{\/\*[\s\S]*?\*\/\}[ \t]*$/gm, '')
		.replace(/^<p class="[^"]*">([\s\S]*?)<\/p>[ \t]*$/gm, '> $1')
		.replace(/^[ \t]*<\/?(Tabs|CardGrid)(\s[^>]*)?>[ \t]*$/gm, '')
		.replace(/^[ \t]*<\/(TabItem|Card)>[ \t]*$/gm, '')
		.replace(/^[ \t]*<TabItem\s[^>]*label="([^"]+)"[^>]*>[ \t]*$/gm, '**$1**')
		.replace(/^[ \t]*<Card\s[^>]*title="([^"]+)"[^>]*>[ \t]*$/gm, '### $1')
		.replace(/^[ \t]*<LinkCard\s[^>]*title="([^"]+)"[^>]*href="([^"]+)"[^>]*\/>[ \t]*$/gm, '- [$1]($2)')
		// The home page's components (src/components/home).
		.replace(/^[ \t]*<\/?(FeatureGrid|Flow)(\s[^>]*)?>[ \t]*$/gm, '')
		.replace(/^[ \t]*<\/(Feature|FlowStep)>[ \t]*$/gm, '')
		.replace(/^[ \t]*<Feature\s[^>]*title="([^"]+)"[^>]*href="([^"]+)"[^>]*>[ \t]*$/gm, '### [$1]($2)')
		.replace(/^[ \t]*<(Feature|FlowStep)\s[^>]*title="([^"]+)"[^>]*>[ \t]*$/gm, '### $2')
		// The diagrams (src/components/diagrams), in words.
		.replace(/^[ \t]*<([A-Z]\w*)(\s[^>]*)?\/>[ \t]*$/gm, (tag, name: string) =>
			name in DIAGRAMS ? `> **Diagram: ${DIAGRAMS[name as DiagramName].title}.** ${DIAGRAMS[name as DiagramName].text}`
				: name in ORG_DIAGRAMS ? `> **Diagram: ${ORG_DIAGRAMS[name].title}.** ${ORG_DIAGRAMS[name].text}` : tag)
		.replace(/^[ \t]*<HomeCta\s[^>]*primary="([^"]+)"[^>]*primaryHref="([^"]+)"[^>]*secondary="([^"]+)"[^>]*secondaryHref="([^"]+)"[^>]*\/>[ \t]*$/gm, '- [$1]($2)\n- [$3]($4)');
}

/** Makes root-relative links absolute so the Markdown works outside the site. */
function absolutize(body: string): string {
	return body.replace(/\]\(\/(?!\/)/g, `](${ROOT}/`);
}

/**
 * The home page's twin is where an agent starts: what axx answers offline costs less than these
 * pages. No list of the site's pages and no hero links: in axx's evaluations agents followed them
 * instead of asking axx, and llms.txt has them.
 */
const AGENTS_START =
	"> **Coding agents:** stormbreaker's steps are axx packs, so with axx installed, in a project that lists them, axx answers for them itself, offline and for its version: " +
	"`axx steps` (every step of the project's packs, read once), `axx steps show <id>...` (their documentation), `axx explain <code>` (an error), and " +
	'`axx validate && axx lint && axx run --compact` (check, then run, in one command). `stormbreaker --help` says the same, and what its own command does. ' +
	`[llms.txt](${ROOT}/llms.txt) indexes this site's Markdown.`;

/** The Markdown twin: title, description and the page source; the home page's is AGENTS_START. */
export function toMarkdown(entry: CollectionEntry<'docs'>): string {
	const { title, description, hero } = entry.data as {
		title: string;
		description?: string;
		hero?: { tagline?: string; actions?: { text: string; link: string }[] };
	};
	let body = entry.body ?? '';
	if (entry.filePath?.endsWith('.mdx')) body = mdxToMarkdown(body);
	// Maintainer notes are not content.
	body = body.replace(/^<!-- (TODO\(verify\)|Code generated)[\s\S]*?-->[ \t]*$/gm, '').replace(/\n{3,}/g, '\n\n');
	const parts = [`# ${title}`];
	if (description) parts.push(`> ${description}`);
	if (hero?.tagline) parts.push(hero.tagline);
	// The home page's links are relative to it: from the site's root here.
	const rooted = (link: string) => (/^[a-z]+:|^\//i.test(link) ? link : `/${link}`);
	if (hero?.actions?.length && entry.id !== 'index') parts.push(hero.actions.map((a) => `- [${a.text}](${rooted(a.link)})`).join('\n'));
	parts.push(`Source: ${ROOT}${entry.id === 'index' ? '/' : `/${entry.id}/`}`);
	parts.push(entry.id === 'index' ? AGENTS_START : body.trim());
	return absolutize(parts.join('\n\n')) + '\n';
}
