package transcript

import (
	"os"
	"strconv"
	"testing"
	"time"
)

// TestRealState reads the last day of a live state dir: HARNESS_TRANSCRIPT_STATE=~/.local/state/harness.
// HARNESS_TRANSCRIPT_DUMP=n writes digest n to stdout for a noise check.
func TestRealState(t *testing.T) {
	state := os.Getenv("HARNESS_TRANSCRIPT_STATE")
	if state == "" {
		t.Skip("HARNESS_TRANSCRIPT_STATE unset")
	}
	start := time.Now()
	sessions, err := Since(state, start.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wall := time.Since(start)

	count, turns := map[string]int{}, map[string]int{}
	bytes := 0
	for _, s := range sessions {
		count[s.Harness]++
		turns[s.Harness] += len(s.Turns)
		bytes += len(s.Digest(8000))
	}
	t.Logf("wall %v, sessions %v, turns %v, Digest(8000) total %d bytes", wall, count, turns, bytes)
	if n, err := strconv.Atoi(os.Getenv("HARNESS_TRANSCRIPT_DUMP")); err == nil && n < len(sessions) {
		os.Stdout.WriteString(sessions[n].Digest(8000))
	}
}
