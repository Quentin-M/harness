// Package lease caps concurrent use of a shared resource across rig seats without a coordinator.
//
// Each resource is one JSON file guarded by an flock, so every acquire, release and expiry is a
// serialized read-modify-write. Seats call this from short-lived shells, so a lease lives until
// released or TTL expiry, never tied to a process.
package lease

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"syscall"
	"time"
)

var ErrNotHeld = errors.New("no lease held")

var name = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)

type Store struct {
	Dir string
	TTL time.Duration
	Now func() time.Time
}

type Lease struct {
	Holder string    `json:"holder"`
	Since  time.Time `json:"since"`
}

// Acquire takes a lease on res for holder. It reports false when cap leases are live. A holder
// that already holds one renews it.
func (s *Store) Acquire(res string, cap int, holder string) (bool, error) {
	if err := check(res, holder); err != nil {
		return false, err
	}
	if cap < 1 {
		return false, fmt.Errorf("lease %s: cap %d < 1", res, cap)
	}
	ok := false
	err := s.update(res, func(ls []Lease) []Lease {
		if i := slices.IndexFunc(ls, func(l Lease) bool { return l.Holder == holder }); i >= 0 {
			ok = true
			ls[i].Since = s.Now().UTC()
			return ls
		}
		if len(ls) >= cap {
			return ls
		}
		ok = true
		return append(ls, Lease{Holder: holder, Since: s.Now().UTC()})
	})
	return ok, err
}

// Release frees holder's lease on res.
func (s *Store) Release(res, holder string) error {
	if err := check(res, holder); err != nil {
		return err
	}
	found := false
	err := s.update(res, func(ls []Lease) []Lease {
		return slices.DeleteFunc(ls, func(l Lease) bool {
			found = found || l.Holder == holder
			return l.Holder == holder
		})
	})
	if err == nil && !found {
		err = fmt.Errorf("lease %s for %s: %w", res, holder, ErrNotHeld)
	}
	return err
}

// List returns the live leases on res, oldest first.
func (s *Store) List(res string) ([]Lease, error) {
	if !name.MatchString(res) {
		return nil, fmt.Errorf("lease: bad resource name %q", res)
	}
	var out []Lease
	err := s.update(res, func(ls []Lease) []Lease { out = ls; return ls })
	return out, err
}

// update runs fn on the live leases of res under an exclusive flock and writes the result back.
func (s *Store) update(res string, fn func([]Lease) []Lease) error {
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	path := filepath.Join(s.Dir, res+".json")
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lease %s: lock: %w", res, err)
	}
	var ls []Lease
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &ls); err != nil {
			return fmt.Errorf("lease %s: corrupt %s: %w", res, path, err)
		}
	}
	now := s.Now()
	ls = slices.DeleteFunc(ls, func(l Lease) bool { return now.Sub(l.Since) > s.TTL })
	ls = fn(ls)
	if b, err = json.Marshal(ls); err != nil {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("lease %s: %w", res, err)
	}
	return nil
}

func check(res, holder string) error {
	if !name.MatchString(res) {
		return fmt.Errorf("lease: bad resource name %q", res)
	}
	if !name.MatchString(holder) {
		return fmt.Errorf("lease: bad holder name %q", holder)
	}
	return nil
}
