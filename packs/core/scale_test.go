//go:build scale

package sbcore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/nimbusxr/axx/core"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/internal/labtest"
)

// TestBindAtScale binds a stream of 60 million keys: every record generated, and every identity
// declared to the orchestrator before any traffic, as a scenario's bind does.
//
//	go test -tags scale -run BindAtScale -timeout 60m -v ./packs/core
func TestBindAtScale(t *testing.T) {
	n := 60_000_000
	if s := os.Getenv("STORMBREAKER_SCALE_IDENTITIES"); s != "" {
		n, _ = strconv.Atoi(s)
	}
	lab := labtest.New(t)
	lab.Start("parcels", "east", []string{"delivery"}, dispatcher.Hooks{})
	root := t.TempDir()
	files := map[string]string{
		"parcel.factory.yaml":   "factory:\n  family: json\nidentity:\n  - path: reference\n    format: uuid-name-based\n",
		"parcel.prototype.yaml": "data:\n  country: DE\n  serviceLevel: STANDARD\n",
		"standard.fixture.yaml": "factory: parcel\ndata:\n  sender: kestrel-books\n  pieces: 2\n",
		"big.stream.yaml":       fmt.Sprintf("count: %d\nmix: [{fixture: parcel#standard, weight: 1}]\n", n),
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := json.Marshal(Config{URL: lab.URL, Token: labtest.Token, Fixtures: root})
	suite := core.NewSuite(core.SuiteOptions{PackConfig: map[string]json.RawMessage{Name: cfg}, Interpolate: func(s string) string { return s }})
	sc := scenario(suite, &sink{}, "@stormbreaker-group/delivery")
	if err := hook(t, "stormbreaker.run.open")(sc); err != nil {
		t.Fatal(err)
	}
	r, _ := Current(sc)
	begin := time.Now()
	if _, err := r.Bind(sc.Context(), "big", "parcels", "/parcels"); err != nil {
		t.Fatal(err)
	}
	bind := time.Since(begin)
	run, err := r.Client().Run(sc.Context(), r.ID())
	if err != nil || run.Claims != int64(n) {
		t.Fatalf("claims %d, %v", run.Claims, err)
	}
	begin = time.Now()
	if err := r.Ready(sc.Context()); err != nil {
		t.Fatal(err)
	}
	ready := time.Since(begin)
	t.Logf("bound %d keys (generated and declared, lint skipped past a million) in %s: %.0f identities/s", n, bind.Round(time.Millisecond), float64(n)/bind.Seconds())
	t.Logf("the gate opened %s later", ready.Round(time.Millisecond))
	sc.SetStatus("failed") // give the run up rather than wait out its cleanup here
	_ = sc.Close()
}
