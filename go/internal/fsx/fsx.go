// Package fsx holds file, YAML, JSON and TOML helpers shared by every harness.
package fsx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Write creates parent dirs and replaces path atomically via a sibling .tmp file.
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// FreshDir removes whatever is at path (file, symlink or tree) and recreates it as an empty dir.
func FreshDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o755)
}

// Link makes dst a symlink to src, creating src; a correct existing link is left alone.
func Link(dst, src string) error {
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 && sameFile(dst, src) {
			return nil
		}
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(src, dst)
}

func sameFile(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// ReadYAML parses a YAML mapping; a missing file or empty document is an empty map.
func ReadYAML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	if fi, _ := os.Stat(path); fi != nil && fi.IsDir() {
		return map[string]any{}, nil
	}
	m, err := ParseYAML(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// ParseYAML parses a YAML mapping document, keeping key order (see Keys).
func ParseYAML(data []byte) (map[string]any, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return map[string]any{}, nil
	}
	v, err := fromNode(doc.Content[0])
	if err != nil {
		return nil, err
	}
	switch m := v.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return m, nil
	}
	return nil, errors.New("top level is not a mapping")
}

func fromNode(n *yaml.Node) (any, error) {
	switch n.Kind {
	case yaml.AliasNode:
		return fromNode(n.Alias)
	case yaml.MappingNode:
		m := map[string]any{}
		var keys []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			val, err := fromNode(v)
			if err != nil {
				return nil, err
			}
			if _, dup := m[k.Value]; !dup {
				keys = append(keys, k.Value)
			}
			m[k.Value] = val
		}
		setOrder(m, keys)
		return m, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := fromNode(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	}
	var v any
	err := n.Decode(&v)
	return v, err
}

// ReadJSON parses a JSON object, keeping key order; a missing file is an empty map.
func ReadJSON(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m, err := ParseJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// ParseJSON parses one JSON object, keeping key order (see Keys) and numbers as written.
func ParseJSON(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := jsonValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after JSON object")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("top level is not an object")
	}
	return m, nil
}

func jsonValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch tok {
	case json.Delim('{'):
		m := map[string]any{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := jsonValue(dec)
			if err != nil {
				return nil, err
			}
			Put(m, k.(string), v)
		}
		_, err := dec.Token()
		return m, err
	case json.Delim('['):
		out := []any{}
		for dec.More() {
			v, err := jsonValue(dec)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		_, err := dec.Token()
		return out, err
	}
	return tok, nil
}

// DumpYAML is PyYAML safe_dump(sort_keys=False, width=1000): map keys in insertion order.
func DumpYAML(v any) []byte {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(toNode(v)); err != nil {
		panic(err)
	}
	enc.Close()
	return buf.Bytes()
}

func toNode(v any) *yaml.Node {
	switch x := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range Keys(x) {
			n.Content = append(n.Content, toNode(k), toNode(x[k]))
		}
		return n
	case map[string]string:
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, k := range sortedKeys(x) {
			n.Content = append(n.Content, toNode(k), toNode(x[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range x {
			n.Content = append(n.Content, toNode(e))
		}
		return n
	case []string:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range x {
			n.Content = append(n.Content, toNode(e))
		}
		return n
	case []map[string]any:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		for _, e := range x {
			n.Content = append(n.Content, toNode(e))
		}
		return n
	}
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		panic(err)
	}
	return n
}

// Merge deep-merges b over a into a new map; b wins, a's key order first.
func Merge(a, b map[string]any) map[string]any {
	out := Copy(a)
	for _, k := range Keys(b) {
		bv := b[k]
		bm, bIsMap := bv.(map[string]any)
		am, aIsMap := out[k].(map[string]any)
		if bIsMap && aIsMap {
			Put(out, k, Merge(am, bm))
		} else {
			Put(out, k, bv)
		}
	}
	return out
}

// Copy is a shallow copy that keeps key order.
func Copy(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	keys := Keys(m)
	for _, k := range keys {
		out[k] = m[k]
	}
	setOrder(out, keys)
	return out
}

var frontmatter = regexp.MustCompile(`(?s)\A---\n(.*?)\n---\n?`)

// SplitFrontmatter splits a leading `---` YAML block from the body; no block means an empty map.
func SplitFrontmatter(text string) (map[string]any, string) {
	m := frontmatter.FindStringSubmatchIndex(text)
	if m == nil {
		return map[string]any{}, text
	}
	fm, err := ParseYAML([]byte(text[m[2]:m[3]]))
	if err != nil {
		fm = map[string]any{}
	}
	return fm, text[m[1]:]
}
