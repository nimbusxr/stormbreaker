// Package streams generates a run's volume test data: deterministic records from the fixtures
// of axx's fixture factories.
//
// A stream is declared as data, in <name>.stream.yaml: how many keys to generate, and the
// weighted mix of shapes those keys take. A shape is a series, the ordered messages one key
// carries, each a fixture ("<factory>#<fixture>"); a stream of single messages is a stream whose
// series are one long. The factory owns what each message is (its values, and the metadata it
// travels with); the stream owns only volume and order.
//
// Nothing is random. Record i is a pure function of the definition, the seed, the run and i:
// each key's shape comes from a hash of the stream's name, the seed, the run and the key, and
// every value from the factory's own expressions, evaluated for that record. Generating the same
// stream again, in another process, next month, gives the same records byte for byte, which is
// what makes cleanup recomputable rather than recorded. And because a key depends on nothing but
// itself, any range of keys can be generated on its own: ranges reassemble exactly into the
// whole stream.
package streams

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/nimbusxr/axx/fixtures"
)

// Suffix is a stream definition file's.
const Suffix = ".stream.yaml"

// Definition is a stream declared as data.
type Definition struct {
	// Name is the stream's, from its file name; never written in it.
	Name string `yaml:"-"`
	// Count is how many keys the stream generates.
	Count int64 `yaml:"count"`
	// Mix is the weighted shapes those keys take.
	Mix []MixEntry `yaml:"mix"`
}

// MixEntry is one shape a key can take: a fixture, or a series of them, with its weight in the
// mix, and values set on each of its messages before anything resolves.
type MixEntry struct {
	Fixture string         `yaml:"fixture"`
	Series  []string       `yaml:"series"`
	Weight  int            `yaml:"weight"`
	Set     map[string]any `yaml:"set"`
}

// Messages are the fixtures a key of this shape carries, in order.
func (e MixEntry) Messages() []string {
	if len(e.Series) > 0 {
		return e.Series
	}
	return []string{e.Fixture}
}

// Parse reads a stream definition named name.
func Parse(name string, data []byte) (*Definition, error) {
	var d Definition
	if err := yaml.UnmarshalWithOptions(data, &d, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("stream %s: %w", name, err)
	}
	d.Name = name
	return &d, d.validate()
}

func (d *Definition) validate() error {
	if d.Count <= 0 {
		return fmt.Errorf("stream %s needs a count above zero", d.Name)
	}
	if len(d.Mix) == 0 {
		return fmt.Errorf("stream %s needs at least one mix entry", d.Name)
	}
	for i, e := range d.Mix {
		switch {
		case e.Fixture != "" && len(e.Series) > 0:
			return fmt.Errorf("stream %s: mix entry %d declares a fixture and a series; a key carries one or the other", d.Name, i+1)
		case e.Fixture == "" && len(e.Series) == 0:
			return fmt.Errorf("stream %s: mix entry %d names no fixture", d.Name, i+1)
		case e.Weight <= 0:
			return fmt.Errorf("stream %s: mix entry %d needs a weight above zero", d.Name, i+1)
		}
		for _, ref := range e.Messages() {
			if f, k, ok := strings.Cut(ref, "#"); !ok || f == "" || k == "" {
				return fmt.Errorf("stream %s: %q is not a fixture reference, <factory>#<fixture>", d.Name, ref)
			}
		}
	}
	return nil
}

// Load reads the stream definition at path; its name is the file's, without .stream.yaml.
func Load(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(strings.TrimSuffix(filepath.Base(path), Suffix), data)
}

// Find finds <name>.stream.yaml under root: exactly one, wherever it is.
func Find(root, name string) (string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "node_modules" || (strings.HasPrefix(d.Name(), ".") && p != root)) {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == name+Suffix {
			found = append(found, p)
		}
		return nil
	})
	switch {
	case err != nil:
		return "", err
	case len(found) == 0:
		return "", fmt.Errorf("no %s%s under %s", name, Suffix, root)
	case len(found) > 1:
		return "", fmt.Errorf("more than one %s%s under %s: %s", name, Suffix, root, strings.Join(found, ", "))
	}
	return found[0], nil
}

var (
	registryMu sync.Mutex
	registry   []fixtures.Function
)

// Register adds expression functions every stream's fixtures can call, for a pack that offers
// them (call it from the pack's Pack function or an init). A function must be pure: a
// deterministic function of its arguments and the record's scope. Recorded functions answer from
// the committed pairings lock only.
func Register(fns ...fixtures.Function) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = append(registry, fns...)
}

func registered() []fixtures.Function {
	registryMu.Lock()
	defer registryMu.Unlock()
	return slices.Clone(registry)
}

// Options say which run a stream generates records for.
type Options struct {
	// Seed and Run key every record: the same seed and run give the same records.
	Seed, Run string
	// Window anchors relative times (run.window): pass the run's window start, never the clock,
	// so the records can be generated again later.
	Window time.Time
	// Functions are expression functions the fixtures call, besides the registered ones.
	Functions []fixtures.Function
}

// Stream is a stream definition opened for a run, over a module's fixture factories.
type Stream struct {
	def    *Definition
	lib    *fixtures.Library
	opts   Options
	shapes []shape
	total  int
}

type shape struct {
	entry    MixEntry
	messages []*fixtures.Fixture
}

// LoadLibrary loads the fixture factories under root, the resource root the factory tooling
// reads, with the registered expression functions and extra.
func LoadLibrary(root string, extra ...fixtures.Function) (*fixtures.Library, error) {
	return fixtures.Load(fixtures.DefaultConfig(root), append(registered(), extra...)...)
}

// Open opens a stream for a run over the fixture factories under root.
func Open(def *Definition, root string, opts Options) (*Stream, error) {
	lib, err := LoadLibrary(root, opts.Functions...)
	if err != nil {
		return nil, err
	}
	return OpenWith(def, lib, opts)
}

// OpenWith opens a stream for a run over a loaded fixtures library.
func OpenWith(def *Definition, lib *fixtures.Library, opts Options) (*Stream, error) {
	if err := def.validate(); err != nil {
		return nil, err
	}
	s := &Stream{def: def, lib: lib, opts: opts}
	for _, e := range def.Mix {
		sh := shape{entry: e}
		for _, ref := range e.Messages() {
			f, err := lib.Fixture(ref)
			if err != nil {
				return nil, fmt.Errorf("stream %s: %w", def.Name, err)
			}
			sh.messages = append(sh.messages, f)
		}
		s.shapes = append(s.shapes, sh)
		s.total += e.Weight
	}
	return s, nil
}

// Definition is the stream's definition.
func (s *Stream) Definition() *Definition { return s.def }

// Record is one generated message.
type Record struct {
	// Key is which key the record belongs to; Position where it falls in that key's series.
	// Together they say which record it is, however the stream was split into ranges.
	Key      int64
	Position int
	// Ref is the fixture it was generated from; Family its factory's family (json, avro...), and
	// Schema its factory's schema relative to the resource root ("" for none).
	Ref, Family, Schema string
	// Identities are its identity values, in the order its factory declares their paths.
	Identities []string
	// Data is its values; Metadata what travels beside them (a key, headers, path variables).
	Data, Metadata *fixtures.Tree
}

// Identity is the record's first identity: the first path its factory declares.
func (r *Record) Identity() string {
	if len(r.Identities) == 0 {
		return ""
	}
	return r.Identities[0]
}

// CanonicalBytes is one record as bytes, the same for the same record in any process.
func CanonicalBytes(r *Record) []byte {
	b, _ := json.Marshal(struct {
		Key        int64           `json:"key"`
		Position   int             `json:"position"`
		Ref        string          `json:"ref"`
		Identities []string        `json:"identities"`
		Data       json.RawMessage `json:"data"`
		Metadata   json.RawMessage `json:"metadata"`
	}{r.Key, r.Position, r.Ref, r.Identities, r.Data.CompactJSON(), r.Metadata.CompactJSON()})
	return b
}

// pick is the shape a key takes. It hashes the key itself rather than drawing from a running
// sequence, so generating key ten thousand never needs the nine thousand before it.
func (s *Stream) pick(key int64) *shape {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d", s.def.Name, s.opts.Seed, s.opts.Run, key)))
	v := int64(binary.BigEndian.Uint64(sum[:8]))
	roll := v % int64(s.total)
	if roll < 0 {
		roll += int64(s.total) // floor modulo
	}
	cursor := int64(0)
	for i := range s.shapes {
		cursor += int64(s.shapes[i].entry.Weight)
		if roll < cursor {
			return &s.shapes[i]
		}
	}
	panic("streams: weighted pick out of range")
}

// Records is how many records the stream generates, about: its count times the mean length of
// its shapes' series, by weight. It is exact when every shape is as long, as most are.
func (s *Stream) Records() int64 {
	var sum float64
	for _, sh := range s.shapes {
		sum += float64(sh.entry.Weight) * float64(len(sh.messages))
	}
	if s.total == 0 {
		return 0
	}
	return int64(math.Round(float64(s.def.Count) * sum / float64(s.total)))
}

// All generates every record of the stream, in order.
func (s *Stream) All() iter.Seq2[*Record, error] { return s.Range(0, s.def.Count) }

// Range generates the records of the keys from from (inclusive) to to (exclusive), in order.
// Ranges are independent: several can be generated at once, anywhere, and together they are
// exactly the whole stream.
func (s *Stream) Range(from, to int64) iter.Seq2[*Record, error] {
	return func(yield func(*Record, error) bool) {
		for key := max(from, 0); key < min(to, s.def.Count); key++ {
			sh := s.pick(key)
			// Later messages of a key can reference what its earlier ones produced; when a key
			// repeats a document, the most recent is what a reference finds.
			generated := map[string]*fixtures.Tree{}
			for pos, f := range sh.messages {
				r, err := s.record(key, pos, f, sh.entry, generated)
				if err != nil {
					yield(nil, err)
					return
				}
				generated[f.Document()] = r.Data
				if !yield(r, nil) {
					return
				}
			}
		}
	}
}

func (s *Stream) record(key int64, pos int, f *fixtures.Fixture, e MixEntry, generated map[string]*fixtures.Tree) (*Record, error) {
	// The discriminator leaves the stream's name out, so streams generated for the same run
	// agree message for message; it has the position, so a key's messages get values of their
	// own unless one references another.
	scope := fixtures.InstanceScope{
		Discriminator: fmt.Sprintf("%s|%s|%d|%d", s.opts.Seed, s.opts.Run, key, pos),
		Attributes: map[string]string{
			"run.id": s.opts.Run, "run.key": fmt.Sprint(key), "run.position": fmt.Sprint(pos),
			"run.window": s.opts.Window.UTC().Format(time.RFC3339Nano),
		},
	}
	x, err := s.lib.Instance(f, fixtures.Input{
		Scope: scope, Overrides: e.Set,
		Documents: func(document string) (*fixtures.Tree, error) {
			t, ok := generated[document]
			if !ok {
				names := make([]string, 0, len(generated))
				for d := range generated {
					names = append(names, d)
				}
				slices.Sort(names)
				return nil, fmt.Errorf("stream %s key %d message %d refers to %s, which this key has not generated: a message refers "+
					"only to the ones before it, and this key has generated %v", s.def.Name, key, pos, document, names)
			}
			return t, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("stream %s key %d message %d: %w", s.def.Name, key, pos, err)
	}
	r := &Record{Key: key, Position: pos, Ref: f.Ref(), Family: f.Family(), Schema: f.SchemaRef(), Data: x.Data, Metadata: x.Metadata}
	for _, id := range f.Identities() {
		v, ok := x.Identities[id.Path]
		if !ok || v == "" {
			return nil, fmt.Errorf("stream %s key %d message %d has no identity at %s", s.def.Name, key, pos, id.Path)
		}
		r.Identities = append(r.Identities, v)
	}
	return r, nil
}

// Identities generates the whole stream once and hands its identities to sink a batch at a
// time, in key order, so a stream large enough to matter is never held whole. Keys are generated
// in chunks on every CPU at once. A record's identities always arrive together, so a batch can
// run a little past batch. It returns how many there were.
func (s *Stream) Identities(batch int, sink func([]string) error) (int, error) {
	if batch <= 0 {
		return 0, errors.New("a batch of identities has at least one")
	}
	const chunk = 4096 // keys a worker generates at a time
	type result struct {
		ids []string
		err error
	}
	workers := runtime.GOMAXPROCS(0)
	futures := make(chan chan result, workers*2) // in key order; the buffer bounds what is ahead
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(futures)
		sem := make(chan struct{}, workers)
		for from := int64(0); from < s.def.Count; from += chunk {
			f := make(chan result, 1)
			select {
			case futures <- f:
			case <-stop:
				return
			}
			select {
			case sem <- struct{}{}:
			case <-stop:
				return
			}
			go func(from, to int64) {
				defer func() { <-sem }()
				var r result
				for rec, err := range s.Range(from, to) {
					if err != nil {
						r.err = err
						break
					}
					r.ids = append(r.ids, rec.Identities...)
				}
				f <- r
			}(from, min(from+chunk, s.def.Count))
		}
	}()
	total := 0
	pending := make([]string, 0, batch+chunk)
	for f := range futures {
		r := <-f
		if r.err != nil {
			return total, r.err
		}
		pending = append(pending, r.ids...)
		for len(pending) >= batch {
			total += batch
			if err := sink(pending[:batch:batch]); err != nil {
				return total, err
			}
			pending = append(make([]string, 0, batch+chunk), pending[batch:]...)
		}
	}
	if len(pending) > 0 {
		total += len(pending)
		if err := sink(pending); err != nil {
			return total, err
		}
	}
	return total, nil
}

// CollisionError is a stream whose keys would claim the same identity.
type CollisionError struct {
	Stream     string
	Collisions []string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf("stream %s gives the same identity to more than one key: %s", e.Stream, strings.Join(e.Collisions, "; "))
}

// Lint fails a stream before any traffic when two keys would claim the same identity, naming
// the fixtures that generated it. Messages of one key sharing an identity is no collision: that
// is what makes them one key's series. It holds every identity while it runs.
func (s *Stream) Lint() error {
	type claim struct {
		key int64
		ref string
	}
	claimed := map[string]claim{}
	var collisions []string
	reported := map[string]bool{}
	for r, err := range s.All() {
		if err != nil {
			return err
		}
		for _, id := range r.Identities {
			c, ok := claimed[id]
			if !ok {
				claimed[id] = claim{r.Key, r.Ref}
				continue
			}
			if c.key != r.Key && !reported[id] {
				reported[id] = true
				collisions = append(collisions, fmt.Sprintf("%s (keys %d and %d, from %s and %s)", id, c.key, r.Key, c.ref, r.Ref))
			}
		}
	}
	if len(collisions) > 0 {
		return &CollisionError{Stream: s.def.Name, Collisions: collisions}
	}
	return nil
}
