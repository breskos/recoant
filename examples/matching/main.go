// Command matching measures reciprocal matching on the dating data set of
// recoant-data (https://github.com/breskos/recoant-data):
//
//	go run ./examples/matching -data ../recoant-data/synthetic
//
// It trains on the first 80 % of the period and asks every user with a
// like in the rest for k profiles, once one-way (ALS on likes) and once
// reciprocal (both directions, harmonic mean), with and without exposure
// balancing. A recommendation is a match if the user likes the profile in
// the test period and the profile likes the user at any time. A random
// ranking is the floor; an oracle that knows the hidden styles and
// attractiveness and ranks by the true probability of a mutual like is the
// ceiling.
package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/evaluation"
	"github.com/breskos/recoant/store"
)

const base = `{
  "recoant": 1,
  "kinds": {"user": {}},
  "relations": {"likes": {"subject": "user", "object": "user", "channels": {"negative": {"no_decay": true}}}},
  "signals": {
    "view": {"relation": "likes", "weight": 0.5},
    "like": {"relation": "likes", "weight": 5},
    "message": {"relation": "likes", "weight": 8},
    "pass": {"relation": "likes", "feedback": "negative", "weight": 3}
  },
  "models": {
    "attraction": {"algorithm": "als", "relation": "likes", "params": {"factors": 16, "alpha": 10, "seed": 1}},
    "popular": {"algorithm": "popularity"}
  },
  "serving": {"match": {"model": "attraction", "target": "user", "exclude_seen": true, "fallback": "popular"}}
  MATCHING
}`

var variants = []struct{ name, matching string }{
	{"one-way ALS", ``},
	{"reciprocal (harmonic)", `,"matching": {"likes": {"mode": "reciprocal"}}`},
	{"reciprocal + exposure 0.5", `,"matching": {"likes": {"mode": "reciprocal", "exposure_balancing": 0.5}}`},
	{"reciprocal + exposure 1", `,"matching": {"likes": {"mode": "reciprocal", "exposure_balancing": 1}}`},
}

func main() {
	data := flag.String("data", "../recoant-data/synthetic", "folder of the synthetic module of recoant-data")
	k := flag.Int("k", 10, "recommendations per user")
	flag.Parse()
	if err := run(context.Background(), os.Stdout, filepath.Join(*data, "matching", "dating"), *k); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, w io.Writer, dir string, k int) error {
	sigs, err := load(filepath.Join(dir, "signals.csv"))
	if err != nil {
		return err
	}
	first, last := sigs[0].Time, sigs[len(sigs)-1].Time
	cut := first.Add(last.Sub(first) * 8 / 10)
	train, test := evaluation.SplitByTime(sigs, cut)
	cfg := evaluation.Config{K: k, Target: recoant.User, Relevant: []recoant.SignalType{"like", "message"}, ExcludeSeen: true}
	_, _ = fmt.Fprintf(w, "matching/dating: %d signals, train until %s (%d), test %d; k = %d\n\n", len(sigs), cut.Format(time.DateOnly), len(train), len(test), k)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintln(tw, "recommender\texpected matches\tof ceiling\thit rate\tmutual hit rate\tdistinct\tgini\tms/request\t")
	type row struct {
		name     string
		rep      evaluation.MutualReport
		ms       float64
		expected float64
	}
	var rows []row
	oracle, err := oracleFor(dir)
	if err != nil {
		return err
	}
	var subjects []recoant.Ref
	for s := range evaluation.GroundTruth(test, cfg) {
		subjects = append(subjects, s)
	}
	measure := func(name string, r recoant.Recommender) error {
		start := time.Now()
		rep, err := evaluation.Mutual(ctx, r, train, test, cfg)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		ms := float64(time.Since(start).Microseconds()) / 1000 / float64(max(rep.Subjects, 1))
		// Expected matches: the true probability of a mutual like, summed
		// over the recommendations, averaged over the users.
		sum := 0.0
		for _, s := range subjects {
			res, err := r.Recommend(ctx, recoant.Request{Subject: s, Target: recoant.User, K: k, ExcludeSeen: true})
			if err != nil {
				return err
			}
			for _, x := range res {
				sum += oracle(s, x.Ref)
			}
		}
		rows = append(rows, row{name, rep, ms, sum / float64(max(len(subjects), 1))})
		return nil
	}

	var st *store.Store
	for _, v := range variants {
		c, err := config.Load(strings.NewReader(strings.Replace(base, "MATCHING", v.matching, 1)))
		if err != nil {
			return err
		}
		e, err := c.Build()
		if err != nil {
			return err
		}
		if err := e.AddBatch(ctx, train); err != nil {
			return err
		}
		if err := e.Fit(ctx); err != nil {
			return err
		}
		st = e.Store()
		if err := measure(v.name, serving{e}); err != nil {
			return err
		}
	}
	if err := measure("random (floor)", ranked{st, func(s, c recoant.Ref) float64 { return hash(s.ID + "|" + c.ID) }}); err != nil {
		return err
	}
	if err := measure("oracle (ceiling)", ranked{st, oracle}); err != nil {
		return err
	}
	floor, ceil := rows[len(rows)-2].expected, rows[len(rows)-1].expected
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%.3f\t%.0f %%\t%.3f\t%.4f\t%d\t%.2f\t%.2f\t\n", r.name, r.expected, 100*(r.expected-floor)/(ceil-floor), r.rep.HitRate, r.rep.MutualHitRate, r.rep.Recommended, r.rep.Gini, r.ms)
	}
	_, _ = fmt.Fprintf(w, "%d users with a like in the test period\n", rows[0].rep.Subjects)
	return tw.Flush()
}

// serving asks the engine's "match" serving.
type serving struct{ e *config.Engine }

func (s serving) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	return s.e.Recommend(ctx, "match", req.Subject, req.K)
}

// oracleFor returns the true probability of a mutual like, from the hidden
// styles and attractiveness in users.csv (the generator's formula).
func oracleFor(dir string) (func(subject, candidate recoant.Ref) float64, error) {
	type user struct {
		style, pref int
		attract     float64
	}
	users := map[string]user{}
	err := readTable(filepath.Join(dir, "users.csv"), func(rec []string) {
		s, _ := strconv.Atoi(rec[1])
		p, _ := strconv.Atoi(rec[2])
		a, _ := strconv.ParseFloat(rec[3], 64)
		users[rec[0]] = user{s, p, a}
	})
	like := func(u, v user) float64 {
		x := -2.0 - 0.8*max(u.attract, 0) + 0.6*v.attract
		if v.style == u.pref {
			x += 3.0
		}
		return 1 / (1 + math.Exp(-x))
	}
	return func(s, c recoant.Ref) float64 {
		u, v := users[s.ID], users[c.ID]
		return like(u, v) * like(v, u)
	}, err
}

// ranked recommends the users known to the store in the order of a score
// function.
type ranked struct {
	s     *store.Store
	score func(subject, candidate recoant.Ref) float64
}

func (r ranked) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	var out []recoant.Scored
	for j := range r.s.Len(req.Target) {
		c, ok := r.s.Ref(req.Target, uint32(j))
		if !ok || c == req.Subject || (req.ExcludeSeen && r.s.Has(req.Subject, c)) {
			continue
		}
		out = append(out, recoant.Scored{Ref: c, Score: r.score(req.Subject, c)})
	}
	slices.SortFunc(out, func(a, b recoant.Scored) int { return cmp.Compare(b.Score, a.Score) })
	return out[:min(req.K, len(out))], nil
}

// hash maps a string to [0, 1), deterministically.
func hash(s string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return float64(h.Sum64()>>11) / (1 << 53)
}

func load(path string) ([]recoant.Signal, error) {
	var out []recoant.Signal
	err := readTable(path, func(rec []string) {
		t, _ := time.Parse(time.RFC3339, rec[0])
		v, _ := strconv.ParseFloat(rec[6], 64)
		out = append(out, recoant.Signal{
			Time:    t,
			Subject: recoant.Ref{Kind: recoant.Kind(rec[1]), ID: rec[2]},
			Object:  recoant.Ref{Kind: recoant.Kind(rec[3]), ID: rec[4]},
			Type:    recoant.SignalType(rec[5]), Value: v, Session: rec[7],
		})
	})
	if err == nil && len(out) == 0 {
		err = errors.New("no signals")
	}
	return out, err
}

func readTable(path string, fn func(rec []string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	if _, err := r.Read(); err != nil {
		return err
	}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		fn(rec)
	}
}
