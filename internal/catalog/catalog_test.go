// Package catalog holds stormbreaker's packs' step text to the frozen catalog, testdata/steps.json:
// step text is public API, so no step changes or disappears by accident.
package catalog

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/nimbusxr/axx/core"

	sbcore "github.com/nimbusxr/stormbreaker/packs/core"
	sbkafka "github.com/nimbusxr/stormbreaker/packs/kafka"
	sbprometheus "github.com/nimbusxr/stormbreaker/packs/prometheus"
	sbrest "github.com/nimbusxr/stormbreaker/packs/rest"
)

var (
	update = flag.Bool("update", false, "add new steps to the catalog (never changes or removes one)")
	prune  = flag.Bool("prune", false, "remove the steps no pack has from the catalog (a decision, with an ADR)")
)

type entry struct {
	Pack string `json:"pack"`
	ID   string `json:"id"`
	Expr string `json:"expr"`
}

func packs() []core.Pack {
	return []core.Pack{sbcore.Pack(), sbrest.Pack(), sbkafka.Pack(), sbprometheus.Pack()}
}

func current() []entry {
	var out []entry
	for _, p := range packs() {
		m := p.Manifest()
		for _, s := range m.Steps {
			out = append(out, entry{Pack: m.Name, ID: s.ID, Expr: s.Expr})
		}
	}
	return out
}

var file = filepath.Join("..", "..", "testdata", "steps.json")

// TestEveryStringSaysWhatItHolds holds the packs to axx's rule for values (its ADR 0014): what a step
// sets or checks is a value, which the step declares, so that axx validate and the run read it.
func TestEveryStringSaysWhatItHolds(t *testing.T) {
	param := regexp.MustCompile(`\{[^}]*\}`)
	for _, p := range packs() {
		for _, s := range p.Manifest().Steps {
			for i, name := range param.FindAllString(s.Expr, -1) {
				if name == "{string}" && !slices.Contains(s.TextArgs, i) && !slices.Contains(s.TypedArgs, i) {
					t.Errorf("step %s: its {string} at place %d holds a value: declare it in TextArgs or TypedArgs", s.ID, i)
				}
			}
		}
	}
}

func TestStepCatalog(t *testing.T) {
	var frozen []entry
	if b, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(b, &frozen); err != nil {
			t.Fatal(err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	now := current()
	byID := func(es []entry) map[string]entry {
		m := map[string]entry{}
		for _, e := range es {
			m[e.ID] = e
		}
		return m
	}
	have, want := byID(now), byID(frozen)
	changed := false
	for _, f := range frozen {
		h, ok := have[f.ID]
		switch {
		case !ok && *prune:
			changed = true
		case !ok:
			t.Errorf("step %s (%q) is gone from %s; removing a step is a decision: run with -prune", f.ID, f.Expr, f.Pack)
		case h.Expr != f.Expr:
			t.Errorf("step %s changed its text from %q to %q; step text is public API: add a step instead", f.ID, f.Expr, h.Expr)
		}
	}
	for _, h := range now {
		if _, ok := want[h.ID]; !ok {
			if *update {
				changed = true
				continue
			}
			t.Errorf("step %s (%q) is not in the catalog: run go test ./internal/catalog -run TestStepCatalog -update", h.ID, h.Expr)
		}
	}
	if changed && !t.Failed() {
		var out []entry
		for _, f := range frozen {
			if _, ok := have[f.ID]; ok {
				out = append(out, f)
			}
		}
		for _, h := range now {
			if _, ok := want[h.ID]; !ok {
				out = append(out, h)
			}
		}
		slices.SortFunc(out, func(a, b entry) int {
			if a.Pack != b.Pack {
				return compare(a.Pack, b.Pack)
			}
			return compare(a.ID, b.ID)
		})
		b, _ := json.MarshalIndent(out, "", "  ")
		if err := os.WriteFile(file, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func compare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// TestStepIDsAreUnique keeps step IDs unique across the packs, as axx requires.
func TestStepIDsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, e := range current() {
		if p, ok := seen[e.ID]; ok {
			t.Errorf("step %s is in %s and %s", e.ID, p, e.Pack)
		}
		seen[e.ID] = e.Pack
	}
}
