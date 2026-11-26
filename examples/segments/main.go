// Command segments measures how well segments recover the hidden taste
// clusters of basic/taste-clusters in recoant-data
// (https://github.com/breskos/recoant-data):
//
//	go run ./examples/segments -data ../recoant-data/synthetic
//
// 5,000 users belong to 10 hidden taste clusters. The program trains ALS on
// their signals, segments the users on their taste vectors with k-means and
// mini-batch k-means, and compares the segments with the true clusters:
// purity (share of users in the majority cluster of their segment) and the
// adjusted Rand index (1 = identical partitions, 0 = as good as random). It
// prints the profile of two segments and the hit rate per segment.
package main

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/evaluation"
	"github.com/breskos/recoant/segment"
)

const base = `{
  "recoant": 1,
  "kinds": {"user": {}, "item": {}},
  "relations": {"interacts": {"subject": "user", "object": "item"}},
  "signals": {
    "visit": {"relation": "interacts", "weight": 1}, "click": {"relation": "interacts", "weight": 2},
    "dwell": {"relation": "interacts", "weight": 3, "transform": {"kind": "saturate", "param": 60}},
    "cart": {"relation": "interacts", "weight": 5}, "purchase": {"relation": "interacts", "weight": 10}
  },
  "models": {"taste": {"algorithm": "als", "relation": "interacts", "params": {"factors": 32, "seed": 1}}},
  "serving": {"for_you": {"model": "taste", "exclude_seen": true}},
  "segments": {"user": {"k": 10, "features": ["taste"], "model": "taste", "seed": 1 BATCH}}
}`

func main() {
	data := flag.String("data", "../recoant-data/synthetic", "folder of the synthetic module of recoant-data")
	flag.Parse()
	if err := run(context.Background(), os.Stdout, filepath.Join(*data, "basic", "taste-clusters")); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, w io.Writer, dir string) error {
	sigs, err := load(filepath.Join(dir, "signals.csv"))
	if err != nil {
		return err
	}
	truth := map[recoant.Ref]int{}
	if err := readTable(filepath.Join(dir, "users.csv"), func(rec []string) {
		c, _ := strconv.Atoi(rec[1])
		truth[recoant.U(rec[0])] = c
	}); err != nil {
		return err
	}
	first, last := sigs[0].Time, sigs[len(sigs)-1].Time
	train, test := evaluation.SplitByTime(sigs, first.Add(last.Sub(first)*8/10))
	_, _ = fmt.Fprintf(w, "basic/taste-clusters: %d users in 10 hidden clusters, %d training signals\n\n", len(truth), len(train))

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintln(tw, "segmentation\tsegments\tpurity\tadjusted Rand\tfit time\t")
	var keep *config.Engine
	for _, v := range []struct{ name, batch string }{{"k-means", ""}, {"mini-batch k-means (256)", `, "batch_size": 256`}} {
		c, err := config.Load(strings.NewReader(strings.Replace(base, "BATCH", v.batch, 1)))
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
		seg, _ := e.Segmenter(recoant.User)
		start := time.Now()
		if err := seg.Fit(ctx); err != nil {
			return err
		}
		took := time.Since(start)
		purity, ari := compare(seg, truth)
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%.3f\t%.3f\t%s\t\n", v.name, len(seg.Profiles()), purity, ari, took.Round(time.Millisecond))
		if keep == nil {
			keep = e
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	seg, _ := keep.Segmenter(recoant.User)
	for _, p := range seg.Profiles()[:2] {
		var typical []string
		for _, x := range p.Typical[:min(5, len(p.Typical))] {
			typical = append(typical, x.Ref.ID)
		}
		_, _ = fmt.Fprintf(w, "\nsegment s%d: %d users (%.0f %%), %.1f items each, typical items %s\n", p.Segment, p.Size, 100*p.Share, p.Activity, strings.Join(typical, ", "))
	}
	reps, err := evaluation.BySegment(ctx, serving{keep}, test, evaluation.Config{K: 10, Target: "item", ExcludeSeen: true}, func(r recoant.Ref) (string, bool) {
		j, ok := seg.Segment(r)
		return fmt.Sprintf("s%d", j), ok
	})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "\nhit rate@10 per segment:")
	for j := range seg.Profiles() {
		r := reps[fmt.Sprintf("s%d", j)]
		_, _ = fmt.Fprintf(w, "  s%d  %.3f  (%d users)\n", j, r.HitRate, r.Subjects)
	}
	return nil
}

type serving struct{ e *config.Engine }

func (s serving) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	return s.e.Recommend(ctx, "for_you", req.Subject, req.K)
}

// compare returns the purity and the adjusted Rand index of the segments
// against the true clusters.
func compare(seg *segment.Segmenter, truth map[recoant.Ref]int) (purity, ari float64) {
	table := map[[2]int]int{}
	rows, cols := map[int]int{}, map[int]int{}
	n := 0
	for ref, c := range truth {
		s, ok := seg.Segment(ref)
		if !ok {
			continue
		}
		table[[2]int{s, c}]++
		rows[s]++
		cols[c]++
		n++
	}
	best := map[int]int{}
	for k, v := range table {
		best[k[0]] = max(best[k[0]], v)
	}
	for _, v := range best {
		purity += float64(v)
	}
	purity /= float64(n)
	pairs := func(x int) float64 { return float64(x) * float64(x-1) / 2 }
	var sumT, sumR, sumC float64
	for _, v := range table {
		sumT += pairs(v)
	}
	for _, v := range rows {
		sumR += pairs(v)
	}
	for _, v := range cols {
		sumC += pairs(v)
	}
	expected := sumR * sumC / pairs(n)
	return purity, (sumT - expected) / ((sumR+sumC)/2 - expected)
}

func load(path string) ([]recoant.Signal, error) {
	var out []recoant.Signal
	err := readTable(path, func(rec []string) {
		t, _ := time.Parse(time.RFC3339, rec[0])
		v, _ := strconv.ParseFloat(rec[6], 64)
		out = append(out, recoant.Signal{
			Time: t, Subject: recoant.Ref{Kind: recoant.Kind(rec[1]), ID: rec[2]}, Object: recoant.Ref{Kind: recoant.Kind(rec[3]), ID: rec[4]},
			Type: recoant.SignalType(rec[5]), Value: v, Session: rec[7],
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
