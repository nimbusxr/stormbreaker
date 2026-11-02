package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	app := &App{Out: &out, Err: &out}
	root := newRoot(app)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("stormbreaker %v: %v", args, err)
	}
	return out.String()
}

func TestVersion(t *testing.T) {
	if got := run(t, "version"); !strings.HasPrefix(got, "stormbreaker v") {
		t.Errorf("version printed %q", got)
	}
}

func TestVersionJSON(t *testing.T) {
	var info struct {
		Version, Platform, Channel string
	}
	if err := json.Unmarshal([]byte(run(t, "version", "--json")), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" || info.Platform == "" || info.Channel == "" {
		t.Errorf("version --json gave %+v", info)
	}
}
