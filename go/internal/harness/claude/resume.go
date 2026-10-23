package claude

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Quentin-M/harness/internal/harness"
)

// importBare makes `--resume <id>` find a session bare claude wrote to ~/.claude: herdr restores
// and launches outside the shell alias run claude without CLAUDE_CONFIG_DIR. The transcript, its
// sidecar dir and its file history are hard-linked, so both stores append to one transcript.
func importBare(ctx *harness.Ctx, args []string) error {
	id := resumeID(args)
	store := filepath.Join(ctx.State, "claude")
	if id == "" || len(transcripts(store, id)) > 0 {
		return nil
	}
	bare := filepath.Join(ctx.Sys.Home, ".claude")
	found := transcripts(bare, id)
	if len(found) != 1 {
		return nil
	}
	project := filepath.Base(filepath.Dir(found[0]))
	for _, rel := range []string{
		filepath.Join("projects", project, id),
		filepath.Join("file-history", id),
		filepath.Join("projects", project, id+".jsonl"), // last: claude sees the session only once it is whole
	} {
		if err := linkTree(filepath.Join(bare, rel), filepath.Join(store, rel)); err != nil {
			return fmt.Errorf("claude: resume %s from %s: %w", id, bare, err)
		}
	}
	return nil
}

// sessionID is a claude session id; anything else is claude's to resolve.
var sessionID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// resumeID is the session id given to --resume or -r, or "" when there is none.
func resumeID(args []string) string {
	for i, a := range args {
		id := ""
		switch {
		case (a == "--resume" || a == "-r") && i+1 < len(args):
			id = args[i+1]
		case strings.HasPrefix(a, "--resume="):
			id = strings.TrimPrefix(a, "--resume=")
		default:
			continue
		}
		if sessionID.MatchString(id) {
			return id
		}
		return ""
	}
	return ""
}

// transcripts are the session files for id across a config dir's projects.
func transcripts(config, id string) []string {
	m, _ := filepath.Glob(filepath.Join(config, "projects", "*", id+".jsonl"))
	return m
}

var link = os.Link

// linkTree mirrors the regular files under src into dst, skipping names dst already has.
// A missing src is nothing to import.
func linkTree(src, dst string) error {
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if _, err := os.Lstat(to); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := link(path, to); err == nil || errors.Is(err, os.ErrExist) {
			return nil
		}
		return copyFile(path, to)
	})
}

// copyFile publishes from at to through a private temp file, so claude never reads half a copy.
func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(to), ".import-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), to)
}
