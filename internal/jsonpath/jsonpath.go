// Package jsonpath reads values out of JSON documents by simple paths: $.order.id, items[0].sku,
// items[*].sku (the leading $. is optional). It is what the observers use to find the identities
// in what a scenario staged by hand; a path that matches several values gives them all.
package jsonpath

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Paths splits a comma-separated list of paths.
func Paths(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type step struct {
	key   string
	index int // -1: none; -2: every element
}

func parse(path string) ([]step, error) {
	p := strings.TrimPrefix(strings.TrimPrefix(path, "$"), ".")
	if p == "" {
		return nil, nil
	}
	var steps []step
	for _, part := range strings.Split(p, ".") {
		key, rest, _ := strings.Cut(part, "[")
		if key != "" {
			steps = append(steps, step{key: key, index: -1})
		}
		for rest != "" {
			idx, after, ok := strings.Cut(rest, "]")
			if !ok {
				return nil, fmt.Errorf("JSON path %q has an unclosed [", path)
			}
			if idx == "*" {
				steps = append(steps, step{index: -2})
			} else {
				n, err := strconv.Atoi(idx)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("JSON path %q has an index that is not a number or *: [%s]", path, idx)
				}
				steps = append(steps, step{index: n})
			}
			rest = strings.TrimPrefix(after, "[")
		}
	}
	return steps, nil
}

// Read is the scalar values at path in the JSON document doc, as text: none when there are none,
// or when doc is not JSON.
func Read(doc []byte, path string) ([]string, error) {
	steps, err := parse(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return nil, nil //nolint:nilerr // a document that is not JSON has no values at any path
	}
	nodes := []any{v}
	for _, s := range steps {
		var next []any
		for _, n := range nodes {
			switch {
			case s.key != "":
				if m, ok := n.(map[string]any); ok {
					if c, ok := m[s.key]; ok {
						next = append(next, c)
					}
				}
			case s.index == -2:
				if a, ok := n.([]any); ok {
					next = append(next, a...)
				}
			default:
				if a, ok := n.([]any); ok && s.index < len(a) {
					next = append(next, a[s.index])
				}
			}
		}
		nodes = next
	}
	var out []string
	for _, n := range nodes {
		switch x := n.(type) {
		case nil, map[string]any, []any:
		case string:
			out = append(out, x)
		case float64:
			out = append(out, strconv.FormatFloat(x, 'f', -1, 64))
		default:
			out = append(out, fmt.Sprint(x))
		}
	}
	return out, nil
}
