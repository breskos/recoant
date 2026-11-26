package evaluation

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/breskos/recoant"
)

// MutualReport measures recommendations where both sides must agree.
type MutualReport struct {
	K        int `json:"k"`
	Subjects int `json:"subjects"`
	// HitRate is the share of subjects with at least one recommended
	// object they showed interest in (one-way success).
	HitRate float64 `json:"hit_rate"`
	// MutualHitRate is the share of subjects with at least one match among
	// their recommendations: the subject showed interest in the object in
	// the test signals, and the object in the subject in any signals.
	MutualHitRate float64 `json:"mutual_hit_rate"`
	// MutualPrecision is the share of recommendations that are matches.
	MutualPrecision float64 `json:"mutual_precision"`
	// Recommended is the number of distinct recommended objects.
	Recommended int `json:"recommended"`
	// Gini is the concentration of recommendations over the recommended
	// objects: 0 if every object is recommended equally often, close to 1
	// if a few objects get almost all recommendations.
	Gini float64 `json:"gini"`
}

// WriteTo writes the report as a table.
func (r MutualReport) WriteTo(w io.Writer) (int64, error) {
	n, err := fmt.Fprintf(w,
		"subjects            %d\nhit rate@%d          %.4f\nmutual hit rate@%d   %.4f\nmutual precision@%d  %.4f\nrecommended         %d\ngini                %.4f\n",
		r.Subjects, r.K, r.HitRate, r.K, r.MutualHitRate, r.K, r.MutualPrecision, r.Recommended, r.Gini)
	return int64(n), err
}

// Mutual asks r for recommendations for every subject with a relevant test
// signal and measures how many of them are matches: pairs where the
// subject showed interest in the object in test and the object showed
// interest in the subject in train or test. cfg.Subject and cfg.Target are
// usually the same kind (people), but need not be (candidates and jobs,
// with the reverse signals from jobs to candidates).
func Mutual(ctx context.Context, r recoant.Recommender, train, test []recoant.Signal, cfg Config) (MutualReport, error) {
	if cfg.K <= 0 || cfg.Target == "" {
		return MutualReport{}, fmt.Errorf("evaluation: K must be positive and Target set")
	}
	subjectKind := cmp.Or(cfg.Subject, recoant.User)
	truth := GroundTruth(test, cfg)
	back := GroundTruth(append(slices.Clip(train), test...), Config{Subject: cfg.Target, Target: subjectKind, Relevant: cfg.Relevant})
	subjects := make([]recoant.Ref, 0, len(truth))
	for s := range truth {
		subjects = append(subjects, s)
	}
	slices.SortFunc(subjects, func(a, b recoant.Ref) int { return cmp.Compare(a.ID, b.ID) })

	rep := MutualReport{K: cfg.K, Subjects: len(subjects)}
	counts := make(map[recoant.Ref]int)
	matches, total := 0, 0
	for _, s := range subjects {
		res, err := r.Recommend(ctx, recoant.Request{Subject: s, Target: cfg.Target, K: cfg.K, ExcludeSeen: cfg.ExcludeSeen})
		if err != nil {
			return MutualReport{}, fmt.Errorf("evaluation: subject %s: %w", s, err)
		}
		hit, mutual := false, false
		for _, x := range res {
			counts[x.Ref]++
			total++
			if _, ok := truth[s][x.Ref]; !ok {
				continue
			}
			hit = true
			if _, ok := back[x.Ref][s]; ok {
				mutual = true
				matches++
			}
		}
		if hit {
			rep.HitRate++
		}
		if mutual {
			rep.MutualHitRate++
		}
	}
	if n := float64(len(subjects)); n > 0 {
		rep.HitRate /= n
		rep.MutualHitRate /= n
		rep.MutualPrecision = float64(matches) / (n * float64(cfg.K))
	}
	rep.Recommended = len(counts)
	rep.Gini = gini(counts, total)
	return rep, nil
}

// gini returns the Gini coefficient of the counts.
func gini(counts map[recoant.Ref]int, total int) float64 {
	if len(counts) < 2 || total == 0 {
		return 0
	}
	xs := make([]float64, 0, len(counts))
	for _, c := range counts {
		xs = append(xs, float64(c))
	}
	slices.Sort(xs)
	n, sum := float64(len(xs)), 0.0
	for i, x := range xs {
		sum += (2*float64(i+1) - n - 1) * x
	}
	return sum / (n * float64(total))
}
