// Package evaluation measures the quality of recommendations offline.
//
// Split the signals into a training and a test part ([SplitByTime],
// [SplitByFraction], [LeaveLastOut]), train a recommender on the first and
// measure with [Evaluate] how well it predicts the second. The metrics treat
// relevance as binary: an object is relevant to a subject if the test part
// has a relevant signal between them.
package evaluation

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"runtime"
	"slices"
	"sync"

	"github.com/breskos/recoant"
)

// Config configures an evaluation.
type Config struct {
	// K is the number of recommendations per subject.
	K int
	// Subject is the kind of the subjects; default [recoant.User].
	Subject recoant.Kind
	// Target is the kind of the recommended objects.
	Target recoant.Kind
	// Relevant lists the signal types that make an object relevant, e.g.
	// only purchases. Empty means every signal type.
	Relevant []recoant.SignalType
	// ExcludeSeen is passed on in each request.
	ExcludeSeen bool
	// Catalog is the number of objects of the target kind. If positive,
	// the report contains the catalog coverage.
	Catalog int
	// Workers is the number of parallel requests; default the number of
	// CPUs.
	Workers int
}

// Report holds the metrics averaged over all evaluated subjects.
type Report struct {
	K           int     `json:"k"`
	Subjects    int     `json:"subjects"`
	Precision   float64 `json:"precision"`
	Recall      float64 `json:"recall"`
	NDCG        float64 `json:"ndcg"`
	MAP         float64 `json:"map"`
	HitRate     float64 `json:"hit_rate"`
	MRR         float64 `json:"mrr"`
	Recommended int     `json:"recommended"`        // distinct recommended objects
	Coverage    float64 `json:"coverage,omitempty"` // Recommended / Catalog
}

// WriteTo writes the report as a table.
func (r Report) WriteTo(w io.Writer) (int64, error) {
	n, err := fmt.Fprintf(w,
		"subjects     %d\nprecision@%d  %.4f\nrecall@%d     %.4f\nndcg@%d       %.4f\nmap@%d        %.4f\nhit rate@%d   %.4f\nmrr          %.4f\nrecommended  %d\ncoverage     %.4f\n",
		r.Subjects, r.K, r.Precision, r.K, r.Recall, r.K, r.NDCG, r.K, r.MAP, r.K, r.HitRate, r.MRR, r.Recommended, r.Coverage)
	return int64(n), err
}

// Truth maps subjects to their relevant objects.
type Truth map[recoant.Ref]Relevant

// GroundTruth collects the relevant objects per subject from test signals.
func GroundTruth(test []recoant.Signal, cfg Config) Truth {
	subject := cmp.Or(cfg.Subject, recoant.User)
	truth := make(Truth)
	for _, s := range test {
		if s.Subject.Kind != subject || s.Object.Kind != cfg.Target {
			continue
		}
		if len(cfg.Relevant) > 0 && !slices.Contains(cfg.Relevant, s.Type) {
			continue
		}
		rel := truth[s.Subject]
		if rel == nil {
			rel = make(Relevant)
			truth[s.Subject] = rel
		}
		rel[s.Object] = struct{}{}
	}
	return truth
}

// Evaluate asks r for recommendations for every subject in the test signals
// with at least one relevant object and averages the metrics.
func Evaluate(ctx context.Context, r recoant.Recommender, test []recoant.Signal, cfg Config) (Report, error) {
	if cfg.K <= 0 || cfg.Target == "" {
		return Report{}, fmt.Errorf("evaluation: K must be positive and Target set")
	}
	truth := GroundTruth(test, cfg)
	subjects := make([]recoant.Ref, 0, len(truth))
	for s := range truth {
		subjects = append(subjects, s)
	}
	slices.SortFunc(subjects, func(a, b recoant.Ref) int { return cmp.Compare(a.ID, b.ID) })

	type result struct {
		m    metrics
		recs []recoant.Ref
	}
	results := make([]result, len(subjects))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		errOnce  sync.Once
		firstErr error
	)
	next := make(chan int)
	workers := cmp.Or(cfg.Workers, runtime.NumCPU())
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				res, err := r.Recommend(ctx, recoant.Request{
					Subject: subjects[i], Target: cfg.Target, K: cfg.K, ExcludeSeen: cfg.ExcludeSeen,
				})
				if err != nil {
					errOnce.Do(func() { firstErr = fmt.Errorf("evaluation: subject %s: %w", subjects[i], err); cancel() })
					continue
				}
				recs := make([]recoant.Ref, len(res))
				for k, x := range res {
					recs[k] = x.Ref
				}
				results[i] = result{m: measure(recs, truth[subjects[i]], cfg.K), recs: recs}
			}
		}()
	}
feed:
	for i := range subjects {
		select {
		case next <- i:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	if firstErr != nil {
		return Report{}, firstErr
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}

	rep := Report{K: cfg.K, Subjects: len(subjects)}
	distinct := make(map[recoant.Ref]struct{})
	for _, res := range results {
		rep.Precision += res.m.precision
		rep.Recall += res.m.recall
		rep.NDCG += res.m.ndcg
		rep.MAP += res.m.ap
		rep.HitRate += res.m.hit
		rep.MRR += res.m.rr
		for _, x := range res.recs {
			distinct[x] = struct{}{}
		}
	}
	if n := float64(len(subjects)); n > 0 {
		rep.Precision /= n
		rep.Recall /= n
		rep.NDCG /= n
		rep.MAP /= n
		rep.HitRate /= n
		rep.MRR /= n
	}
	rep.Recommended = len(distinct)
	if cfg.Catalog > 0 {
		rep.Coverage = float64(rep.Recommended) / float64(cfg.Catalog)
	}
	return rep, nil
}

type metrics struct{ precision, recall, ndcg, ap, hit, rr float64 }

func measure(recs []recoant.Ref, rel Relevant, k int) metrics {
	return metrics{
		precision: Precision(recs, rel, k),
		recall:    Recall(recs, rel, k),
		ndcg:      NDCG(recs, rel, k),
		ap:        AveragePrecision(recs, rel, k),
		hit:       HitRate(recs, rel, k),
		rr:        ReciprocalRank(recs, rel, k),
	}
}
