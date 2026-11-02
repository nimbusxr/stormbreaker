package sdk

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/nimbusxr/stormbreaker/internal/dispatcher"
	"github.com/nimbusxr/stormbreaker/protocol"
)

// footprintOf is a footprint of claims that filters as the orchestrator does.
func footprintOf(claims ...protocol.Claim) dispatcher.Footprint {
	return func(_ context.Context, only protocol.FootprintFilter) iter.Seq2[protocol.Claim, error] {
		return func(yield func(protocol.Claim, error) bool) {
			for _, c := range claims {
				if !only.Matches(c.Dispatcher, c.Store, c.Locator) {
					continue
				}
				if !yield(c, nil) {
					return
				}
			}
		}
	}
}

func TestHooksSpeakTheProtocol(t *testing.T) {
	hooks := Hooks{
		Prepare: func(ctx context.Context, run *Run, declare func(Entry) error) error {
			for id, err := range run.Identities(ctx, "svc", "/c") {
				if err != nil {
					return err
				}
				if err := declare(Entry{Store: "db", Locator: "tracking", Identities: []string{"TRK-" + id}}); err != nil {
					return err
				}
			}
			return nil
		},
		Cleanup: func(ctx context.Context, run *Run, remove func(Removal) error) (bool, error) {
			if err := remove(Removal{Store: "db", Locator: "tracking", Identities: []string{"TRK-a"}}); err != nil {
				return false, err
			}
			return true, remove(Removal{Store: "topic", Locator: "tracking", Identities: []string{"TRK-a"}, CleanBy: "2026-11-15"})
		},
	}.runtime("tracking")
	var got []protocol.FootprintEntry
	collect := func(e protocol.FootprintEntry) error { got = append(got, e); return nil }
	fp := footprintOf(
		protocol.Claim{Store: "svc", Locator: "/c", Identity: "a"},
		protocol.Claim{Store: "svc", Locator: "/c", Identity: "a"},
		protocol.Claim{Store: "other", Locator: "/c", Identity: "z"},
		protocol.Claim{Store: "svc", Locator: "/c", Identity: "b"},
	)
	run := &protocol.Run{ID: "run-1", Group: "delivery"}
	if err := hooks.Prepare(context.Background(), run, fp, collect); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range got {
		if e.Resolution != protocol.ResolutionExpected {
			t.Errorf("declared %+v", e)
		}
		ids = append(ids, e.Identities...)
	}
	if !slices.Equal(ids, []string{"TRK-a", "TRK-a", "TRK-b"}) {
		t.Errorf("declared %v", ids)
	}
	got = nil
	complete, err := hooks.Cleanup(context.Background(), run, fp, collect)
	if err != nil || !complete {
		t.Fatalf("cleanup: %v, complete %t", err, complete)
	}
	if got[0].Resolution != protocol.ResolutionRemovedVerified || got[1].Resolution != protocol.ResolutionCleanBy || got[1].CleanBy != "2026-11-15" {
		t.Errorf("cleanup reported %+v", got)
	}

	bad := Hooks{Cleanup: func(_ context.Context, _ *Run, remove func(Removal) error) (bool, error) {
		return false, remove(Removal{Store: "s", Locator: "l", CleanBy: "next week"})
	}}.runtime("tracking")
	if _, err := bad.Cleanup(context.Background(), run, fp, collect); !errors.Is(err, ErrBlocked) {
		t.Errorf("a removal promised by 'next week': %v", err)
	}
	if none := (Hooks{}).runtime("tracking"); none.Prepare != nil || none.Cleanup != nil {
		t.Error("nil hooks became something")
	}
}

func TestUnansweredIsWhatTheDispatcherHasNotYetAnsweredFor(t *testing.T) {
	expected := func(who, id string) protocol.Claim {
		return protocol.Claim{Dispatcher: who, Instance: "shared", Store: "db", Locator: "tracking", Identity: id, Resolution: protocol.ResolutionExpected}
	}
	answered := func(id, resolution string) protocol.Claim {
		c := expected("tracking", id)
		c.Resolution = resolution
		return c
	}
	// Declared a, b, c and d; then removed a and promised b, before a restart; c declared twice.
	fp := footprintOf(
		expected("tracking", "a"), expected("tracking", "b"), expected("tracking", "c"), expected("tracking", "c"), expected("tracking", "d"),
		expected("parcels", "e"),
		answered("a", protocol.ResolutionRemovedVerified), answered("b", protocol.ResolutionCleanBy),
		expected("tracking", "d"), // said again: still open
	)
	var left []string
	for id, err := range view(&protocol.Run{ID: "run-1"}, fp, "tracking").Unanswered(context.Background(), "db", "tracking") {
		if err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	if !slices.Equal(left, []string{"c", "d"}) {
		t.Errorf("unanswered %v, want [c d]: once each, and none another dispatcher's", left)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("STORMBREAKER_DISPATCHER", "")
	if _, err := ConfigFromEnv(); err == nil || !strings.HasPrefix(err.Error(), "set STORMBREAKER_CALLBACK_URL, STORMBREAKER_DISPATCHER, STORMBREAKER_INSTANCE, STORMBREAKER_ORCHESTRATOR, STORMBREAKER_TEAM (STORMBREAKER_INSTANCE is this instance's stable name") {
		t.Errorf("nothing set: %v", err)
	}
	t.Setenv("STORMBREAKER_DISPATCHER", "parcels")
	t.Setenv("STORMBREAKER_TEAM", "pay")
	t.Setenv("STORMBREAKER_CALLBACK_URL", "http://parcels-dispatcher:8090/events")
	t.Setenv("STORMBREAKER_ORCHESTRATOR", "http://orchestrator:8080")
	t.Setenv("STORMBREAKER_GROUPS", " delivery, ,parcels ")
	t.Setenv("STORMBREAKER_INSTANCE", "east")
	t.Setenv("STORMBREAKER_REASSERT", "5s")
	c, err := ConfigFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "parcels" || c.Instance != "east" || !slices.Equal(c.Groups, []string{"delivery", "parcels"}) || c.Reassert.String() != "5s" {
		t.Errorf("config %+v", c)
	}
	t.Setenv("STORMBREAKER_REASSERT", "often")
	if _, err := ConfigFromEnv(); err == nil {
		t.Error("STORMBREAKER_REASSERT=often was accepted")
	}
}
