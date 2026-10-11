package fsx

import (
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DumpJSON is Python json.dumps(v, indent=2) + "\n": insertion-ordered keys, ASCII-escaped.
func DumpJSON(v any) []byte {
	var b strings.Builder
	writeJSON(&b, v, "")
	b.WriteString("\n")
	return []byte(b.String())
}

func writeJSON(b *strings.Builder, v any, indent string) {
	inner := indent + "  "
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case string:
		b.WriteString(PyQuote(x, true))
	case map[string]any:
		keys := Keys(x)
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range keys {
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(inner + PyQuote(k, true) + ": ")
			writeJSON(b, x[k], inner)
		}
		b.WriteString("\n" + indent + "}")
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Slice, reflect.Array:
			if rv.Len() == 0 {
				b.WriteString("[]")
				return
			}
			b.WriteString("[\n")
			for i := 0; i < rv.Len(); i++ {
				if i > 0 {
					b.WriteString(",\n")
				}
				b.WriteString(inner)
				writeJSON(b, rv.Index(i).Interface(), inner)
			}
			b.WriteString("\n" + indent + "]")
		case reflect.Map:
			m := map[string]any{}
			for _, k := range rv.MapKeys() {
				m[fmt.Sprint(k.Interface())] = rv.MapIndex(k).Interface()
			}
			writeJSON(b, m, indent)
		default:
			b.WriteString(pyNum(v))
		}
	}
}

// DumpTOML matches the launcher's minimal TOML writer: scalars first, then [tables] in order.
func DumpTOML(m map[string]any) []byte { return []byte(dumpTOML(m, "")) }

func dumpTOML(m map[string]any, prefix string) string {
	var lines strings.Builder
	type table struct {
		name string
		v    map[string]any
	}
	var tables []table
	for _, k := range Keys(m) {
		if sub, ok := m[k].(map[string]any); ok {
			tables = append(tables, table{prefix + tomlKey(k), sub})
		} else {
			lines.WriteString(tomlKey(k) + " = " + tomlVal(m[k]) + "\n")
		}
	}
	out := lines.String()
	for _, t := range tables {
		out += "\n[" + t.name + "]\n" + dumpTOML(t.v, t.name+".")
	}
	return out
}

var bareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func tomlKey(k string) string {
	if bareKey.MatchString(k) {
		return k
	}
	return PyQuote(k, true)
}

func tomlVal(v any) string {
	switch x := v.(type) {
	case bool:
		return strconv.FormatBool(x)
	case string:
		return PyQuote(x, false)
	case nil:
		return PyQuote("None", false)
	case map[string]any:
		parts := make([]string, 0, len(x))
		for _, k := range Keys(x) {
			parts = append(parts, tomlKey(k)+" = "+tomlVal(x[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array:
		parts := make([]string, rv.Len())
		for i := range parts {
			parts[i] = tomlVal(rv.Index(i).Interface())
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8,
		reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return pyNum(v)
	}
	return PyQuote(fmt.Sprint(v), false)
}

func pyNum(v any) string {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		switch {
		case math.IsInf(f, 1):
			return "Infinity"
		case math.IsInf(f, -1):
			return "-Infinity"
		case math.IsNaN(f):
			return "NaN"
		case f == math.Trunc(f) && math.Abs(f) < 1e16:
			return strconv.FormatFloat(f, 'f', 1, 64)
		}
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return fmt.Sprint(v)
}

// PyQuote is Python json.dumps of a string; ascii mirrors ensure_ascii.
func PyQuote(s string, ascii bool) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || (ascii && r == 0x7f):
				fmt.Fprintf(&b, `\u%04x`, r)
			case ascii && r > 0x7f:
				if r == utf8.RuneError {
					b.WriteString(`�`)
				} else if r > 0xffff {
					r -= 0x10000
					fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
				} else {
					fmt.Fprintf(&b, `\u%04x`, r)
				}
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}
