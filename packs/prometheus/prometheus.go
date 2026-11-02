// Package sbprometheus is the stormbreaker-prometheus pack: SLO checks over a run's window,
// from metrics, across every region a service runs in.
//
// A Prometheus service names one logical service and where its metrics are, however the
// environment keeps them: a Prometheus of the app's own in each region or zone, one shared
// Prometheus per region, or a single global view (Thanos, Mimir, a central Prometheus). Its url
// lists those servers, comma-separated; replicas of one server (an HA pair) are separated by |,
// and the first that answers is asked, so a pair is never counted twice. A selector picks the
// service's series out of a Prometheus that holds more than it. Checks merge what the servers
// report as one Prometheus would: counters are summed, and histogram buckets are summed before a quantile is
// interpolated from them. Percentiles are never averaged. A check judges the run's window, and
// polls like every other check that waits on something asynchronous, so scrape lag is waited
// out rather than slept around. A window shorter than the service's minimum (its scrape
// interval) is refused, because it cannot be judged honestly.
package sbprometheus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nimbusxr/axx/core"

	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	"github.com/nimbusxr/stormbreaker/protocol"
	"github.com/nimbusxr/stormbreaker/slo"
)

// Name is the pack's name.
const Name = "stormbreaker-prometheus"

// Service is a Prometheus service: one logical service, and the Prometheus servers that hold
// its metrics.
type Service struct {
	Name string
	// Sources are the servers whose series are summed: one per region, per app per region, or one
	// global view. Each is its replicas' URLs, asked in order until one answers.
	Sources [][]string
	// Selector is label matchers, without braces, that pick the service's series out of a
	// Prometheus that holds other services' too, like app="parcels". Empty selects every series.
	Selector string
	// MinimumWindow is the shortest window a check judges: the scrape interval, or more.
	MinimumWindow time.Duration
}

type scenarioContext struct {
	services *core.Services[*Service]
}

const registerExpr = "the {word} prometheus service with the following properties:"

var stateKey = core.NewStateKey(Name, func(*core.Scenario) *scenarioContext {
	return &scenarioContext{services: core.NewServices[*Service]("Prometheus service", "").RegisteredBy(registerExpr)}
}, nil)

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Pack returns the stormbreaker-prometheus pack.
func Pack() core.Pack { return pack{} }

type pack struct{}

func (pack) Manifest() core.Manifest {
	return core.Manifest{
		Name: Name, Namespace: Name,
		Doc: "SLO checks over a stormbreaker run's window, from Prometheus, across every region a service runs in: from a " +
			"Prometheus of the app's own in each region or zone, a shared Prometheus per region, or a global view. Counters are " +
			"summed across servers, and histogram buckets are merged before a quantile is interpolated: percentiles are never " +
			"averaged. Checks poll until they hold or their patience runs out. Builds on stormbreaker-core (list it in " +
			"axx-packs.yaml), whose run gives the window.",
		Params: []core.ParamType{
			{
				Name: "prometheusService", Regexps: []string{`([^\s]+)`}, Doc: "the name of a Prometheus service the scenario registered",
				Examples: []string{"parcels-metrics"},
				Transform: func(sc *core.Scenario, name string, _ []*string) (any, error) {
					return stateKey.Of(sc).services.Get(name)
				},
			},
			{
				Name: "quantile", Regexps: []string{`p(\d{1,2}(?:\.\d+)?)`}, Doc: "a quantile, like p99 or p99.9",
				Examples: []string{"p50", "p99", "p99.9"}, Snippet: true,
				Transform: func(_ *core.Scenario, match string, groups []*string) (any, error) {
					if len(groups) == 0 || groups[0] == nil {
						return nil, fmt.Errorf("invalid quantile %q", match)
					}
					p, err := strconv.ParseFloat(*groups[0], 64)
					if err != nil {
						return nil, fmt.Errorf("invalid quantile %q", match)
					}
					return p / 100, nil
				},
			},
		},
		Steps: []core.StepDef{
			{
				ID: "stormbreaker.prometheus.service", Keyword: "Given", Arg: core.ArgTable, Expr: registerExpr,
				Doc: "Register a Prometheus service: one logical service, and the Prometheus servers that hold its metrics, in " +
					"every region it runs in. The first one registered in a scenario is the default.",
				Table: &core.TableDoc{
					Columns: []string{"property", "value"},
					Rows: []core.TableRow{
						{Name: "url", Takes: "the Prometheus servers whose series are summed, comma-separated: one per region, per app per " +
							"region or zone, or a single global view; replicas of one server (an HA pair) separated by `|`, the first that " +
							"answers asked; it can use `${env:…}`", Required: true},
						{Name: "selector", Takes: "label matchers that pick the service's series out of a Prometheus that holds other " +
							"services' too, like `app=\"parcels\"`", Default: "every series"},
						{Name: "minimum-window", Takes: "the shortest window a check judges, the scrape interval or more, like `30s`", Default: "30s"},
					},
				},
				Examples: []string{
					"Given the parcels-metrics prometheus service with the following properties:\n" +
						"  | url | http://prom-east:9090,http://prom-west:9090 |",
					"Given the parcels-metrics prometheus service with the following properties:\n" +
						"  | url      | http://parcels-prom-0.eu-1:9090\\|http://parcels-prom-1.eu-1:9090,http://parcels-prom-0.eu-2:9090\\|http://parcels-prom-1.eu-2:9090 |",
					"Given the parcels-metrics prometheus service with the following properties:\n" +
						"  | url      | http://thanos-query:9090 |\n" +
						"  | selector | app=\"parcels\"           |",
				},
				Run: addService,
			},
			{
				ID: "stormbreaker.prometheus.quantile", Keyword: "Then",
				Expr: "within {duration} the {quantile} of {word} over the run[[ on {prometheusService}]] is at most {float} seconds",
				Doc: "Check a latency quantile of a histogram (its name without `_bucket`) over the run's window. The servers' " +
					"buckets are summed, then the quantile is interpolated as `histogram_quantile` does.",
				Examples: []string{"Then within 30s the p99 of http_server_requests_seconds over the run is at most 0.5 seconds"},
				Run:      checkQuantile,
			},
			{
				ID: "stormbreaker.prometheus.increase", Keyword: "Then",
				Expr:     "within {duration} the increase of {word} over the run[[ on {prometheusService}]] is at least {float}",
				Doc:      "Check how much a counter increased over the run's window, summed across the servers.",
				Examples: []string{"Then within 30s the increase of http_server_requests_seconds_count over the run is at least 45"},
				Run:      checkIncrease,
			},
		},
	}
}

func addService(sc *core.Scenario, a core.Args) error {
	pairs, err := a.Table.Pairs()
	if err != nil {
		return err
	}
	svc := &Service{Name: a.String(0), MinimumWindow: 30 * time.Second}
	for _, p := range pairs {
		v := sc.Suite().Interpolate(p.Value)
		switch p.Key {
		case "url":
			for _, src := range strings.Split(v, ",") {
				var replicas []string
				for _, u := range strings.Split(src, "|") {
					if u = strings.TrimRight(strings.TrimSpace(u), "/"); u != "" {
						replicas = append(replicas, u)
					}
				}
				if len(replicas) > 0 {
					svc.Sources = append(svc.Sources, replicas)
				}
			}
		case "selector":
			sel := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "{"), "}"))
			if sel == "" || strings.ContainsAny(sel, "{}") || !strings.ContainsAny(sel, "=~") {
				return fmt.Errorf("a selector is label matchers, like app=\"parcels\", not %q", v)
			}
			svc.Selector = sel
		case "minimum-window":
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return fmt.Errorf("minimum-window is a duration, like 30s, not %q", v)
			}
			svc.MinimumWindow = d
		default:
			return fmt.Errorf("a Prometheus service has a url, a selector and a minimum-window, not %q", p.Key)
		}
	}
	if len(svc.Sources) == 0 {
		return fmt.Errorf("Prometheus service %q needs a url", svc.Name) //nolint:staticcheck // the service's name starts the sentence
	}
	return stateKey.Of(sc).services.Add(svc.Name, svc)
}

// service is the step's named service, or the scenario's default.
func service(sc *core.Scenario, a core.Args, i int) (*Service, error) {
	if a.Present(i) {
		if s, ok := a.Value(i).(*Service); ok {
			return s, nil
		}
	}
	return stateKey.Of(sc).services.Default()
}

// rangeOf is a PromQL range covering the window, in whole seconds.
func rangeOf(w slo.Window) string { return strconv.FormatInt(w.Seconds(), 10) + "s" }

func checkQuantile(sc *core.Scenario, a core.Args) error {
	patience := a.Value(0).(time.Duration)
	q := a.Value(1).(float64)
	metric := a.String(2)
	limit, err := strconv.ParseFloat(a.Raw(4), 64)
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 3)
	if err != nil {
		return err
	}
	verdict, found := judging(sc, svc.Name), ""
	return verdict(&found, slo.Judge(sc.Context(), patience, func(ctx context.Context) error {
		w, err := slo.RunWindow(sc, svc.Name, svc.MinimumWindow)
		if err != nil {
			return err
		}
		var servers []map[float64]float64
		for _, src := range svc.Sources {
			b, err := bucketIncrease(ctx, src, svc.series(metric+"_bucket"), w)
			if err != nil {
				return err
			}
			servers = append(servers, b)
		}
		v, err := slo.Quantile(q, slo.Merge(servers...))
		if err != nil {
			return core.Failf("the %s of %s over the run: %v", a.Raw(1), metric, err)
		}
		found = fmt.Sprintf("%.3gs", v)
		if v > limit {
			return core.Fail(fmt.Sprintf("the %s of %s over the run is %gs, above the %gs limit", a.Raw(1), metric, v, limit),
				fmt.Sprintf("at most %gs", limit), fmt.Sprintf("%gs", v))
		}
		return nil
	}))
}

func checkIncrease(sc *core.Scenario, a core.Args) error {
	patience := a.Value(0).(time.Duration)
	metric := a.String(1)
	minimum, err := strconv.ParseFloat(a.Raw(3), 64)
	if err != nil {
		return err
	}
	svc, err := service(sc, a, 2)
	if err != nil {
		return err
	}
	verdict, found := judging(sc, svc.Name), ""
	return verdict(&found, slo.Judge(sc.Context(), patience, func(ctx context.Context) error {
		w, err := slo.RunWindow(sc, svc.Name, svc.MinimumWindow)
		if err != nil {
			return err
		}
		total := 0.0
		for _, src := range svc.Sources {
			n, err := totalIncrease(ctx, src, svc.series(metric), w)
			if err != nil {
				return err
			}
			total += n
		}
		found = fmt.Sprintf("%.4g", total)
		if total < minimum {
			return core.Fail(fmt.Sprintf("the increase of %s over the run is %g, below the required %g", metric, total, minimum),
				fmt.Sprintf("at least %g", minimum), fmt.Sprintf("%g", total))
		}
		return nil
	}))
}

// judging says in the run's progress (ADR 0027) that the step's check is waiting, for whoever
// watches the run, and returns what says its verdict once it has one: err, which it hands back,
// with what the check found.
func judging(sc *core.Scenario, source string) func(found *string, err error) error {
	r, ok := sbcore.Current(sc)
	step := sc.Step()
	if !ok || step == nil {
		return func(_ *string, err error) error { return err }
	}
	r.Check(protocol.CheckResult{Check: step.Text, Source: source, Status: "waiting"})
	return func(found *string, err error) error {
		c := protocol.CheckResult{Check: step.Text, Source: source, Status: "passed", Found: *found}
		if err != nil {
			c.Status = "failed"
		}
		r.Check(c)
		return err
	}
}

// promResult is the result of an instant query.
type promResult []struct {
	Metric map[string]string `json:"metric"`
	Value  [2]any            `json:"value"`
}

// series is a metric's series in PromQL: the service's, by its selector.
func (s *Service) series(metric string) string {
	if s.Selector == "" {
		return metric
	}
	return metric + "{" + s.Selector + "}"
}

// ask runs an instant query on a server: its replicas in order, until one answers. Replicas of an
// HA pair hold the same series, so one answer is the server's; summing both would count it twice.
func ask(ctx context.Context, replicas []string, promql string, at time.Time) (promResult, error) {
	var errs []error
	for _, replica := range replicas {
		res, err := query(ctx, replica, promql, at)
		if err == nil {
			return res, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func query(ctx context.Context, source, promql string, at time.Time) (promResult, error) {
	u := source + "/api/v1/query?" + url.Values{
		"query": {promql}, "time": {strconv.FormatFloat(float64(at.UnixMilli())/1000, 'f', 3, 64)},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Prometheus at %s: %w", source, err) //nolint:staticcheck // a product name starts the sentence
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			ResultType string     `json:"resultType"`
			Result     promResult `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("Prometheus at %s answered %d with no JSON: %w", source, resp.StatusCode, err) //nolint:staticcheck // a product name starts the sentence
	}
	if resp.StatusCode != http.StatusOK || body.Status != "success" {
		return nil, fmt.Errorf("Prometheus at %s refused %s: %d %s", source, promql, resp.StatusCode, body.Error) //nolint:staticcheck // a product name starts the sentence
	}
	return body.Data.Result, nil
}

func sampleValue(v [2]any) (float64, error) {
	s, ok := v[1].(string)
	if !ok {
		return 0, fmt.Errorf("a sample's value is %v, not a number in a string", v[1])
	}
	return strconv.ParseFloat(s, 64)
}

// bucketIncrease is a histogram's cumulative bucket counts over the window, summed across every
// series of buckets the server holds: upper bound (le) to count.
func bucketIncrease(ctx context.Context, replicas []string, buckets string, w slo.Window) (map[float64]float64, error) {
	res, err := ask(ctx, replicas, "sum by (le) (increase("+buckets+"["+rangeOf(w)+"]))", w.To)
	if err != nil {
		return nil, err
	}
	out := map[float64]float64{}
	for _, s := range res {
		le, err := strconv.ParseFloat(s.Metric["le"], 64) // ParseFloat reads "+Inf"
		if err != nil {
			return nil, fmt.Errorf("a bucket of %s has le %q", buckets, s.Metric["le"])
		}
		n, err := sampleValue(s.Value)
		if err != nil {
			return nil, err
		}
		out[le] = n
	}
	return out, nil
}

// totalIncrease is a counter's increase over the window, summed across the server's series.
func totalIncrease(ctx context.Context, replicas []string, series string, w slo.Window) (float64, error) {
	res, err := ask(ctx, replicas, "sum(increase("+series+"["+rangeOf(w)+"]))", w.To)
	if err != nil || len(res) == 0 {
		return 0, err
	}
	return sampleValue(res[0].Value)
}
