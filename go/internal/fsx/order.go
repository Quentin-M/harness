package fsx

import (
	"reflect"
	"sort"
	"sync"
)

// Go maps have no order, but generated files must follow Python's dict insertion order. A map's
// order lives in a registry keyed by its address; registered maps are kept alive so an address
// is never reused for another map.
var reg = struct {
	sync.Mutex
	order map[uintptr][]string
	keep  []map[string]any
}{order: map[uintptr][]string{}}

func addr(m map[string]any) uintptr { return reflect.ValueOf(m).Pointer() }

func setOrder(m map[string]any, keys []string) {
	if m == nil {
		return
	}
	reg.Lock()
	defer reg.Unlock()
	p := addr(m)
	if _, ok := reg.order[p]; !ok {
		reg.keep = append(reg.keep, m)
	}
	reg.order[p] = append([]string(nil), keys...)
}

// Keys returns m's keys in insertion order (YAML order for parsed maps, Put order otherwise).
// Keys added by plain assignment come last, sorted.
func Keys(m map[string]any) []string {
	if m == nil {
		return nil
	}
	reg.Lock()
	known := reg.order[addr(m)]
	reg.Unlock()
	out := make([]string, 0, len(m))
	seen := make(map[string]bool, len(m))
	for _, k := range known {
		if _, ok := m[k]; ok && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	var rest []string
	for k := range m {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// Put sets m[k] = v, appending k to m's order when it is new (Python dict assignment).
func Put(m map[string]any, k string, v any) {
	if _, ok := m[k]; !ok {
		setOrder(m, append(Keys(m), k))
	}
	m[k] = v
}

// OMap builds a map from alternating key, value pairs, keeping that key order.
func OMap(kv ...any) map[string]any {
	if len(kv)%2 != 0 {
		panic("fsx.OMap: odd argument count")
	}
	m := make(map[string]any, len(kv)/2)
	keys := make([]string, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		k := kv[i].(string)
		if _, ok := m[k]; !ok {
			keys = append(keys, k)
		}
		m[k] = kv[i+1]
	}
	setOrder(m, keys)
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
