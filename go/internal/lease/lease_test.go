package lease

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func store(t *testing.T) (*Store, *clock) {
	c := &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)}
	return &Store{Dir: t.TempDir(), TTL: 4 * time.Hour, Now: c.now}, c
}

func holders(t *testing.T, s *Store, res string) []string {
	ls, err := s.List(res)
	if err != nil {
		t.Fatal(err)
	}
	var hs []string
	for _, l := range ls {
		hs = append(hs, l.Holder)
	}
	return hs
}

func TestConcurrentAcquiresNeverExceedCap(t *testing.T) {
	s, _ := store(t)
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.Acquire("box", 3, fmt.Sprintf("h%d", i))
			if err != nil {
				t.Error(err)
			}
			if ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 3 {
		t.Fatalf("cap 3, %d won", won.Load())
	}
	if hs := holders(t, s, "box"); len(hs) != 3 {
		t.Fatalf("list = %v, want 3 holders", hs)
	}
}

func TestFullRefusesAndReleaseFreesOneSlot(t *testing.T) {
	s, _ := store(t)
	for _, h := range []string{"a", "b"} {
		if ok, _ := s.Acquire("box", 2, h); !ok {
			t.Fatalf("%s should acquire", h)
		}
	}
	if ok, _ := s.Acquire("box", 2, "late"); ok {
		t.Fatal("late acquired a full resource")
	}
	if err := s.Release("box", "a"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Acquire("box", 2, "late"); !ok {
		t.Fatal("late should acquire after a release")
	}
	if hs := holders(t, s, "box"); len(hs) != 2 || hs[0] != "b" || hs[1] != "late" {
		t.Fatalf("holders = %v, want [b late]", hs)
	}
}

func TestReacquireByHolderTakesNoExtraSlot(t *testing.T) {
	s, _ := store(t)
	s.Acquire("box", 3, "a")
	if ok, _ := s.Acquire("box", 3, "a"); !ok {
		t.Fatal("re-acquire by holder should succeed")
	}
	if hs := holders(t, s, "box"); len(hs) != 1 || hs[0] != "a" {
		t.Fatalf("holders = %v, want [a]", hs)
	}
}

func TestReleaseUnheldIsErrNotHeld(t *testing.T) {
	s, _ := store(t)
	if err := s.Release("box", "nobody"); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("err = %v, want ErrNotHeld", err)
	}
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	s, c := store(t)
	s.Acquire("box", 1, "old")
	if ok, _ := s.Acquire("box", 1, "new"); ok {
		t.Fatal("fresh lease reclaimed")
	}
	c.t = c.t.Add(4*time.Hour + time.Second)
	if ok, _ := s.Acquire("box", 1, "new"); !ok {
		t.Fatal("expired lease not reclaimed")
	}
	if hs := holders(t, s, "box"); len(hs) != 1 || hs[0] != "new" {
		t.Fatalf("holders = %v, want [new]", hs)
	}
}

func TestResourcesAreIndependent(t *testing.T) {
	s, _ := store(t)
	s.Acquire("box", 1, "a")
	if ok, _ := s.Acquire("gpu", 1, "b"); !ok {
		t.Fatal("gpu should be free")
	}
}

func TestListReportsSince(t *testing.T) {
	s, c := store(t)
	s.Acquire("box", 1, "a")
	ls, _ := s.List("box")
	if len(ls) != 1 || !ls[0].Since.Equal(c.t) {
		t.Fatalf("list = %+v, want since %v", ls, c.t)
	}
}

func TestRejectsBadInput(t *testing.T) {
	s, _ := store(t)
	for _, tc := range []struct {
		res, holder string
		cap         int
	}{{"../x", "a", 1}, {"", "a", 1}, {"box", "", 1}, {"box", "a", 0}} {
		if _, err := s.Acquire(tc.res, tc.cap, tc.holder); err == nil {
			t.Errorf("Acquire(%q, %d, %q) accepted", tc.res, tc.cap, tc.holder)
		}
	}
}

func TestConcurrentSameHolderTakesOneLease(t *testing.T) {
	s, _ := store(t)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); s.Acquire("box", 5, "a") }()
	}
	wg.Wait()
	if hs := holders(t, s, "box"); len(hs) != 1 {
		t.Fatalf("holders = %v, want one lease for a", hs)
	}
}

func TestConcurrentReleaseAndAcquireKeepCap(t *testing.T) {
	s, c := store(t)
	for i := range 3 {
		s.Acquire("box", 3, fmt.Sprintf("old%d", i))
	}
	c.t = c.t.Add(5 * time.Hour) // all three expire; 30 newcomers race for the freed slots
	var won atomic.Int32
	var wg sync.WaitGroup
	for i := range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := s.Acquire("box", 3, fmt.Sprintf("new%d", i)); ok {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	if won.Load() != 3 || len(holders(t, s, "box")) != 3 {
		t.Fatalf("won %d, holders %v; want 3", won.Load(), holders(t, s, "box"))
	}
}

func TestCorruptStateIsAnErrorNotAnEmptyResource(t *testing.T) {
	s, _ := store(t)
	os.WriteFile(filepath.Join(s.Dir, "box.json"), []byte("{nope"), 0o644)
	if _, err := s.Acquire("box", 1, "a"); err == nil {
		t.Fatal("corrupt state accepted")
	}
}

func TestReacquireRenewsTheLease(t *testing.T) {
	s, c := store(t)
	s.Acquire("box", 1, "a")
	c.t = c.t.Add(3 * time.Hour)
	s.Acquire("box", 1, "a")
	c.t = c.t.Add(3 * time.Hour) // 6h after the first acquire, 3h after the renewal
	if ok, _ := s.Acquire("box", 1, "b"); ok {
		t.Fatal("renewed lease expired at the original TTL")
	}
}
