package jsonpath

import (
	"slices"
	"testing"
)

func TestRead(t *testing.T) {
	doc := []byte(`{"parcel":{"id":"p-1","weight":12.5,"insured":true},"pieces":[{"id":"a"},{"id":"b"},{"n":1}],"empty":null}`)
	for path, want := range map[string][]string{
		"$.parcel.id":    {"p-1"},
		"parcel.id":      {"p-1"},
		"parcel.weight":  {"12.5"},
		"parcel.insured": {"true"},
		"pieces[1].id":   {"b"},
		"pieces[*].id":   {"a", "b"},
		"pieces[9].id":   nil,
		"parcel":         nil, // not a scalar
		"empty":          nil,
		"nothing.here":   nil,
	} {
		got, err := Read(doc, path)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Read(%s) = %v, %v; want %v", path, got, err, want)
		}
	}
	if got, _ := Read([]byte("not json"), "a"); got != nil {
		t.Errorf("not JSON gave %v", got)
	}
	for _, bad := range []string{"pieces[x]", "pieces[0"} {
		if _, err := Read(doc, bad); err == nil {
			t.Errorf("Read(%s) accepted it", bad)
		}
	}
	if p := Paths(" a.b, ,c "); !slices.Equal(p, []string{"a.b", "c"}) {
		t.Errorf("Paths = %v", p)
	}
}
