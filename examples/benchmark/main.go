// Command benchmark measures the performance of recoant on the large data
// set of recoant-data (https://github.com/breskos/recoant-data): ingestion,
// memory, training, incremental refresh and request latency of every
// model.
//
//	go run ./examples/benchmark -data ../recoant-data/synthetic
//	go run ./examples/benchmark -scale   # memory of the store at 40 million interactions
//
// The data set benchmark/large has 3.5 million signals of 200,000 users on
// 50,000 items. All but the last -refresh signals are ingested and every
// model is trained on them; the last signals then arrive as new data and
// the models with incremental updates refresh. Latency is measured for
// -requests users with a history, top 10, excluding seen items: one at a
// time (p50, p95, p99) and from all CPUs at once (throughput).
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/algo/bpr"
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/algo/svd"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

var rel = store.Relation{Subject: recoant.User, Object: "item"}

type model interface {
	recoant.Recommender
}

type spec struct {
	name   string
	params string
	make   func(s *store.Store, base []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error)
}

var specs = []spec{
	{"popularity", "all-time", func(s *store.Store, base []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := popularity.New(signal.DefaultModel(), popularity.Config{})
		if err != nil {
			return nil, nil, nil, err
		}
		m.SetSeen(s)
		return m, func(ctx context.Context) error { return m.Fit(ctx, base) }, nil, nil
	}},
	{"item-knn", "cosine, 50 neighbors, shrink 10", func(s *store.Store, _ []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := knn.NewItemKNN(s, knn.Config{Relation: rel, Shrink: 10})
		if err != nil {
			return nil, nil, nil, err
		}
		return m, m.Fit, m.Refresh, nil
	}},
	{"user-knn", "cosine, 50 neighbors, shrink 10, max degree 1000", func(s *store.Store, _ []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := knn.NewUserKNN(s, knn.Config{Relation: rel, Shrink: 10, MaxDegree: 1000})
		if err != nil {
			return nil, nil, nil, err
		}
		return m, m.Fit, m.Refresh, nil
	}},
	{"pure-svd", "64 factors", func(s *store.Store, _ []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := svd.New(s, svd.Config{Relation: rel, Factors: 64, Seed: 1})
		if err != nil {
			return nil, nil, nil, err
		}
		return m, m.Fit, nil, nil
	}},
	{"als", "32 factors, 10 iterations", func(s *store.Store, _ []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := als.New(s, als.Config{Relation: rel, Factors: 32, Iterations: 10, Seed: 1})
		if err != nil {
			return nil, nil, nil, err
		}
		return m, m.Fit, m.Refresh, nil
	}},
	{"bpr", "32 factors, 30 epochs", func(s *store.Store, _ []recoant.Signal) (model, func(context.Context) error, func(context.Context) error, error) {
		m, err := bpr.New(s, bpr.Config{Relation: rel, Factors: 32, Epochs: 30, Seed: 1})
		if err != nil {
			return nil, nil, nil, err
		}
		return m, m.Fit, nil, nil
	}},
}

func main() {
	data := flag.String("data", "../recoant-data/synthetic", "folder of the recoant-data synthetic module")
	requests := flag.Int("requests", 2000, "number of measured requests per model")
	refresh := flag.Int("refresh", 2000, "number of new signals before the refresh")
	scale := flag.Bool("scale", false, "measure the memory of the store at 40 million interactions instead")
	only := flag.String("only", "", "measure only this model")
	flag.Parse()
	fmt.Printf("%s, %d CPUs, %s/%s, %s\n\n", cpuName(), runtime.NumCPU(), runtime.GOOS, runtime.GOARCH, runtime.Version())
	if *scale {
		scaleStore()
		return
	}
	ctx := context.Background()
	start := time.Now()
	sigs, err := load(filepath.Join(*data, "benchmark/large/signals.csv.gz"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("loaded %d signals in %v (CSV parsing, not part of recoant)\n", len(sigs), since(start))
	base, tail := sigs[:len(sigs)-*refresh], sigs[len(sigs)-*refresh:]

	// Ingestion and store memory.
	before := heap()
	start = time.Now()
	s := newStore(ctx, base)
	took := time.Since(start)
	storeHeap := heap() - before
	st := s.Stats()
	fmt.Printf("ingest: %d signals in %v = %.2f million signals/s; %d users, %d items, %d interactions; store heap %.0f MB (%.0f bytes per interaction)\n\n",
		len(base), took.Round(time.Millisecond), float64(len(base))/took.Seconds()/1e6, s.Len(recoant.User), s.Len("item"), st.Interactions,
		float64(storeHeap)/1e6, float64(storeHeap)/float64(st.Interactions))

	users := sample(s, *requests)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintf(w, "model\tparameters\tfit\tmodel heap\trefresh (%d new)\tp50\tp95\tp99\tthroughput (%d CPUs)\t\n", *refresh, runtime.NumCPU())
	for _, sp := range specs {
		if *only != "" && sp.name != *only {
			continue
		}
		s := newStore(ctx, base)
		before := heap()
		m, fitFn, refreshFn, err := sp.make(s, base)
		if err != nil {
			log.Fatal(err)
		}
		start := time.Now()
		if err := fitFn(ctx); err != nil {
			log.Fatal(err)
		}
		fitTook := time.Since(start)
		modelHeap := heap() - before
		p50, p95, p99 := latency(ctx, m, users)
		qps := throughput(ctx, m, users)
		ref := "full fit"
		if sp.name == "popularity" {
			ref = "online (per signal)"
		}
		if refreshFn != nil {
			if _, err := s.AddBatch(ctx, tail); err != nil {
				log.Fatal(err)
			}
			start := time.Now()
			if err := refreshFn(ctx); err != nil {
				log.Fatal(err)
			}
			ref = since(start).String()
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%v\t%.0f MB\t%s\t%s\t%s\t%s\t%.0f req/s\t\n", sp.name, sp.params, fitTook.Round(10*time.Millisecond),
			float64(modelHeap)/1e6, ref, ms(p50), ms(p95), ms(p99), qps)
		_ = w.Flush()
		runtime.KeepAlive(m)
	}
}

func newStore(ctx context.Context, sigs []recoant.Signal) *store.Store {
	s, err := store.New(signal.DefaultModel())
	if err != nil {
		log.Fatal(err)
	}
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		log.Fatal(err)
	}
	return s
}

// sample returns n users with a history, spread over the index range.
func sample(s *store.Store, n int) []recoant.Ref {
	total := s.Len(recoant.User)
	out := make([]recoant.Ref, 0, n)
	for i := range n {
		if r, ok := s.Ref(recoant.User, uint32(i*total/n)); ok {
			out = append(out, r)
		}
	}
	return out
}

func request(u recoant.Ref) recoant.Request {
	return recoant.Request{Subject: u, Target: "item", K: 10, ExcludeSeen: true}
}

// latency measures requests one at a time and returns p50, p95 and p99.
func latency(ctx context.Context, m model, users []recoant.Ref) (p50, p95, p99 time.Duration) {
	d := make([]time.Duration, len(users))
	for i, u := range users {
		start := time.Now()
		if _, err := m.Recommend(ctx, request(u)); err != nil {
			log.Fatal(err)
		}
		d[i] = time.Since(start)
	}
	slices.Sort(d)
	q := func(p float64) time.Duration { return d[min(len(d)-1, int(p*float64(len(d))))] }
	return q(0.5), q(0.95), q(0.99)
}

// throughput sends all requests from one goroutine per CPU.
func throughput(ctx context.Context, m model, users []recoant.Ref) float64 {
	var next atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for range runtime.NumCPU() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(users) {
					return
				}
				if _, err := m.Recommend(ctx, request(users[i])); err != nil {
					log.Fatal(err)
				}
			}
		}()
	}
	wg.Wait()
	return float64(len(users)) / time.Since(start).Seconds()
}

// scaleStore fills a store with 40 million interactions of 2 million users
// with 500,000 items (Zipf-distributed popularity) and reports its memory.
func scaleStore() {
	const users, items, interactions = 2_000_000, 500_000, 40_000_000
	s, err := store.New(signal.DefaultModel())
	if err != nil {
		log.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	zipf := rand.NewZipf(rng, 1.05, 1, items-1)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	uids := make([]string, users)
	for i := range uids {
		uids[i] = "u" + strconv.Itoa(i)
	}
	iids := make([]string, items)
	for i := range iids {
		iids[i] = "i" + strconv.Itoa(i)
	}
	before := heap()
	start := time.Now()
	for n := range interactions {
		sig := recoant.Signal{
			Subject: recoant.Ref{Kind: recoant.User, ID: uids[n%users]},
			Object:  recoant.Ref{Kind: "item", ID: iids[zipf.Uint64()]},
			Type:    recoant.Click, Value: 1, Time: t0.Add(time.Duration(n) * time.Second / 10),
		}
		if err := s.Add(sig); err != nil {
			log.Fatal(err)
		}
	}
	took := time.Since(start)
	h := heap() - before
	st := s.Stats()
	fmt.Printf("scale: %d signals of %d users on %d items in %v = %.2f million signals/s\n", interactions, users, items, took.Round(time.Millisecond), float64(interactions)/took.Seconds()/1e6)
	fmt.Printf("scale: %d distinct interactions, store heap %.2f GB (%.1f bytes per interaction, IDs included)\n", st.Interactions, float64(h)/1e9, float64(h)/float64(st.Interactions))
	runtime.KeepAlive(uids)
	runtime.KeepAlive(iids)
	runtime.KeepAlive(s)
}

func heap() int64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f ms", float64(d)/1e6) }

func since(t time.Time) time.Duration { return time.Since(t).Round(time.Millisecond) }

// cpuName returns the processor model, if the system tells it.
func cpuName() string {
	if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
		return strings.TrimSpace(string(out))
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if name, ok := strings.CutPrefix(line, "model name\t: "); ok {
				return name
			}
		}
	}
	return runtime.GOARCH
}

// load reads a gzip-compressed recoant-data signals file.
func load(path string) ([]recoant.Signal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil, err
	}
	r := csv.NewReader(gz)
	r.ReuseRecord = true
	if _, err := r.Read(); err != nil {
		return nil, err
	}
	var out []recoant.Signal
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, rec[0])
		if err != nil {
			return nil, err
		}
		v, err := strconv.ParseFloat(rec[6], 64)
		if err != nil {
			return nil, err
		}
		out = append(out, recoant.Signal{
			Time:    t,
			Subject: recoant.Ref{Kind: recoant.Kind(rec[1]), ID: rec[2]},
			Object:  recoant.Ref{Kind: recoant.Kind(rec[3]), ID: rec[4]},
			Type:    recoant.SignalType(rec[5]), Value: v, Session: rec[7],
		})
	}
}
