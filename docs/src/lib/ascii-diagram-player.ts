/**
 * The diagrams' controls, on the page: a figure shows one step at a time, and its reader moves
 * through them, with the arrows, the step numbers, or the arrow keys. No step moves by itself.
 * (src/components/diagrams/AsciiDiagram.astro renders a figure; the topology builder makes one in
 * the browser with figureHTML.)
 */
import { render, type Scene } from './ascii-diagram';

/** Wires a diagram's figure to its controls. It returns what unwires it. */
export function startDiagram(figure: HTMLElement): () => void {
	const grids = [...figure.querySelectorAll<HTMLElement>('[data-grid]')];
	const captions = [...figure.querySelectorAll<HTMLElement>('[data-caption]')];
	const nums = [...figure.querySelectorAll<HTMLButtonElement>('[data-goto]')];
	const prev = figure.querySelector<HTMLButtonElement>('[data-prev]');
	const next = figure.querySelector<HTMLButtonElement>('[data-next]');
	let step = 0;

	const show = (i: number) => {
		step = Math.max(0, Math.min(grids.length - 1, i));
		grids.forEach((g, j) => (g.hidden = j !== step));
		captions.forEach((c, j) => (c.hidden = j !== step));
		nums.forEach((n, j) => n.setAttribute('aria-pressed', String(j === step)));
		if (prev) prev.disabled = step === 0;
		if (next) next.disabled = step === grids.length - 1;
	};
	const onPrev = () => show(step - 1);
	const onNext = () => show(step + 1);
	const onKey = (e: KeyboardEvent) => {
		if (e.key === 'ArrowRight') { show(step + 1); e.preventDefault(); }
		if (e.key === 'ArrowLeft') { show(step - 1); e.preventDefault(); }
	};
	const onNums = nums.map((n, j) => { const go = () => show(j); n.addEventListener('click', go); return go; });
	prev?.addEventListener('click', onPrev);
	next?.addEventListener('click', onNext);
	figure.addEventListener('keydown', onKey);
	show(0);
	return () => {
		prev?.removeEventListener('click', onPrev);
		next?.removeEventListener('click', onNext);
		figure.removeEventListener('keydown', onKey);
		nums.forEach((n, j) => n.removeEventListener('click', onNums[j]));
	};
}

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

/** A diagram's figure, as AsciiDiagram.astro renders one, for a scene drawn in the browser. */
export function figureHTML(scene: Scene, o: { name: string; title: string; text: string; legend?: { kind: string; label: string }[] }): string {
	const steps = render(scene);
	const controls = steps.length > 1 ? `<div class="adg-controls" role="group" aria-label="${escape(o.title)}: steps">
		<button type="button" class="adg-nav" data-prev aria-label="Previous step">‹</button>
		${steps.map((s, i) => `<button type="button" class="adg-num" data-goto="${i}" aria-label="Step ${i + 1}: ${escape(s.name)}" aria-pressed="${i === 0}">${i + 1}</button>`).join('')}
		<button type="button" class="adg-nav" data-next aria-label="Next step">›</button></div>` : '';
	return `<figure class="adg" data-adg>
	<div class="adg-bar"><span class="adg-name">${escape(o.name)}</span>${controls}</div>
	<div class="adg-caption" aria-live="polite">${steps.map((s, i) => `<p data-caption="${i}"${i ? ' hidden' : ''}>${steps.length > 1 ? `<span class="adg-count">${i + 1}/${steps.length}</span>` : ''}<strong>${escape(s.name)}.</strong> ${escape(s.text)}</p>`).join('')}</div>
	<div class="adg-stage" role="img" aria-label="${escape(o.title)}. ${escape(o.text)}"><div class="adg-layers">
		${steps.map((s, i) => `<pre class="adg-grid" data-grid="${i}"${i ? ' hidden' : ''}>${s.html}</pre>`).join('')}</div></div>
	${o.legend?.length ? `<ul class="adg-legend" aria-hidden="true">${o.legend.map((l) => `<li><span class="k-${l.kind}">──</span>${escape(l.label)}</li>`).join('')}</ul>` : ''}
</figure>`;
}
