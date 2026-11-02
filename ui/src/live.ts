// An environment as it goes: the UI's server reads it once a second while anyone watches, and
// streams each read here (ADR 0027). This keeps the latest, and the samples the charts draw.
import type { LiveRun, Sample, Snapshot } from './types';

const KEEP = 1800;

export class Live {
	snapshot?: Snapshot;
	/** When the last read arrived, by this browser's clock. */
	receivedAt = 0;
	connected = false;
	samples = new Map<string, Sample[]>();
	private source?: EventSource;
	private listeners = new Set<() => void>();

	constructor(readonly env: string) {}

	start() {
		const source = new EventSource(`/api/env/${encodeURIComponent(this.env)}/live`);
		this.source = source;
		source.addEventListener('open', () => this.set(true));
		source.addEventListener('error', () => this.set(false));
		source.addEventListener('history', (e) => {
			this.samples = new Map(Object.entries(JSON.parse((e as MessageEvent).data) as Record<string, Sample[]>));
			this.emit();
		});
		source.addEventListener('snapshot', (e) => {
			const s = JSON.parse((e as MessageEvent).data) as Snapshot;
			for (const [id, sample] of Object.entries(s.samples ?? {})) {
				const list = this.samples.get(id) ?? [];
				if (list[list.length - 1]?.at !== sample.at) list.push(sample);
				if (list.length > KEEP) list.splice(0, list.length - KEEP);
				this.samples.set(id, list);
			}
			// A read that failed keeps what was read before, and says why.
			this.snapshot = s.error && this.snapshot ? { ...this.snapshot, error: s.error, at: s.at } : s;
			this.receivedAt = Date.now();
			this.set(true);
		});
	}

	stop() {
		this.source?.close();
		this.listeners.clear();
	}

	on(fn: () => void): () => void {
		this.listeners.add(fn);
		return () => this.listeners.delete(fn);
	}

	run(id: string): LiveRun | undefined {
		return this.snapshot?.live.find((l) => l.run.id === id);
	}

	/** Whether the last read is recent: the stream is up and the orchestrator answers. */
	fresh(): boolean {
		return this.connected && !this.snapshot?.error && Date.now() - this.receivedAt < 5000;
	}

	private set(connected: boolean) {
		this.connected = connected;
		this.emit();
	}

	private emit() {
		for (const fn of this.listeners) fn();
	}
}
