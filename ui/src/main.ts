/**
 * The stormbreaker UI (ADR 0026, 0027): an environment and its runs, as its orchestrator has
 * them, as they go. It reads through its server (internal/ui), which holds the orchestrators'
 * tokens, shares their reads among viewers, and streams each environment once a second while
 * anyone watches. Nothing here is drawn by hand: every row, figure and box is a read.
 */
import { esc } from './format';
import { Live } from './live';
import { environmentView } from './views/environment';
import { runView } from './views/run';

const app = document.getElementById('app')!;

async function read<T>(env: string, path: string): Promise<T> {
	const r = await fetch(`/api/env/${encodeURIComponent(env)}/${path}`);
	if (!r.ok) throw new Error(`${r.status} ${(await r.text()).trim()}`);
	return r.json() as Promise<T>;
}
async function readText(env: string, path: string): Promise<string> {
	const r = await fetch(`/api/env/${encodeURIComponent(env)}/${path}`);
	if (!r.ok) throw new Error(`${r.status}`);
	const body = await r.text();
	try {
		const v = JSON.parse(body);
		return typeof v === 'string' ? v : body;
	} catch {
		return body;
	}
}

let environments: string[] = [];
let live: Live | undefined;
let leave: (() => void) | undefined;

function shell(env: string): HTMLElement {
	const tabs = environments.length > 1
		? `<nav class="envs" aria-label="Environments">${environments.map((e) => `<a href="/env/${esc(e)}" data-route${e === env ? ' aria-current="page"' : ''}>${esc(e)}</a>`).join('')}</nav>`
		: `<span class="env">${esc(env)}</span>`;
	app.innerHTML = `
		<header class="top">
			<a class="mark" href="/env/${esc(env)}" data-route><span>storm</span><b>breaker</b></a>
			${tabs}
			<div class="status" data-status role="status" aria-live="polite"></div>
		</header>
		<div class="banner" data-banner hidden></div>
		<main data-main></main>`;
	return app.querySelector<HTMLElement>('[data-main]')!;
}

/** The top bar's word on the live view: live and how fresh, or why not. */
function status() {
	const el = app.querySelector<HTMLElement>('[data-status]');
	const banner = app.querySelector<HTMLElement>('[data-banner]');
	if (!el || !live) return;
	const fresh = live.fresh();
	const s = live.snapshot;
	const age = live.receivedAt ? Math.round((Date.now() - live.receivedAt) / 1000) : 0;
	el.className = `status ${fresh ? 'on' : 'off'}`;
	el.innerHTML = fresh
		? `<span class="dot" aria-hidden="true"></span>live <span class="muted">· read ${age < 2 ? 'just now' : `${age}s ago`} · read-only</span>`
		: `<span class="dot" aria-hidden="true"></span>${live.connected ? 'cannot read the orchestrator' : 'reconnecting…'}`;
	if (banner) {
		const why = s?.error ?? (!live.connected && live.receivedAt ? 'The UI lost its stream; it reconnects by itself.' : '');
		banner.hidden = !why;
		banner.textContent = why ? `${why} What you see is what it said last.` : '';
	}
}

function notFound(message: string) {
	app.innerHTML = `<main><div class="quiet"><p>${esc(message)}</p><p><a href="/" data-route>Back to the start</a></p></div></main>`;
}

async function route() {
	leave?.();
	leave = undefined;
	const parts = location.pathname.split('/').filter(Boolean).map(decodeURIComponent);
	try {
		if (!environments.length) environments = (await (await fetch('/api/environments')).json()).environments ?? [];
	} catch (err) {
		return notFound(`Cannot reach the UI's server: ${(err as Error).message}`);
	}
	if (!environments.length) return notFound('The UI has no environment to show.');
	if (parts[0] !== 'env' || !parts[1]) {
		history.replaceState(null, '', `/env/${encodeURIComponent(environments[0])}${location.search}`);
		return route();
	}
	const env = parts[1];
	if (!environments.includes(env)) return notFound(`No environment named ${env}.`);
	if (live?.env !== env) {
		live?.stop();
		live = new Live(env);
		live.start();
	}
	const main = shell(env);
	const offStatus = live.on(status);
	status();
	const leaveView = parts[2] === 'runs' && parts[3]
		? (document.title = `${parts[3]} · ${env} · stormbreaker`, runView(main, live, env, parts[3], (p) => read(env, p), (p) => readText(env, p)))
		: (document.title = `${env} · stormbreaker`, environmentView(main, live, env, (p) => read(env, p)));
	leave = () => {
		offStatus();
		leaveView();
	};
}

document.addEventListener('click', (e) => {
	const a = (e.target as HTMLElement).closest<HTMLAnchorElement>('a[data-route]');
	if (!a || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
	e.preventDefault();
	if (a.getAttribute('href') !== location.pathname + location.search) history.pushState(null, '', a.getAttribute('href')!);
	window.scrollTo({ top: 0 });
	void route();
});
window.addEventListener('popstate', () => void route());
// The freshness in the top bar counts on between reads.
window.setInterval(status, 1000);
void route();
