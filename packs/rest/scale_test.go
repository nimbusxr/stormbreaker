//go:build scale

package sbrest

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"
	"github.com/nimbusxr/axx/packs/rest"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
)

// fastServer is a target that answers every request at once, in a process of its own
// (internal/scale/sink), so the measurements are the driver's.
func fastServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	bin := filepath.Join(t.TempDir(), "sink")
	if out, err := exec.Command("go", "build", "-o", bin, "../../internal/scale/sink").CombinedOutput(); err != nil {
		t.Fatalf("build the sink: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "-addr", addr)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	for range 100 {
		if resp, err := http.Get("http://" + addr + "/"); err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return "http://" + addr
}

func scaleScenario(t *testing.T, lab *labtest.Lab, root, connections string) *core.Scenario {
	t.Helper()
	core1, _ := json.Marshal(sbcore.Config{URL: lab.URL, Token: labtest.Token, Fixtures: root, Readiness: "120s", Close: "300s"})
	rest1, _ := json.Marshal(Config{Connections: connections, Timeout: "10s"})
	suite := core.NewSuite(core.SuiteOptions{
		PackConfig:  map[string]json.RawMessage{sbcore.Name: core1, Name: rest1},
		Interpolate: func(s string) string { return s },
	})
	sc := core.NewScenario(context.Background(), core.ScenarioInfo{Name: "scale", Tags: []string{"@stormbreaker-group/delivery"}}, suite, &sink{})
	for _, h := range sbcore.Pack().Manifest().Hooks {
		if h.Phase == core.BeforeScenario {
			if err := h.Run(sc); err != nil {
				t.Fatal(err)
			}
		}
	}
	return sc
}

// TestRESTDriverCeiling is how many requests a second one process sends through stormbreaker-rest:
// bodyless requests over reused connections, at rising rates (STORMBREAKER_SCALE_RATES, e.g.
// "100000,200000,300000"), each for ten seconds.
//
//	go test -tags scale -run RESTDriverCeiling -v ./packs/rest
func TestRESTDriverCeiling(t *testing.T) {
	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	url := fastServer(t)
	seconds := 10
	rates := []int{25_000, 50_000, 75_000, 100_000}
	if s := os.Getenv("STORMBREAKER_SCALE_RATES"); s != "" {
		rates = nil
		for _, f := range strings.Split(s, ",") {
			var r int
			if _, err := fmt.Sscan(strings.TrimSpace(f), &r); err != nil {
				t.Fatalf("STORMBREAKER_SCALE_RATES: %v", err)
			}
			rates = append(rates, r)
		}
	}
	for _, c := range []struct {
		connections string
		rates       []int
	}{{"reuse", rates}} {
		for _, rate := range c.rates {
			sc := scaleScenario(t, lab, t.TempDir(), c.connections)
			if err := rest.Context(sc).AddService(&rest.Service{Name: "fast", URL: url}); err != nil {
				t.Fatal(err)
			}
			if err := sbcore.Ready(sc); err != nil {
				t.Fatal(err)
			}
			err := step(t, stepSend)(sc, args(rate*seconds, "post", "/load", rate, nil))
			res := stateKey.Of(sc).last
			if res == nil {
				t.Fatalf("%s at %d/s: %v", c.connections, rate, err)
			}
			stopped := ""
			if err != nil {
				stopped = "  stopped: " + err.Error()
			}
			t.Logf("%-5s %7d/s asked: %7.0f/s sent (%d), %6d failed, latency p50 %8s p99 %8s, drift p99 %8s max %8s  %s%s",
				c.connections, rate, float64(res.Sent)/res.Elapsed.Seconds(), res.Sent, res.Failed,
				res.Latency.Quantile(0.5), res.Latency.Quantile(0.99), res.Drift.Quantile(0.99), res.Drift.Max(), res.FirstError, stopped)
			sc.SetStatus("failed") // give the run up: nothing to clean
			_ = sc.Close()
			time.Sleep(2 * time.Second) // let the sockets of the last rate settle
		}
	}
}

// TestRESTStreamAtScale is a stream of half a million records, bound (linted, every identity
// declared) and sent at 50,000 a second: generation, requests and the run's cleanup together.
func TestRESTStreamAtScale(t *testing.T) {
	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	url := fastServer(t)
	root := t.TempDir()
	for _, f := range []string{"parcel.factory.yaml", "parcel.prototype.yaml", "standard.fixture.yaml"} {
		b, _ := os.ReadFile(filepath.Join("testdata/root", f))
		_ = os.WriteFile(filepath.Join(root, f), b, 0o644)
	}
	const count, rate = 500_000, 50_000
	_ = os.WriteFile(filepath.Join(root, "big.stream.yaml"), fmt.Appendf(nil, "count: %d\nmix: [{fixture: parcel#standard, weight: 1}]\n", count), 0o644)
	sc := scaleScenario(t, lab, root, "reuse")
	if err := rest.Context(sc).AddService(&rest.Service{Name: "fast", URL: url}); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if err := step(t, "stormbreaker.rest.stream")(sc, args("big", "post", "/parcels/{reference}", nil)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.rest.stream.path-variables")(sc, args("big", "path")); err != nil {
		t.Fatal(err)
	}
	bind := time.Since(begin)
	begin = time.Now()
	if err := step(t, stepExecute)(sc, args("big", rate)); err != nil {
		t.Fatal(err)
	}
	res := stateKey.Of(sc).last
	sent := time.Since(begin)
	begin = time.Now()
	sc.SetStatus("passed")
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	closed := time.Since(begin)
	t.Logf("bound %d records (lint, declare) in %s", count, bind.Round(time.Millisecond))
	t.Logf("sent at %d/s asked: %.0f/s, %d failed, gate wait included %s, drift p99 %s, latency p99 %s",
		rate, float64(res.Sent)/res.Elapsed.Seconds(), res.Failed, sent.Round(time.Millisecond), res.Drift.Quantile(0.99), res.Latency.Quantile(0.99))
	t.Logf("closed (cleanup, journal) in %s", closed.Round(time.Millisecond))
}

// TestRESTSustained is the design target held for its length: a stream bound (every identity
// declared) and sent at 100,000 requests a second for ten minutes, sixty million in all, with the
// driver's heap watched throughout.
//
//	STORMBREAKER_SCALE_SECONDS=600 go test -tags scale -run RESTSustained -timeout 60m -v ./packs/rest
func TestRESTSustained(t *testing.T) {
	seconds, rate := 600, 100_000
	if s := os.Getenv("STORMBREAKER_SCALE_SECONDS"); s != "" {
		if _, err := fmt.Sscan(s, &seconds); err != nil {
			t.Fatal(err)
		}
	}
	if s := os.Getenv("STORMBREAKER_SCALE_RATE"); s != "" {
		if _, err := fmt.Sscan(s, &rate); err != nil {
			t.Fatal(err)
		}
	}
	count := seconds * rate
	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	url := fastServer(t)
	root := t.TempDir()
	for _, f := range []string{"parcel.factory.yaml", "parcel.prototype.yaml", "standard.fixture.yaml"} {
		b, _ := os.ReadFile(filepath.Join("testdata/root", f))
		_ = os.WriteFile(filepath.Join(root, f), b, 0o644)
	}
	_ = os.WriteFile(filepath.Join(root, "big.stream.yaml"), fmt.Appendf(nil, "count: %d\nmix: [{fixture: parcel#standard, weight: 1}]\n", count), 0o644)
	sc := scaleScenario(t, lab, root, "reuse")
	if err := rest.Context(sc).AddService(&rest.Service{Name: "fast", URL: url}); err != nil {
		t.Fatal(err)
	}

	var peakHeap atomic.Uint64
	var peakGoroutines atomic.Int64
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peakHeap.Load() {
				peakHeap.Store(m.HeapInuse)
			}
			if g := int64(runtime.NumGoroutine()); g > peakGoroutines.Load() {
				peakGoroutines.Store(g)
			}
			select {
			case <-stop:
				return
			case <-t.C:
			}
		}
	}()

	begin := time.Now()
	if err := step(t, "stormbreaker.rest.stream")(sc, args("big", "post", "/parcels/{reference}", nil)); err != nil {
		t.Fatal(err)
	}
	if err := step(t, "stormbreaker.rest.stream.path-variables")(sc, args("big", "path")); err != nil {
		t.Fatal(err)
	}
	bind := time.Since(begin)
	sendErr := step(t, stepExecute)(sc, args("big", rate))
	res := stateKey.Of(sc).last
	begin = time.Now()
	sc.SetStatus("passed")
	closeErr := sc.Close()
	closed := time.Since(begin)
	close(stop)
	if res == nil {
		t.Fatalf("nothing sent: %v", sendErr)
	}
	t.Logf("records                %d (%d/s for %ds)", count, rate, seconds)
	t.Logf("bound in               %s", bind.Round(time.Millisecond))
	t.Logf("sent                   %d at %.0f/s over %s, %d failed %s", res.Sent, float64(res.Sent)/res.Elapsed.Seconds(), res.Elapsed.Round(time.Millisecond), res.Failed, res.FirstError)
	t.Logf("client latency         p50 %s, p99 %s, p99.9 %s, max %s", res.Latency.Quantile(0.5), res.Latency.Quantile(0.99), res.Latency.Quantile(0.999), res.Latency.Max())
	t.Logf("arrival drift          p50 %s, p99 %s, max %s", res.Drift.Quantile(0.5), res.Drift.Quantile(0.99), res.Drift.Max())
	t.Logf("closed in              %s", closed.Round(time.Millisecond))
	t.Logf("peak heap              %.0f MB, peak goroutines %d", float64(peakHeap.Load())/1e6, peakGoroutines.Load())
	if sendErr != nil || closeErr != nil || res.Failed > 0 {
		t.Errorf("send: %v; close: %v; %d failed", sendErr, closeErr, res.Failed)
	}
}
