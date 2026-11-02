// Words and numbers, as the UI writes them.
import type { RunSummary } from './types';

export const esc = (s: unknown) => String(s ?? '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');

const seconds = (ms: number) => Math.max(0, Math.round(ms / 1000));

/** A span of time, the way people say it: 45s, 3m 12s, 2h 5m, 3d. */
export function span(ms: number): string {
	const s = seconds(ms);
	if (s < 60) return `${s}s`;
	if (s < 3600) return `${Math.floor(s / 60)}m${s % 60 ? ` ${s % 60}s` : ''}`;
	if (s < 86400) return `${Math.floor(s / 3600)}h${Math.floor((s % 3600) / 60) ? ` ${Math.floor((s % 3600) / 60)}m` : ''}`;
	return `${Math.floor(s / 86400)}d`;
}

export const ago = (iso?: string) => (iso ? `${span(Date.now() - Date.parse(iso))} ago` : '');
export const when = (iso?: string) => (iso ? new Date(iso).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'medium' }) : '');

/** A count in full, 1,284; a big one compact, 12.9K, 4.2M. */
export function count(n: number): string {
	if (Math.abs(n) < 10_000) return Math.round(n).toLocaleString('en-US');
	return new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 }).format(n);
}

/** A rate a second, to the precision it needs. */
export const rate = (n: number) => (n >= 100 ? count(n) : n >= 10 ? n.toFixed(1).replace(/\.0$/, '') : n.toFixed(2).replace(/\.?0+$/, '') || '0');

/** Milliseconds, as a latency is read. */
export const ms = (n: number) => (n >= 1000 ? `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)} s` : `${n >= 10 ? Math.round(n) : n.toFixed(1).replace(/\.0$/, '')} ms`);

/** The scenario a run belongs to, from who opened it: stormbreaker-core opens a run as
 * "axx <file>:<line> <scenario>". */
export function opener(r: Pick<RunSummary, 'initiator'>): { name: string; where?: string } {
	const m = r.initiator.match(/^axx (\S+:\d+) (.+)$/);
	return m ? { name: m[2], where: m[1] } : { name: r.initiator };
}

/** How a run ended, or where it is: a state a person reads. */
export function outcome(r: RunSummary): string {
	if (r.failed) return 'failed';
	if (r.abandoned) return 'abandoned';
	if (r.unfinished) return 'unfinished';
	return r.state;
}

export const plural = (n: number, one: string, many = `${one}s`) => `${count(n)} ${n === 1 ? one : many}`;
