package streams

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nimbusxr/axx/fixtures"
)

// uuidFn is a team's identifier function, the way a team writes one: derived from the record
// being generated, so it is deterministic and recomputable, and named, so one record can carry
// several. Name-based (MD5, version 3), as Java's UUID.nameUUIDFromBytes.
type uuidFn struct{}

func (uuidFn) Name() string    { return "uuid" }
func (uuidFn) Version() string { return "1" }
func (uuidFn) Recorded() bool  { return false }
func (uuidFn) Apply(args []string, scope *fixtures.InstanceScope) (string, error) {
	name := "id"
	if len(args) > 0 {
		name = args[0]
	}
	sum := md5.Sum([]byte(scope.Material(name)))
	sum[6] = sum[6]&0x0f | 0x30
	sum[8] = sum[8]&0x3f | 0x80
	h := hex.EncodeToString(sum[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:], nil
}

// traceFn is a team's tracing function: given what a trace is shared by and a name for this
// message's span, a W3C traceparent.
type traceFn struct{}

func (traceFn) Name() string    { return "traceparent" }
func (traceFn) Version() string { return "1" }
func (traceFn) Recorded() bool  { return false }
func (traceFn) Apply(args []string, _ *fixtures.InstanceScope) (string, error) {
	if len(args) != 2 {
		return "", fmt.Errorf("traceparent(shared, span) takes two arguments, got %v", args)
	}
	h := func(s string, n int) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:])[:n] }
	return "00-" + h(args[0]+"|trace", 32) + "-" + h(args[0]+"|"+args[1], 16) + "-01", nil
}

const root = "testdata/root"

var window = time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)

func functions() []fixtures.Function { return []fixtures.Function{uuidFn{}, traceFn{}} }

func open(t *testing.T, def *Definition, seed, run string) *Stream {
	t.Helper()
	s, err := Open(def, root, Options{Seed: seed, Run: run, Window: window, Functions: functions()})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func load(t *testing.T, name string) *Definition {
	t.Helper()
	path, err := Find(root, name)
	if err != nil {
		t.Fatal(err)
	}
	def, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func parse(t *testing.T, name, y string) *Definition {
	t.Helper()
	def, err := Parse(name, []byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return def
}

func records(t *testing.T, s *Stream, from, to int64) []*Record {
	t.Helper()
	var out []*Record
	for r, err := range s.Range(from, to) {
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func identities(t *testing.T, s *Stream) []string {
	t.Helper()
	var out []string
	if _, err := s.Identities(7, func(b []string) error { out = append(out, b...); return nil }); err != nil {
		t.Fatal(err)
	}
	return out
}

func bytesOf(rs []*Record) [][]byte {
	var out [][]byte
	for _, r := range rs {
		out = append(out, CanonicalBytes(r))
	}
	return out
}

func TestTheSameInputsGiveByteIdenticalStreams(t *testing.T) {
	def := load(t, "parcels-load")
	a := bytesOf(records(t, open(t, def, "run-1", "run-1"), 0, def.Count))
	b := bytesOf(records(t, open(t, def, "run-1", "run-1"), 0, def.Count))
	if len(a) != 200 || !slices.EqualFunc(a, b, bytes.Equal) {
		t.Fatalf("two generations of the same stream differ (%d and %d records)", len(a), len(b))
	}
}

func TestRecomputingLaterMatchesWhatWasDeclared(t *testing.T) {
	def := load(t, "parcels-load")
	declared := identities(t, open(t, def, "run-1", "run-1"))
	later := identities(t, open(t, def, "run-1", "run-1"))
	if len(declared) != 200 || !slices.Equal(declared, later) {
		t.Errorf("recomputed %d identities, declared %d", len(later), len(declared))
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-3[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestIdentitiesLookRealNothingAnnouncesThemAsTestData(t *testing.T) {
	for _, id := range identities(t, open(t, load(t, "parcels-load"), "run-1", "run-1")) {
		if !uuidPattern.MatchString(id) {
			t.Fatalf("identity %q is not an ordinary UUID", id)
		}
	}
}

func TestDifferentRunsAndSeedsGiveDifferentIdentities(t *testing.T) {
	def := load(t, "parcels-load")
	base := identities(t, open(t, def, "run-1", "run-1"))
	for _, other := range [][2]string{{"run-1", "run-2"}, {"seed-2", "run-1"}} {
		ids := identities(t, open(t, def, other[0], other[1]))
		for _, id := range ids {
			if slices.Contains(base, id) {
				t.Errorf("seed %s run %s shares identity %s with run-1", other[0], other[1], id)
				break
			}
		}
	}
}

func TestCorrelatedStreamsAgreeRecordForRecord(t *testing.T) {
	keys := open(t, parse(t, "keys", "count: 20\nmix: [{fixture: parcel#correlated-key, weight: 1}]"), "run-1", "run-1")
	values := open(t, parse(t, "values", "count: 20\nmix: [{fixture: parcel#correlated-value, weight: 1}]"), "run-1", "run-1")
	if a, b := identities(t, keys), identities(t, values); !slices.Equal(a, b) {
		t.Errorf("two streams of the same run differ:\n%v\n%v", a[:3], b[:3])
	}
}

func TestAuthoredIdentitiesAreKeptAndTheLintCatchesThemPinnedAcrossKeys(t *testing.T) {
	s := open(t, parse(t, "pinned", "count: 3\nmix: [{fixture: parcel#fixed-1001, weight: 1}]"), "run-1", "run-1")
	if ids := identities(t, s); !slices.Equal(ids, []string{"PX-1001", "PX-1001", "PX-1001"}) {
		t.Errorf("authored identities: %v", ids)
	}
	var ce *CollisionError
	if err := s.Lint(); !errors.As(err, &ce) || !strings.Contains(err.Error(), "PX-1001 (keys 0 and 1, from parcel#fixed-1001 and parcel#fixed-1001)") {
		t.Errorf("the lint: %v", err)
	}
}

func TestTheLintPassesAndTheStreamMixesByWeight(t *testing.T) {
	s := open(t, load(t, "parcels-load"), "run-1", "run-1")
	if err := s.Lint(); err != nil {
		t.Fatal(err)
	}
	small := 0
	for _, r := range records(t, s, 0, 200) {
		if r.Ref == "parcel#standard" {
			small++
		}
	}
	// 8 to 2: about 160 of 200.
	if small < 140 || small > 180 {
		t.Errorf("%d of 200 keys took the weight-8 shape", small)
	}
}

func TestASeriesSharesItsIdentityThroughReferenceAndItsTraceBySpan(t *testing.T) {
	s := open(t, load(t, "parcel-lifecycle"), "run-1", "run-1")
	rs := records(t, s, 0, 3)
	byKey := map[int64][]*Record{}
	for _, r := range rs {
		byKey[r.Key] = append(byKey[r.Key], r)
	}
	series := 0
	for key, msgs := range byKey {
		if len(msgs) == 1 {
			if msgs[0].Ref != "parcel#bulky" {
				t.Errorf("key %d: a single message from %s", key, msgs[0].Ref)
			}
			continue
		}
		series++
		created, completed := msgs[0], msgs[1]
		if created.Ref != "parcel#standard" || completed.Ref != "parcel-collection#booked" || completed.Position != 1 {
			t.Fatalf("key %d's series: %s, %s", key, created.Ref, completed.Ref)
		}
		if created.Identity() != completed.Identity() || completed.Data.Get("sender") != "kestrel-books" {
			t.Errorf("key %d: the booking does not name the registered parcel", key)
		}
		ct, dt := fmt.Sprint(created.Metadata.Get("trace")), fmt.Sprint(completed.Metadata.Get("trace"))
		if ct[:35] != dt[:35] || ct == dt {
			t.Errorf("key %d: traces %s and %s should share a trace id and differ by span", key, ct, dt)
		}
		if created.Metadata.Get("key") != created.Identity() {
			t.Errorf("key %d: metadata key %v, identity %s", key, created.Metadata.Get("key"), created.Identity())
		}
	}
	if series == 0 {
		t.Error("no key took the series shape")
	}
}

func TestRangesGiveExactlyTheWholeStream(t *testing.T) {
	def := load(t, "parcel-lifecycle")
	def.Count = 50
	s := open(t, def, "run-1", "run-1")
	whole := bytesOf(records(t, s, 0, 50))
	var parts [][]byte
	for _, rg := range [][2]int64{{0, 7}, {7, 30}, {30, 31}, {31, 50}} {
		parts = append(parts, bytesOf(records(t, s, rg[0], rg[1]))...)
	}
	if !slices.EqualFunc(whole, parts, bytes.Equal) {
		t.Error("ranges do not reassemble into the whole stream")
	}
	// A range depends on nothing before it: generated alone, in a fresh stream, it is the same.
	alone := bytesOf(records(t, open(t, def, "run-1", "run-1"), 31, 50))
	if !slices.EqualFunc(alone, parts[len(parts)-len(alone):], bytes.Equal) {
		t.Error("a range generated alone differs from the same range of the whole")
	}
}

func TestDifferentKeysNeverShareAnIdentity(t *testing.T) {
	seen := map[string]int64{}
	for _, r := range records(t, open(t, load(t, "parcel-lifecycle"), "run-1", "run-1"), 0, 3) {
		for _, id := range r.Identities {
			if k, ok := seen[id]; ok && k != r.Key {
				t.Errorf("keys %d and %d share %s", k, r.Key, id)
			}
			seen[id] = r.Key
		}
	}
}

func TestAnIdentityWithNoValueIsDerivedForEachRecord(t *testing.T) {
	s := open(t, parse(t, "bare", "count: 5\nmix: [{fixture: bare/bare#no-identity, weight: 1}]"), "run-1", "run-1")
	ids := identities(t, s)
	if len(ids) != 5 || len(slices.Compact(slices.Sorted(slices.Values(ids)))) != 5 {
		t.Errorf("derived identities %v", ids)
	}
	if err := s.Lint(); err != nil {
		t.Error(err)
	}
}

func TestOverridesAndTheWindowReachEveryRecord(t *testing.T) {
	s := open(t, parse(t, "vip", "count: 2\nmix: [{fixture: parcel#standard, weight: 1, set: {serviceLevel: PRIORITY, pieces: 3}}]"), "run-1", "run-1")
	for _, r := range records(t, s, 0, 2) {
		if r.Data.Get("serviceLevel") != "PRIORITY" || fmt.Sprint(r.Data.Get("pieces")) != "3" {
			t.Errorf("key %d: %s", r.Key, r.Data.CompactJSON())
		}
	}
}

func TestDefinitionsAreChecked(t *testing.T) {
	for _, bad := range []string{
		"count: 0\nmix: [{fixture: a#b, weight: 1}]",
		"count: 1\nmix: []",
		"count: 1\nmix: [{fixture: a#b, series: [a#c], weight: 1}]",
		"count: 1\nmix: [{fixture: a#b, weight: 0}]",
		"count: 1\nmix: [{fixture: ab, weight: 1}]",
		"count: 1\nmix: [{weight: 1}]",
		"count: 1\nmixes: []",
	} {
		if _, err := Parse("bad", []byte(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := Open(parse(t, "missing", "count: 1\nmix: [{fixture: parcel#nothing, weight: 1}]"), root, Options{}); err == nil {
		t.Error("a fixture that does not exist was accepted")
	}
	if _, err := Find(root, "nowhere"); err == nil {
		t.Error("found a stream that does not exist")
	}
}
