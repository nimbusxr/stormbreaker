/**
 * Which docs a build is (.github/workflows/docs.yml): the latest release's,
 * at the site's root, or main's, at /next/. A build without STORMBREAKER_DOCS_CHANNEL
 * (a local one) is neither, at the root.
 */
export function docsBuild(env = process.env) {
	const channel = env.STORMBREAKER_DOCS_CHANNEL === 'latest' || env.STORMBREAKER_DOCS_CHANNEL === 'next' ? env.STORMBREAKER_DOCS_CHANNEL : '';
	return {
		channel,
		// The latest release, like v0.1.1: the one the root's docs are.
		release: env.STORMBREAKER_DOCS_RELEASE ?? '',
		base: channel === 'next' ? '/next/' : '/',
	};
}

/**
 * Puts the site's base path in front of the docs' site-absolute links
 * (/guides/install/), whose pages are below it in main's docs.
 */
export function siteBase(base) {
	const prefix = base.replace(/\/$/, '');
	const rebase = (url) => (prefix && typeof url === 'string' && url.startsWith('/') && !url.startsWith('//') && url !== prefix && !url.startsWith(`${prefix}/`) ? prefix + url : undefined);
	const node = (n) => {
		const url = rebase(n.url);
		return url ? { ...n, url } : undefined;
	};
	return { name: 'sb-site-base', link: node, definition: node, image: node };
}
