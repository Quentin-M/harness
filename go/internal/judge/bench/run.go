package bench

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Result is what came back for one case: a reply, or the reason there was none.
type Result struct {
	Reply  *Reply
	Err    string // set when Reply is nil
	Cached bool
}

// Counts summarises one run's traffic.
type Counts struct {
	Requests    int // HTTP requests made
	CacheHits   int // distinct requests answered from disk
	Unavailable int // cases with no reply
}

// Runner asks a judge about every case, deduplicating identical states and consulting the cache.
type Runner struct {
	Client      *Client
	Cache       *Cache // nil disables reads and writes
	NoCache     bool   // ignore cached replies (fresh replies are still stored)
	Concurrency int
	Log         io.Writer // progress lines; nil is silent
}

// Run returns one Result per case, in order. It fails only when ctx ends; per-case failures are
// recorded in the Result and score as unavailable (blocked).
func (r *Runner) Run(ctx context.Context, b *Battery, cases []Case) ([]Result, Counts, error) {
	type job struct {
		key   string
		state State
		idx   []int
	}
	var jobs []*job
	byKey := map[string]*job{}
	for i, c := range cases {
		st := StateOf(c, b.Notice)
		key := CacheKey(r.Client.Model, b, st)
		j := byKey[key]
		if j == nil {
			j = &job{key: key, state: st}
			byKey[key], jobs = j, append(jobs, j)
		}
		j.idx = append(j.idx, i)
	}

	results := make([]Result, len(cases))
	var counts Counts
	var mu sync.Mutex
	var done atomic.Int64
	questions := b.QuestionsJSON()
	conc := max(r.Concurrency, 1)
	work := make(chan *job)
	var wg sync.WaitGroup
	for range conc {
		wg.Go(func() {
			for j := range work {
				var res Result
				var requested bool
				if r.Cache != nil && !r.NoCache {
					if rep, ok := r.Cache.Get(j.key); ok {
						res = Result{Reply: rep, Cached: true}
					}
				}
				if res.Reply == nil {
					requested = true
					rep, err := r.Client.Ask(ctx, j.state, questions)
					if err != nil {
						res.Err = err.Error()
					} else {
						res.Reply = rep
						if r.Cache != nil {
							if err := r.Cache.Put(j.key, rep); err != nil && r.Log != nil {
								fmt.Fprintf(r.Log, "bench: cache write: %v\n", err)
							}
						}
					}
				}
				mu.Lock()
				for _, i := range j.idx {
					results[i] = res
				}
				switch {
				case requested:
					counts.Requests++
				default:
					counts.CacheHits++
				}
				if res.Reply == nil {
					counts.Unavailable += len(j.idx)
				}
				mu.Unlock()
				if n := done.Add(1); r.Log != nil && (n%100 == 0 || int(n) == len(jobs)) {
					fmt.Fprintf(r.Log, "bench: %d/%d requests\n", n, len(jobs))
				}
			}
		})
	}
feed:
	for _, j := range jobs {
		select {
		case work <- j:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, counts, err
	}
	return results, counts, nil
}
