// A line chart over time, in SVG, for a run's load (the dataviz rules: 2px lines, a ringed end
// dot, solid hairline grid, one axis, a crosshair whose tooltip lists every series, a legend
// for two series or more, keyboard as well as pointer). It redraws in place each second and
// keeps where the reader points.
import { esc } from './format';

export type Series = { name: string; color: string; points: [t: number, v: number][]; context?: boolean };
export type ChartData = { series: Series[]; format: (v: number) => string; reference?: { value: number; label: string } };

const H = 150, AXIS = 22, LEFT = 46, RIGHT = 64, TOP = 10;

function nice(max: number): number[] {
	if (max <= 0) return [0, 1];
	const raw = max / 4, mag = 10 ** Math.floor(Math.log10(raw));
	const step = [1, 2, 2.5, 5, 10].map((m) => m * mag).find((s) => s >= raw)!;
	const ticks: number[] = [];
	for (let v = 0; v <= max + step * 0.001; v += step) ticks.push(v);
	if (ticks[ticks.length - 1] < max) ticks.push(ticks[ticks.length - 1] + step);
	return ticks;
}

const clock = (t: number) => new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false });

export class LineChart {
	private data?: ChartData;
	private at: number | undefined; // the index the crosshair is on
	private width = 600;

	constructor(private readonly el: HTMLElement, private readonly label: string) {
		el.classList.add('chart');
		el.tabIndex = 0;
		el.setAttribute('role', 'img');
		el.addEventListener('pointermove', (e) => this.point(e));
		el.addEventListener('pointerleave', () => { this.at = undefined; this.draw(); });
		el.addEventListener('keydown', (e) => this.key(e));
		el.addEventListener('blur', () => { this.at = undefined; this.draw(); });
		new ResizeObserver(() => {
			const w = Math.max(240, Math.round(el.clientWidth));
			if (w !== this.width) { this.width = w; this.draw(); }
		}).observe(el);
		this.width = Math.max(240, Math.round(el.clientWidth || 600));
	}

	update(data: ChartData) {
		this.data = data;
		this.draw();
	}

	/** Every time any series has a point at, in order: the crosshair's stops. */
	private times(): number[] {
		return [...new Set((this.data?.series ?? []).flatMap((s) => s.points.map((p) => p[0])))].sort((a, b) => a - b);
	}

	private point(e: PointerEvent) {
		const ts = this.times();
		if (ts.length < 2) return;
		const r = this.el.getBoundingClientRect();
		const x = e.clientX - r.left;
		const [t0, t1] = [ts[0], ts[ts.length - 1]];
		const t = t0 + ((x - LEFT) / (this.width - LEFT - RIGHT)) * (t1 - t0);
		let best = 0;
		ts.forEach((v, i) => { if (Math.abs(v - t) < Math.abs(ts[best] - t)) best = i; });
		if (best !== this.at) { this.at = best; this.draw(); }
	}

	private key(e: KeyboardEvent) {
		const n = this.times().length;
		if (!n || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight')) return;
		e.preventDefault();
		this.at = Math.max(0, Math.min(n - 1, (this.at ?? n - 1) + (e.key === 'ArrowLeft' ? -1 : 1)));
		this.draw();
	}

	private draw() {
		const d = this.data;
		if (!d) return;
		const w = this.width, plotW = w - LEFT - RIGHT;
		const ts = this.times();
		const series = d.series.filter((s) => s.points.length);
		if (ts.length < 2 || !series.length) {
			this.el.innerHTML = `<p class="chart-empty">${esc(this.label)}: drawn as the run goes.</p>`;
			return;
		}
		const max = Math.max(d.reference?.value ?? 0, ...series.flatMap((s) => s.points.map((p) => p[1])));
		const ticks = nice(max), top = ticks[ticks.length - 1] || 1;
		const [t0, t1] = [ts[0], ts[ts.length - 1]];
		const x = (t: number) => LEFT + ((t - t0) / Math.max(1, t1 - t0)) * plotW;
		const y = (v: number) => TOP + H - (v / top) * H;
		const grid = ticks.map((v) => `<line class="grid" x1="${LEFT}" x2="${w - RIGHT}" y1="${y(v)}" y2="${y(v)}"/><text class="tick" x="${LEFT - 8}" y="${y(v) + 4}" text-anchor="end">${esc(d.format(v))}</text>`).join('');
		// As many times along the bottom as fit, never one on another.
		const n = t1 - t0 < 4000 ? 2 : Math.max(2, Math.min(5, Math.floor(plotW / 110) + 1));
		const xticks = Array.from({ length: n }, (_, i) => t0 + ((t1 - t0) * i) / (n - 1));
		const xaxis = xticks.map((t, i) => `<text class="tick" x="${x(t)}" y="${TOP + H + 16}" text-anchor="${i === 0 ? 'start' : i === n - 1 ? 'end' : 'middle'}">${esc(clock(t))}</text>`).join('');
		const ref = d.reference ? `<line class="ref" x1="${LEFT}" x2="${w - RIGHT}" y1="${y(d.reference.value)}" y2="${y(d.reference.value)}"/><text class="ref-label" x="${w - RIGHT + 6}" y="${y(d.reference.value) + 4}">${esc(d.reference.label)}</text>` : '';
		// Context series first, under the one the chart is about.
		const order = [...series.filter((s) => s.context), ...series.filter((s) => !s.context)];
		const lines = order.map((s) => {
			const path = s.points.map((p, i) => `${i ? 'L' : 'M'}${x(p[0]).toFixed(1)},${y(p[1]).toFixed(1)}`).join('');
			const [lt, lv] = s.points[s.points.length - 1];
			const label = s.context ? '' : `<text class="end-label" x="${x(lt) + 8}" y="${y(lv) + 4}">${esc(d.format(lv))}</text>`;
			return `<path class="line" d="${path}" stroke="${s.color}"/><circle class="dot" cx="${x(lt)}" cy="${y(lv)}" r="4" fill="${s.color}"/>${d.reference ? '' : label}`;
		}).join('');
		let cross = '', tip = '';
		if (this.at !== undefined && ts[this.at] !== undefined) {
			const t = ts[this.at], cx = x(t);
			const at = (s: Series) => s.points.find((p) => p[0] === t);
			cross = `<line class="cross" x1="${cx}" x2="${cx}" y1="${TOP}" y2="${TOP + H}"/>` +
				series.map((s) => { const p = at(s); return p ? `<circle class="dot" cx="${cx}" cy="${y(p[1])}" r="4" fill="${s.color}"/>` : ''; }).join('');
			const rows = series.map((s) => { const p = at(s); return p ? `<div class="tip-row"><span class="key" style="background:${s.color}"></span><strong>${esc(d.format(p[1]))}</strong><span>${esc(s.name)}</span></div>` : ''; }).join('');
			const left = cx > w / 2 ? `right:${w - cx + 10}px` : `left:${cx + 10}px`;
			tip = `<div class="tip" style="${left};top:${TOP}px"><div class="tip-time">${esc(clock(t))}</div>${rows}</div>`;
		}
		const legend = series.length > 1
			? `<div class="legend">${series.map((s) => `<span><span class="key" style="background:${s.color}"></span>${esc(s.name)}</span>`).join('')}</div>`
			: '';
		const last = series.map((s) => `${s.name} ${d.format(s.points[s.points.length - 1][1])}`).join(', ');
		this.el.setAttribute('aria-label', `${this.label}, now ${last}. Arrow keys move along it.`);
		this.el.innerHTML = `${legend}<svg width="${w}" height="${TOP + H + AXIS}" viewBox="0 0 ${w} ${TOP + H + AXIS}">${grid}${xaxis}${ref}${lines}${cross}</svg>${tip}`;
	}
}
