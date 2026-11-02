// An environment's page: what runs now, the environment as it is, every run before, and every
// dispatcher. The first three follow the live view; older pages of runs are read when asked.
import { ago, count, esc, ms, opener, outcome, plural, rate, span, when } from '../format';
import type { Live } from '../live';
import { currentStep, patch, pill, rateNow, stage, strip, tone } from '../parts';
import { describe, draw, groupScene } from '../scene';
import type { Dispatcher, LiveRun, RunList, RunSummary, Snapshot } from '../types';

const PAGE = 20;

export function environmentView(root: HTMLElement, live: Live, env: string, read: <T>(path: string) => Promise<T>): () => void {
	const params = new URLSearchParams(location.search);
	const group = params.get('group') ?? '';
	const state = params.get('state') ?? '';
	const per = Math.min(100, Math.max(1, Number(params.get('per')) || PAGE));
	// The pages of runs walked back through, newest first: each an opening time to list before.
	const cursors = (params.get('pages') ?? '').split(',').filter(Boolean).map(Number);
	const filtered = Boolean(group || state || cursors.length || per !== PAGE);
	let page: RunList | undefined;
	let pageError = '';

	root.innerHTML = `
		<section class="now" aria-labelledby="now-title"><div class="section-head"><h2 id="now-title">Now</h2><span data-part="now-count" class="muted"></span></div><div data-part="now"></div></section>
		<section aria-labelledby="topo-title"><div class="section-head"><h2 id="topo-title">The environment</h2><span class="muted">as its orchestrator has it</span></div><div data-part="topology"></div></section>
		<section aria-labelledby="runs-title"><div class="section-head"><h2 id="runs-title">Runs</h2></div><div data-part="filters"></div><div data-part="runs"></div><div data-part="pager"></div></section>
		<section aria-labelledby="disp-title"><div class="section-head"><h2 id="disp-title">Dispatchers</h2><span data-part="disp-count" class="muted"></span></div><div data-part="dispatchers"></div></section>`;

	const go = (next: URLSearchParams) => {
		const q = next.toString();
		history.pushState(null, '', `/env/${encodeURIComponent(env)}${q ? `?${q}` : ''}`);
		dispatchEvent(new PopStateEvent('popstate'));
	};
	root.addEventListener('change', (e) => {
		const el = e.target as HTMLSelectElement;
		if (!el.dataset.filter) return;
		const next = new URLSearchParams(location.search);
		if (el.value) next.set(el.dataset.filter, el.value); else next.delete(el.dataset.filter);
		next.delete('pages');
		go(next);
	});

	const readPage = async () => {
		if (!filtered) return;
		const q = new URLSearchParams({ limit: String(per) });
		if (group) q.set('group', group);
		if (state) q.append('state', state);
		if (cursors.length) q.set('before', String(cursors[cursors.length - 1]));
		try {
			page = await read<RunList>(`runs?${q}`);
			pageError = '';
		} catch (err) {
			pageError = (err as Error).message;
		}
		paint();
	};

	const paint = () => {
		const s = live.snapshot;
		if (!s) return;
		paintNow(root, env, s, live);
		paintTopology(root, env, s);
		paintRuns(root, env, s, filtered ? page : { runs: s.runs, before: s.before }, pageError, { group, state, cursors, per });
		paintDispatchers(root, s);
	};
	const off = live.on(paint);
	paint();
	void readPage();
	const timer = filtered ? window.setInterval(readPage, 5000) : 0;
	return () => {
		off();
		clearInterval(timer);
	};
}

function paintNow(root: HTMLElement, env: string, s: Snapshot, live: Live) {
	const runs = [...s.live].sort((a, b) => Date.parse(b.run.openedAt) - Date.parse(a.run.openedAt));
	patch(root, 'now-count', runs.length ? plural(runs.length, 'run') + ' open' : '');
	if (!runs.length) {
		const last = s.runs.find((r) => r.closedAt);
		patch(root, 'now', `<div class="quiet"><p><strong>Nothing is running in ${esc(env)}.</strong> Runs start from CI; this page follows each one as it goes.</p>${last ? `<p class="muted">The last run closed ${esc(ago(last.closedAt))}: <a href="/env/${esc(env)}/runs/${esc(last.id)}" data-route>${esc(opener(last).name)}</a>.</p>` : ''}</div>`);
		return;
	}
	patch(root, 'now', `<div class="cards">${runs.map((l) => card(env, l, live)).join('')}</div>`);
}

function card(env: string, l: LiveRun, live: Live): string {
	const r = l.run, p = l.progress;
	const st = stage(r);
	const who = opener(r);
	const steps = p?.steps ?? [];
	const { index, step } = currentStep(steps);
	const samples = live.samples.get(r.id) ?? [];
	const loads = (p?.load ?? []).map((ld) => {
		const now = rateNow(samples, ld.stream);
		const share = ld.planned ? Math.min(100, (ld.sent / ld.planned) * 100) : 0;
		return `<div class="card-load">
			<div class="card-load-head"><span class="mono">${esc(ld.stream)}</span> <span class="muted">→ ${esc(ld.target)} · ${esc(ld.protocol)}</span></div>
			<div class="card-figures">
				<span><strong>${ld.done ? 'done' : now === undefined ? '…' : `${rate(now)}/s`}</strong><small>of ${rate(ld.rate)}/s</small></span>
				<span><strong>${count(ld.sent)}</strong><small>of ${count(ld.planned)} sent</small></span>
				<span><strong>${count(ld.failed)}</strong><small>failed</small></span>
				<span><strong>${ld.latency ? ms(ld.latency.p99) : '…'}</strong><small>p99</small></span>
			</div>
			<div class="meter" aria-hidden="true"><span style="width:${share.toFixed(1)}%"></span></div>
		</div>`;
	}).join('');
	const instances = r.dispatchers.map((d) => `<span class="chip ${tone(d.state)}" title="${esc(d.detail || d.lastStatus || '')}">${esc(d.name)}/${esc(d.instance)} <b>${esc(d.state)}</b></span>`).join('');
	const checks = (p?.checks ?? []).map((c) => `<span class="chip ${tone(c.status)}" title="${esc(c.source ?? '')}">${esc(c.check)} <b>${esc(c.status)}${c.found ? ` · ${esc(c.found)}` : ''}</b></span>`).join('');
	const title = p?.scenario ?? who.name;
	return `<article class="card">
		<div class="card-top">${pill(st)}${st === 'preparing' ? pill('held', 'gate held') : r.gate.open ? pill('open', 'gate open') : ''}<span class="muted">${esc(r.group)} · ${esc(span(Date.now() - Date.parse(r.openedAt)))}</span></div>
		<h3><a href="/env/${esc(env)}/runs/${esc(r.id)}" data-route aria-label="Follow ${esc(title)}">${esc(title)}</a></h3>
		<div class="where mono">${esc(p?.feature ?? who.where ?? '')} · ${esc(r.id)}</div>
		${strip(steps)}
		${step ? `<p class="now-step"><span class="muted">Step ${index + 1} of ${steps.length}</span> ${esc(step.keyword)} ${esc(step.text)}</p>` : ''}
		${loads}
		${instances || checks ? `<div class="chips">${instances}${checks}</div>` : ''}
	</article>`;
}

function paintTopology(root: HTMLElement, env: string, s: Snapshot) {
	if (!s.groups.length) {
		patch(root, 'topology', '<p class="quiet muted">No groups yet: a dispatcher makes one when it joins.</p>');
		return;
	}
	const byName = new Map<string, Dispatcher[]>();
	for (const d of s.dispatchers) byName.set(d.name, [...(byName.get(d.name) ?? []), d]);
	patch(root, 'topology', s.groups.map((g) => {
		const members = g.dispatchers.flatMap((n) => byName.get(n) ?? []);
		const run = s.live.find((l) => l.run.group === g.name && l.run.holds) ?? s.live.find((l) => l.run.group === g.name);
		return `<figure class="topology"><pre class="adg-grid" aria-hidden="true">${draw(groupScene(env, g.name, members, run))}</pre><figcaption>${esc(describe(g.name, members, run))}</figcaption></figure>`;
	}).join(''));
}

function paintRuns(root: HTMLElement, env: string, s: Snapshot, list: RunList | undefined, error: string, f: { group: string; state: string; cursors: number[]; per: number }) {
	const options = (name: string, values: string[], on: string, all: string) =>
		`<label><span>${esc(name)}</span><select data-filter="${name.toLowerCase()}"><option value="">${esc(all)}</option>${values.map((v) => `<option${v === on ? ' selected' : ''}>${esc(v)}</option>`).join('')}</select></label>`;
	patch(root, 'filters', `<div class="filters">${options('Group', s.groups.map((g) => g.name), f.group, 'every group')}${options('State', ['opened', 'traffic-complete', 'settled', 'cleanup', 'closed'], f.state, 'every state')}</div>`);
	if (error) {
		patch(root, 'runs', `<p class="quiet bad">Cannot read the runs: ${esc(error)}</p>`);
		return;
	}
	if (!list) {
		patch(root, 'runs', '<p class="quiet muted">Reading…</p>');
		return;
	}
	const rows = list.runs.map((r: RunSummary) => {
		const who = opener(r);
		const took = r.closedAt ? span(Date.parse(r.closedAt) - Date.parse(r.openedAt)) : `${span(Date.now() - Date.parse(r.openedAt))} so far`;
		return `<tr>
			<td><a href="/env/${esc(env)}/runs/${esc(r.id)}" data-route>${esc(who.name)}</a><div class="sub mono">${esc(r.id)}${who.where ? ` · ${esc(who.where)}` : ''}</div></td>
			<td>${esc(r.group)}</td>
			<td>${pill(outcome(r))}</td>
			<td class="num">${esc(took)}</td>
			<td class="num">${count(r.claims)}</td>
			<td><time datetime="${esc(r.openedAt)}" title="${esc(when(r.openedAt))}">${esc(ago(r.openedAt))}</time></td>
		</tr>`;
	}).join('');
	patch(root, 'runs', list.runs.length
		? `<div class="table-wrap"><table><thead><tr><th scope="col">Run</th><th scope="col">Group</th><th scope="col">State</th><th scope="col" class="num">Took</th><th scope="col" class="num">Claims</th><th scope="col">Opened</th></tr></thead><tbody>${rows}</tbody></table></div>`
		: `<p class="quiet muted">${f.group || f.state ? 'No run matches.' : 'No runs yet.'}</p>`);
	const q = (cursors: number[]) => {
		const p = new URLSearchParams();
		if (f.group) p.set('group', f.group);
		if (f.state) p.set('state', f.state);
		if (f.per !== PAGE) p.set('per', String(f.per));
		if (cursors.length) p.set('pages', cursors.join(','));
		const s = p.toString();
		return `/env/${encodeURIComponent(env)}${s ? `?${s}` : ''}`;
	};
	const newer = f.cursors.length ? `<a class="button" href="${esc(q(f.cursors.slice(0, -1)))}" data-route>← Newer</a>` : '<span></span>';
	const older = list.before ? `<a class="button" href="${esc(q([...f.cursors, list.before]))}" data-route>Older →</a>` : '<span></span>';
	patch(root, 'pager', f.cursors.length || list.before ? `<nav class="pager" aria-label="Pages of runs">${newer}<span class="muted">page ${f.cursors.length + 1}</span>${older}</nav>` : '');
}

function paintDispatchers(root: HTMLElement, s: Snapshot) {
	const groupsOf = (name: string) => s.groups.filter((g) => g.dispatchers.includes(name)).map((g) => g.name).join(', ');
	const now = Date.now();
	patch(root, 'disp-count', s.dispatchers.length ? plural(s.dispatchers.length, 'instance') : '');
	patch(root, 'dispatchers', s.dispatchers.length
		? `<div class="table-wrap"><table><thead><tr><th scope="col">Dispatcher</th><th scope="col">Instance</th><th scope="col">Team</th><th scope="col">Groups</th><th scope="col">Lease</th></tr></thead><tbody>${s.dispatchers.map((d) => {
			const left = Date.parse(d.leaseExpiresAt) - now;
			return `<tr><td class="mono">${esc(d.name)}</td><td class="mono">${esc(d.instance)}</td><td>${esc(d.team)}</td><td>${esc(groupsOf(d.name))}</td><td>${left > 0 ? pill('live') : pill('lapsed')}</td></tr>`;
		}).join('')}</tbody></table></div>`
		: '<p class="quiet muted">No dispatcher has registered.</p>');
}
