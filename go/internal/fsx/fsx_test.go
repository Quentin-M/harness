package fsx

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestWriteCreatesParentsAndReplaces(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a/b/c.txt")
	for _, s := range []string{"one", "two"} {
		if err := Write(p, []byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(p); string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("tmp file left behind")
	}
}

func TestFreshDirReplacesFileSymlinkAndTree(t *testing.T) {
	d := t.TempDir()
	tree := filepath.Join(d, "tree")
	Write(filepath.Join(tree, "x/y"), []byte("z"))
	file := filepath.Join(d, "file")
	os.WriteFile(file, []byte("z"), 0o644)
	target := filepath.Join(d, "target")
	Write(filepath.Join(target, "keep"), []byte("z"))
	link := filepath.Join(d, "link")
	os.Symlink(target, link)
	for _, p := range []string{tree, file, link, filepath.Join(d, "new")} {
		if err := FreshDir(p); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() {
			t.Fatalf("%s not a dir", p)
		}
		if ents, _ := os.ReadDir(p); len(ents) != 0 {
			t.Fatalf("%s not empty", p)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatal("FreshDir on a symlink followed it")
	}
}

func TestLink(t *testing.T) {
	d := t.TempDir()
	src, dst := filepath.Join(d, "src"), filepath.Join(d, "deep/dst")
	for i := 0; i < 2; i++ {
		if err := Link(dst, src); err != nil {
			t.Fatal(err)
		}
	}
	if r, _ := os.Readlink(dst); r != src {
		t.Fatalf("readlink %q", r)
	}
	os.Remove(dst)
	Write(filepath.Join(dst, "f"), []byte("x"))
	if err := Link(dst, src); err != nil {
		t.Fatal(err)
	}
	if r, _ := os.Readlink(dst); r != src {
		t.Fatal("existing dir not replaced by link")
	}
}

func TestReadYAMLMissingAndEmpty(t *testing.T) {
	d := t.TempDir()
	m, err := ReadYAML(filepath.Join(d, "nope.yaml"))
	if err != nil || len(m) != 0 {
		t.Fatal(m, err)
	}
	os.WriteFile(filepath.Join(d, "e.yaml"), []byte("# only a comment\n"), 0o644)
	if m, err := ReadYAML(filepath.Join(d, "e.yaml")); err != nil || len(m) != 0 {
		t.Fatal(m, err)
	}
}

func TestYAMLKeepsKeyOrderRoundTrip(t *testing.T) {
	src := "zeta: 1\nalpha:\n  m: true\n  b: [x, y]\nmid: null\n"
	m, err := ParseYAML([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := Keys(m); !reflect.DeepEqual(got, []string{"zeta", "alpha", "mid"}) {
		t.Fatal(got)
	}
	if got := Keys(m["alpha"].(map[string]any)); !reflect.DeepEqual(got, []string{"m", "b"}) {
		t.Fatal(got)
	}
	out := string(DumpYAML(m))
	if strings.Index(out, "zeta") > strings.Index(out, "alpha") || strings.Index(out, "m:") > strings.Index(out, "b:") {
		t.Fatal(out)
	}
	back, _ := ParseYAML([]byte(out))
	if !reflect.DeepEqual(back, m) {
		t.Fatalf("%v != %v", back, m)
	}
}

func TestPutAndOMapOrder(t *testing.T) {
	m := OMap("b", 1, "a", 2)
	Put(m, "c", 3)
	Put(m, "b", 4)
	if got := Keys(m); !reflect.DeepEqual(got, []string{"b", "a", "c"}) {
		t.Fatal(got)
	}
	m["plain"] = 5
	if got := Keys(m); got[len(got)-1] != "plain" {
		t.Fatal(got)
	}
}

func TestMergeDeepBWins(t *testing.T) {
	a := OMap("x", OMap("k", 1, "j", 2), "y", "a", "z", OMap("q", 1))
	b := OMap("x", OMap("j", 3, "n", 4), "y", OMap("deep", true), "w", 1, "z", "flat")
	got := Merge(a, b)
	want := map[string]any{"x": map[string]any{"k": 1, "j": 3, "n": 4}, "y": map[string]any{"deep": true},
		"z": "flat", "w": 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if k := Keys(got); !reflect.DeepEqual(k, []string{"x", "y", "z", "w"}) {
		t.Fatal(k)
	}
	if k := Keys(got["x"].(map[string]any)); !reflect.DeepEqual(k, []string{"k", "j", "n"}) {
		t.Fatal(k)
	}
	if a["x"].(map[string]any)["j"] != 2 {
		t.Fatal("Merge mutated a")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	fm, body := SplitFrontmatter("---\nname: x\npaths:\n- a\n---\nBODY\n")
	if fm["name"] != "x" || body != "BODY\n" {
		t.Fatal(fm, body)
	}
	if fm, body := SplitFrontmatter("---\na: 1\n---"); fm["a"] != 1 || body != "" {
		t.Fatal(fm, body)
	}
	if fm, body := SplitFrontmatter("no fm\n---\na: 1\n---\n"); len(fm) != 0 || body != "no fm\n---\na: 1\n---\n" {
		t.Fatal(fm, body)
	}
	if fm, body := SplitFrontmatter("---\n\n---\nB"); len(fm) != 0 || body != "B" {
		t.Fatal(fm, body)
	}
}

func TestDumpJSONMatchesPython(t *testing.T) {
	v := OMap("model", "big[1m]", "on", true, "n", 30, "list", []any{"a", OMap("k", "v")}, "empty", map[string]any{},
		"uni", "é\"\n")
	want := "{\n  \"model\": \"big[1m]\",\n  \"on\": true,\n  \"n\": 30,\n  \"list\": [\n    \"a\",\n    {\n" +
		"      \"k\": \"v\"\n    }\n  ],\n  \"empty\": {},\n  \"uni\": \"\\u00e9\\\"\\n\"\n}\n"
	if got := string(DumpJSON(v)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestDumpTOMLMatchesPythonAndParses(t *testing.T) {
	v := OMap("model", "rev", "features", OMap("hooks", true), "n", 86400000,
		"hooks", OMap("state", OMap("x:session_start:0:0", OMap("trusted_hash", "sha256:abc"))),
		"arr", []any{OMap("type", "command", "async", true)})
	got := string(DumpTOML(v))
	want := "model = \"rev\"\nn = 86400000\narr = [{type = \"command\", async = true}]\n\n[features]\nhooks = true\n\n" +
		"[hooks]\n\n[hooks.state]\n\n[hooks.state.\"x:session_start:0:0\"]\ntrusted_hash = \"sha256:abc\"\n"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	var back map[string]any
	if _, err := toml.Decode(got, &back); err != nil {
		t.Fatal(err)
	}
}
