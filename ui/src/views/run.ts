// A run's page: the scenario as it goes (its steps, its load, its checks), the run as the
// orchestrator has it (its gate, its instances, its journal), and the topology under test. A
// run still open follows the live view; one that closed is read once.
import { render } from '../../../docs/src/lib/ascii-diagram';
import { runLifecycle } from '../../../docs/src/components/diagrams/scenes';
import { ago, count, esc, ms, opener, rate, span, when } from '../format';
import type { Live } from '../live';
import { LineChart, type Series } from '../chart';
import { CONTEXT, SERIES, currentStep, patch, pill, rateNow, stage, tiles, tone } from '../parts';
import { describe, draw, groupScene } from '../scene';
import type { Progress, Run, Sample } from '../types';

const lifecycle = render(runLifecycle);
const stepOf = new Map(runLifecycle.steps.map((s, i) => [s.name, i]));

export function runView(root: HTMLElement, live: Live, env: string, id: string, read: <T>(path: string) => Promise<T>, readText: (path: string) => Promise<string>): () => void {
	let run: Run | undefined, progress: Progress | undefined, samples: Sample[] = [];
	let journal = '', missing = '', wasLive = false, journalAt = 0, loaded = false;

	root.innerHTML = `
		<p class="crumbs"><a href="/env/${esc(env)}" data-route>← ${esc(env)}</a></p>
		<header class="run-head" data-part="head"><p class="muted">Reading ${esc(id)}…</p></header>
		<div class="run-grid">
			<div class="col-main">
				<section data-section="load" hidden aria-labelledby="load-title">
					<div class="section-head"><h2 id="load-title">Load</h2><span class="muted" data-part="load-note"></span></div>
					<div data-part="tiles"></div>
					<div class="charts">
						<figure class="panel"><figcaption>Records a second</figcaption><div data-chart="rate"></div></figure>
						<figure class="panel"><figcaption>Latency the suite sees, over the run so far</figcaption><div data-chart="latency"></div></figure>
					</div>
					<details class="table-view"><summary>The load as a table</summary><div data-part="load-table"></div></details>
				</section>
				<section aria-labelledby="steps-title"><div class="section-head"><h2 id="steps-title">Steps</h2><span class="muted" data-part="steps-note"></span></div><div data-part="steps"></div></section>
			</div>
			<aside class="col-side">
				<section aria-labelledby="gate-title"><h2 id="gate-title">Readiness gate</h2><div data-part="gate"></div></section>
				<section aria-labelledby="checks-title"><h2 id="checks-title">Checks</h2><div data-part="checks"></div></section>
				<section aria-labelledby="inst-title"><h2 id="inst-title">Instances</h2><div data-part="instances"></div></section>
			</aside>
		</div>
		<section aria-labelledby="topo-title"><div class="section-head"><h2 id="topo-title">The topology under test</h2><span class="muted">as the orchestrator and the suite have it</span></div><div data-part="topology"></div></section>
		<section aria-labelledby="life-title"><div class="section-head"><h2 id="life-title">Where the run is</h2><span class="muted">its lifecycle</span></div><div class="lifecycle" data-part="lifecycle"></div></section>
		<section aria-labelledby="journal-title"><div class="section-head"><h2 id="journal-title">Journal</h2><span class="muted">the run told as a story</span></div><pre class="journal" data-journal></pre></section>`;

	const rateChart = new LineChart(root.querySelector('[data-chart="rate"]')!, 'Records a second');
	const latencyChart = new LineChart(root.querySelector('[data-chart="latency"]')!, 'Latency the suite sees');
	const journalEl = root.querySelector<HTMLElement>('[data-journal]')!;

	// A run that is not live is read: once it closed, or if it never was in the live view.
	const readOnce = async () => {
		try {
			const [r, p, j, sm] = await Promise.all([
				read<Run>(`runs/${encodeURIComponent(id)}`),
				read<Progress>(`runs/${encodeURIComponent(id)}/progress`).catch(() => undefined),
				readText(`runs/${encodeURIComponent(id)}/journal`).catch(() => ''),
				fetch(`/api/env/${encodeURIComponent(env)}/samples/${encodeURIComponent(id)}`).then((x) => x.json() as Promise<Sample[]>).catch(() => [] as Sample[]),
			]);
			run = r;
			progress = p;
			journal = j;
			if (sm.length) samples = sm;
			missing = '';
		} catch (err) {
			missing = (err as Error).message;
		}
		loaded = true;
		paint();
	};
	const readJournal = async () => {
		journalAt = Date.now();
		journal = await readText(`runs/${encodeURIComponent(id)}/journal`).catch(() => journal);
		paint();
	};

	const paint = () => {
		const l = live.run(id);
		if (l) {
			run = l.run;
			progress = l.progress ?? progress;
			samples = live.samples.get(id) ?? samples;
			wasLive = true;
			if (Date.now() - journalAt > 3000) void readJournal();
		} else if (wasLive) {
			// It closed while we watched: read how it ended.
			wasLive = false;
			void readOnce();
		} else if (!loaded && live.snapshot) {
			loaded = true;
			void readOnce();
		}
		if (missing && !run) {
			patch(root, 'head', `<h1>${esc(id)}</h1><p class="bad">Cannot read this run: ${esc(missing)}</p>`);
			return;
		}
		if (!run) return;
		paintHead(root, run, progress);
		paintLifecycle(root, run);
		paintLoad(root, progress, samples, rateChart, latencyChart);
		paintSteps(root, run, progress);
		paintGate(root, run);
		paintChecks(root, progress);
		paintInstances(root, run);
		const s = live.snapshot;
		if (s) {
			const g = s.groups.find((x) => x.name === run!.group);
			const members = s.dispatchers.filter((d) => g?.dispatchers.includes(d.name) || run!.dispatchers.some((i) => i.name === d.name));
			patch(root, 'topology', `<figure class="topology"><pre class="adg-grid" aria-hidden="true">${draw(groupScene(env, run.group, members, { run, progress }))}</pre><figcaption>${esc(describe(run.group, members, { run, progress }))}</figcaption></figure>`);
		}
		if (journalEl.textContent !== journal) {
			const atEnd = journalEl.scrollTop + journalEl.clientHeight >= journalEl.scrollHeight - 8;
			journalEl.textContent = journal || 'The journal is empty.';
			if (atEnd) journalEl.scrollTop = journalEl.scrollHeight;
		}
	};
	const off = live.on(paint);
	paint();
	return off;
}

function paintHead(root: HTMLElement, r: Run, p?: Progress) {
	const who = opener(r);
	const st = stage(r);
	const took = r.closedAt ? span(Date.parse(r.closedAt) - Date.parse(r.openedAt)) : `${span(Date.now() - Date.parse(r.openedAt))} so far`;
	const result = p?.status ? pill(p.status, `scenario ${p.status}`) : '';
	patch(root, 'head', `
		<div class="run-title"><h1>${esc(p?.scenario ?? who.name)}</h1><div class="pills">${pill(st)}${result}</div></div>
		<p class="where mono">${esc(p?.feature ?? who.where ?? '')}${p?.feature || who.where ? ' · ' : ''}${esc(r.id)}</p>
		${r.failReason ? `<p class="bad">${esc(r.failReason)}</p>` : ''}
		<dl class="facts">
			<div><dt>Group</dt><dd>${esc(r.group)}</dd></div>
			<div><dt>Opened</dt><dd><time datetime="${esc(r.openedAt)}" title="${esc(when(r.openedAt))}">${esc(ago(r.openedAt))}</time></dd></div>
			<div><dt>${r.closedAt ? 'Took' : 'Open for'}</dt><dd>${esc(took)}</dd></div>
			<div><dt>Claims</dt><dd>${count(r.claims)}</dd></div>
			<div><dt>Streams</dt><dd>${r.streams?.length ? r.streams.map((s) => esc(s.name)).join(', ') : 'none'}</dd></div>
		</dl>`);
}

function paintLifecycle(root: HTMLElement, r: Run) {
	const name = r.failed ? 'failed' : r.abandoned ? 'abandoned' : r.state === 'opened' ? (r.gate.open ? 'traffic' : 'preparing') : r.state;
	const step = lifecycle[stepOf.get(name) ?? 0];
	patch(root, 'lifecycle', `<pre class="adg-grid">${step.html}</pre><p class="caption"><strong>${esc(step.name)}.</strong> ${esc(step.text)}</p>`);
}

/** Points a chart draws from samples: one per sample after the first. */
function series(samples: Sample[], value: (s: Sample, i: number) => number | undefined): [number, number][] {
	const out: [number, number][] = [];
	samples.forEach((s, i) => {
		if (!i) return;
		const v = value(s, i);
		if (v !== undefined && Number.isFinite(v)) out.push([Date.parse(s.at), v]);
	});
	return out;
}

function paintLoad(root: HTMLElement, p: Progress | undefined, samples: Sample[], rateChart: LineChart, latencyChart: LineChart) {
	const loads = p?.load ?? [];
	const section = root.querySelector<HTMLElement>('[data-section="load"]')!;
	section.hidden = !loads.length;
	if (!loads.length) return;
	patch(root, 'load-note', loads.map((l) => `${l.stream} → ${l.target} · ${l.protocol}`).join(' · '));
	patch(root, 'tiles', loads.map((l) => `${loads.length > 1 ? `<h3 class="stream-name mono">${esc(l.stream)}</h3>` : ''}${tiles(l, rateNow(samples, l.stream))}`).join(''));
	const of = (s: Sample, stream: string) => s.load.find((l) => l.stream === stream);
	// A stream's rate is drawn while it sends: after its last record, a trailing average would
	// only fall.
	const lastSent = (stream: string) => samples.reduce((last, s, i) => {
		const x = of(s, stream), prev = i ? of(samples[i - 1], stream) : undefined;
		return x && (!prev || x.sent > prev.sent) ? i : last;
	}, 0);
	const rates: Series[] = loads.slice(0, SERIES.length).map((l, k) => ({
		name: l.stream, color: SERIES[k],
		points: series(samples, (s, i) => {
			const back = samples[Math.max(0, i - 3)], a = of(back, l.stream), b = of(s, l.stream);
			if (!b || i > lastSent(l.stream)) return undefined;
			const dt = (Date.parse(s.at) - Date.parse(back.at)) / 1000;
			return dt > 0 ? (b.sent - (a?.sent ?? 0)) / dt : undefined;
		}),
	}));
	rateChart.update({ series: rates, format: (v) => `${rate(v)}/s`, reference: loads.length === 1 ? { value: loads[0].rate, label: `declared ${rate(loads[0].rate)}/s` } : undefined });
	const latency: Series[] = loads.length === 1
		? [
			{ name: 'p50', color: CONTEXT, context: true, points: series(samples, (s) => of(s, loads[0].stream)?.p50) },
			{ name: 'p99', color: SERIES[0], points: series(samples, (s) => of(s, loads[0].stream)?.p99) },
		]
		: loads.slice(0, SERIES.length).map((l, k) => ({ name: `${l.stream} p99`, color: SERIES[k], points: series(samples, (s) => of(s, l.stream)?.p99) }));
	latencyChart.update({ series: latency, format: ms });
	const rows = samples.slice(-300).reverse().map((s) => `<tr><td class="mono">${esc(new Date(s.at).toLocaleTimeString())}</td>${loads.map((l) => {
		const x = of(s, l.stream);
		return x ? `<td class="num">${count(x.sent)}</td><td class="num">${count(x.failed)}</td><td class="num">${esc(ms(x.p50))}</td><td class="num">${esc(ms(x.p99))}</td>` : '<td></td><td></td><td></td><td></td>';
	}).join('')}</tr>`).join('');
	const head = loads.map((l) => `<th scope="col" class="num">${loads.length > 1 ? `${esc(l.stream)} ` : ''}sent</th><th scope="col" class="num">failed</th><th scope="col" class="num">p50</th><th scope="col" class="num">p99</th>`).join('');
	patch(root, 'load-table', samples.length ? `<div class="table-wrap"><table><thead><tr><th scope="col">At</th>${head}</tr></thead><tbody>${rows}</tbody></table></div>` : '<p class="muted">Samples are taken while the run goes, by the UI watching it.</p>');
}

function paintSteps(root: HTMLElement, r: Run, p?: Progress) {
	if (!p) {
		patch(root, 'steps-note', '');
		patch(root, 'steps', `<p class="quiet muted">${r.state === 'closed' ? 'The suite said nothing about its steps' : 'Waiting for the suite to say how it goes'}: stormbreaker-core sends them (ADR 0027).</p>`);
		return;
	}
	const steps = p.steps ?? [];
	const done = steps.filter((s) => s.status && s.status !== 'running').length;
	const { index } = currentStep(steps);
	patch(root, 'steps-note', steps.length ? `${done} of ${steps.length} done` : '');
	patch(root, 'steps', `<ol class="steps">${steps.map((s, i) => {
		const t = tone(s.status || 'idle');
		return `<li class="step ${t}${s.status === 'running' ? ' running' : ''}${i === index ? ' current' : ''}">
			<span class="step-icon" aria-hidden="true">${({ good: '✓', bad: '✕', busy: '●', idle: '○' } as const)[t]}</span>
			<span class="step-text"><b>${esc(s.keyword)}</b> ${esc(s.text)}</span>
			<span class="step-status">${esc(s.status || 'not run')}</span>
			${s.error ? `<pre class="step-error">${esc(s.error)}</pre>` : ''}
		</li>`;
	}).join('')}</ol>`);
}

function paintGate(root: HTMLElement, r: Run) {
	if (r.failed) return patch(root, 'gate', `<p class="gate bad">${pill('failed', 'never opened')}</p><p class="muted">${esc(r.failReason ?? '')}</p>`);
	if (r.state !== 'opened') return patch(root, 'gate', `<p class="gate">${pill('open', 'opened')}</p><p class="muted">The traffic went through it; the run is past it now.</p>`);
	if (r.gate.open) return patch(root, 'gate', `<p class="gate">${pill('open', 'open')}</p><p class="muted">Every required instance prepared on the footprint as it is: the load may go.</p>`);
	patch(root, 'gate', `<p class="gate">${pill('held', 'held')}</p>${r.gate.waiting?.length ? `<p class="muted">Waiting on:</p><ul class="waiting">${r.gate.waiting.map((w) => `<li class="mono">${esc(w)}</li>`).join('')}</ul>` : ''}`);
}

function paintChecks(root: HTMLElement, p?: Progress) {
	const checks = p?.checks ?? [];
	patch(root, 'checks', checks.length
		? `<ul class="checks">${checks.map((c) => `<li class="check ${tone(c.status)}">${pill(c.status)}<span class="check-text">${esc(c.check)}</span>${c.source || c.found ? `<span class="muted">${c.source ? `from ${esc(c.source)}` : ''}${c.found ? `${c.source ? ' · ' : ''}found ${esc(c.found)}` : ''}</span>` : ''}</li>`).join('')}</ul>`
		: '<p class="muted">No checks yet.</p>');
}

function paintInstances(root: HTMLElement, r: Run) {
	patch(root, 'instances', r.dispatchers.length
		? `<ul class="instances">${r.dispatchers.map((d) => `<li>${pill(d.state)}<span class="mono">${esc(d.name)}/${esc(d.instance)}</span>${d.required ? '' : '<span class="muted">optional</span>'}<div class="muted small">${esc(d.detail || d.evidence || d.lastStatus || '')}${d.lastReportAt ? ` · reported ${esc(ago(d.lastReportAt))}` : ''}</div></li>`).join('')}</ul>`
		: '<p class="muted">No dispatcher is on this run.</p>');
}
