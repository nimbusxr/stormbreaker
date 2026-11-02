// What the orchestrator says (protocol/types.go), and what the UI's server adds: a snapshot of
// an environment each second, and the samples its charts draw (internal/ui/live.go).

export type RunSummary = {
	id: string; group: string; state: string; initiator: string; failed: boolean; abandoned: boolean;
	holds: boolean; unfinished: boolean; claims: number; openedAt: string; cleanupAt?: string; closedAt?: string;
};
export type Instance = {
	name: string; instance: string; required: boolean; state: string; lastStatus?: string; detail?: string;
	evidence?: string; lastReportAt?: string; footprintVersion: number;
};
export type Run = RunSummary & {
	failReason?: string; gate: { open: boolean; waiting?: string[] }; dispatchers: Instance[];
	streams: { name: string; seed: string }[]; footprintVersion: number; windowFrom: string; windowTo?: string;
};
export type RunList = { runs: RunSummary[]; before?: number };
export type Dispatcher = { name: string; instance: string; team: string; leaseExpiresAt: string };
export type Group = { name: string; dispatchers: string[] };

export type StepProgress = { keyword: string; text: string; status?: string; error?: string };
export type Latency = { p50: number; p90: number; p99: number; max: number };
export type LoadProgress = {
	stream: string; protocol: string; target: string; rate: number; planned: number;
	sent: number; inFlight: number; failed: number; latency?: Latency; done?: boolean;
};
export type CheckResult = { check: string; source?: string; status: string; found?: string };
export type Progress = {
	scenario: string; feature?: string; status?: string; steps: StepProgress[];
	load?: LoadProgress[]; checks?: CheckResult[]; final?: boolean; at?: string;
};

export type Sample = { at: string; load: { stream: string; sent: number; failed: number; inFlight: number; p50: number; p99: number }[] };
export type LiveRun = { run: Run; progress?: Progress };
export type Snapshot = {
	environment: string; at: string; error?: string; runs: RunSummary[]; before?: number;
	groups: Group[]; dispatchers: Dispatcher[]; live: LiveRun[]; samples?: Record<string, Sample>;
};
