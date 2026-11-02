// The pieces both pages are made of: states, steps, the load's figures, and the patching that
// lets a page redraw each second without losing the reader's place.
import { count, esc, ms, rate } from './format';
import type { LoadProgress, Progress, Run, Sample, StepProgress } from './types';

/** The colours charts draw series in, in this order (validated against the dark surface). */
export const SERIES = ['#3d9be6', '#d95926', '#199e70', '#c98500'];
/** The colour a series that is only context is drawn in. */
export const CONTEXT = '#4f5b74';

/** What a state means to a reader, which is how it is coloured: good, bad, under way, or idle. */
export function tone(state: string): 'good' | 'bad' | 'busy' | 'idle' {
	switch (state) {
		case 'passed': case 'prepared': case 'completed': case 'closed': case 'open': case 'live': return 'good';
		case 'failed': case 'abandoned': case 'unfinished': case 'unreachable': case 'blocked': case 'lapsed': case 'undefined': case 'ambiguous': return 'bad';
		case 'running': case 'opened': case 'preparing': case 'traffic': case 'held': case 'waiting': case 'notified': case 'acknowledged':
		case 'traffic-complete': case 'settled': case 'cleanup': case 'in-progress': case 'partial': return 'busy';
		default: return 'idle';
	}
}

/** A state, as a pill: an icon (drawn by the stylesheet) and its word, never colour alone. */
export const pill = (state: string, label = state) => `<span class="pill ${tone(state)}">${esc(label)}</span>`;

/** Where a run is: preparing until its gate opens, then traffic, and so on. */
export function stage(r: Run): string {
	if (r.failed) return 'failed';
	if (r.abandoned) return 'abandoned';
	if (r.unfinished) return 'unfinished';
	if (r.state === 'opened') return r.gate.open ? 'traffic' : 'preparing';
	return r.state;
}

/** The step a scenario is on, or its last. */
export function currentStep(steps: StepProgress[]): { index: number; step?: StepProgress } {
	const i = steps.findIndex((s) => s.status === 'running');
	if (i >= 0) return { index: i, step: steps[i] };
	const failed = steps.findIndex((s) => s.status === 'failed');
	if (failed >= 0) return { index: failed, step: steps[failed] };
	let last = -1;
	steps.forEach((s, j) => { if (s.status) last = j; });
	return { index: last, step: steps[last] };
}

/** The scenario's steps as a strip, a segment each, coloured by how it went. */
export function strip(steps: StepProgress[]): string {
	if (!steps.length) return '';
	return `<div class="strip" role="img" aria-label="${esc(steps.map((s) => `${s.keyword} ${s.text}: ${s.status || 'not run'}`).join('; '))}">${steps.map((s) => `<span class="seg ${tone(s.status || 'idle')}${s.status === 'running' ? ' running' : ''}"></span>`).join('')}</div>`;
}

/** The rate a stream goes at now, from the samples of the last few seconds. */
export function rateNow(samples: Sample[], stream: string, window = 5_000): number | undefined {
	const pts = samples.map((s) => ({ t: Date.parse(s.at), l: s.load.find((l) => l.stream === stream) })).filter((p) => p.l);
	if (pts.length < 2) return undefined;
	const last = pts[pts.length - 1];
	const first = pts.find((p) => last.t - p.t <= window) ?? pts[0];
	const dt = (last.t - first.t) / 1000;
	return dt > 0 ? (last.l!.sent - first.l!.sent) / dt : undefined;
}

/** A stream's figures, as stat tiles: how fast, how much, how many failed, how long. */
export function tiles(l: LoadProgress, now: number | undefined): string {
	const share = l.planned ? Math.min(1, l.sent / l.planned) : 0;
	const failedShare = l.sent ? (l.failed / l.sent) * 100 : 0;
	const tile = (label: string, value: string, sub: string, extra = '') => `<div class="tile"><div class="tile-label">${esc(label)}</div><div class="tile-value">${value}</div><div class="tile-sub">${sub}</div>${extra}</div>`;
	return `<div class="tiles">
		${tile('Rate', l.done ? 'done' : now === undefined ? '…' : `${rate(now)}<small>/s</small>`, `declared ${rate(l.rate)}/s`)}
		${tile('Sent', count(l.sent), `of ${count(l.planned)}`, `<div class="meter" role="meter" aria-valuemin="0" aria-valuemax="${l.planned}" aria-valuenow="${l.sent}" aria-label="sent of planned"><span style="width:${(share * 100).toFixed(1)}%"></span></div>`)}
		${tile('Failed', count(l.failed), l.sent ? `${failedShare < 0.1 && l.failed ? '<0.1' : failedShare.toFixed(1)}% of sent` : 'none sent yet')}
		${tile('Latency p99', l.latency ? ms(l.latency.p99) : '…', l.latency ? `p50 ${ms(l.latency.p50)} · max ${ms(l.latency.max)}` : 'none answered yet')}
	</div>`;
}

/** The run's progress, or a stand-in made of what the orchestrator knows. */
export const scenarioOf = (r: Run, p?: Progress) => p?.scenario ?? r.initiator;

/** Puts html into the element marked data-part=key, unless it is there already: a page redraws
 * each second, and what did not change keeps its place, its focus and its selection. */
const drawn = new WeakMap<HTMLElement, string>();
export function patch(root: HTMLElement, key: string, html: string) {
	const el = root.querySelector<HTMLElement>(`[data-part="${key}"]`);
	if (el && drawn.get(el) !== html) {
		el.innerHTML = html;
		drawn.set(el, html);
	}
}
