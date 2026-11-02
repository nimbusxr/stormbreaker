/**
 * What each diagram shows, in words: its SVG's title and description, and what the page's Markdown
 * twin (src/lib/markdown.ts) says in its place.
 */
export const DIAGRAMS = {
	RunTopology: {
		title: 'A run through a shared environment',
		text: 'The suite runs on a CI runner outside the cluster. It opens a run on the environment\'s orchestrator, declares what its streams will create, and renews its lease. The orchestrator notifies each team\'s dispatcher with signed webhooks, and the dispatchers report back what they derive and prepare. When every required dispatcher has prepared on the current footprint, the gate opens and the suite sends load to the parcels service, which feeds the tracking service, which books collections with a mocked courier. The suite judges SLOs from Prometheus. Then each dispatcher removes and verifies its own team\'s data, and the run closes.',
	},
	RunLifecycle: {
		title: 'The run lifecycle',
		text: 'While a run is opened, its dispatchers prepare until the readiness gate opens and the traffic flows; then it moves to traffic-complete, settled, cleanup and closed. Two side paths lead to cleanup: failed, when the gate cannot open, and abandoned, when the suite goes silent or gives the run up. A cleanup still going at the cleanup deadline closes unfinished.',
	},
	ReadinessGate: {
		title: 'How the readiness gate converges',
		text: 'The suite declares 1,000 parcels, and the footprint is at version 1. The parcels and tracking dispatchers each run a round from version 1. The tracking round derives 1,000 tracking records and declares them, so the footprint grows to version 2. Both rounds report prepared on version 1, which no longer counts, and the gate stays closed. Each dispatcher checks whether anything new was reported where its hook walks; nothing was, so each settles on version 2 without running its hook again (a hook that walks the whole footprint runs a second round instead). Every instance has now prepared on the current version, and the gate opens.',
	},
	Delivery: {
		title: 'How a transition reaches every dispatcher',
		text: 'The orchestrator writes a transition to its record first. It then sends a signed notification to every instance on the run. Two instances acknowledge. The notification to the tracking instance fails. The tracking instance polls the event feed, finds the event, and catches up. Every instance reads the run and acts on its state.',
	},
	DispatcherHooks: {
		title: 'A dispatcher’s two hooks, against the footprint',
		text: 'The suite declares 1,000 parcels in the run’s footprint. The tracking team’s Prepare hook walks the footprint and declares the 1,000 tracking records its service will create from them. The gate opens and the traffic creates those records. After the traffic, the Cleanup hook walks the tracking team’s own claims, deletes those rows from its Postgres, checks they are gone, and reports them removed and verified. Every claim is appended, and says who made it.',
	},
	PrometheusTopologies: {
		title: 'Where a service’s metrics are',
		text: 'Three ways an environment keeps metrics, in two regions, eu-1 and eu-2. Per app, per region: each app runs its own Prometheus in each region, as an HA pair, and the suite’s check asks the parcels team’s pair in each region, one replica of each. Per region: one shared Prometheus in each region scrapes every app, and the check asks each with a selector, app="parcels". Global: each region’s Prometheus feeds one global view, such as Thanos or Mimir, and the check asks that one endpoint with the selector.',
	},
} as const satisfies Record<string, { title: string; text: string }>;

export type DiagramName = keyof typeof DIAGRAMS;
