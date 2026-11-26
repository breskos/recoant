// Command evaluate compares the recommenders of recoant on the synthetic
// data sets of recoant-data (https://github.com/breskos/recoant-data).
//
//	go run ./examples/evaluate -data ../recoant-data/synthetic
//
// Each data set is split by time (80 % train, 20 % test); every model is
// trained on the train part and asked for the top 10 for each test user,
// excluding what the user has already seen. On the explicit ratings, the
// matrix factorizations are measured by the RMSE of predicted ratings.
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
	"text/tabwriter"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/algo/bpr"
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/algo/mf"
	"github.com/breskos/recoant/algo/popularity"
	"github.com/breskos/recoant/algo/svd"
	"github.com/breskos/recoant/evaluation"
	"github.com/breskos/recoant/signal"
	"github.com/breskos/recoant/store"
)

type task struct {
	set      string       // data set folder
	object   recoant.Kind // kind of the recommended objects
	target   recoant.Kind // kind to recommend: object or user
	relevant []recoant.SignalType
}

var tasks = []task{
	{"basic/zipf-popularity", "item", "item", nil},
	{"basic/taste-clusters", "item", "item", nil},
	{"latent/implicit-feedback", "item", "item", nil},
	{"relations/social", "item", recoant.User, []recoant.SignalType{"follow"}},
}

func main() {
	data := flag.String("data", "../recoant-data/synthetic", "folder of the recoant-data synthetic module")
	k := flag.Int("k", 10, "number of recommendations")
	flag.Parse()
	ctx := context.Background()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintf(w, "data set\tmodel\tusers\tprecision@%d\trecall@%d\tndcg@%d\thit rate\tcoverage\tfit\t\n", *k, *k, *k)
	for _, tk := range tasks {
		sigs, err := load(filepath.Join(*data, tk.set, "signals.csv"))
		if err != nil {
			log.Fatal(err)
		}
		train, test, err := evaluation.SplitByFraction(sigs, 0.8)
		if err != nil {
			log.Fatal(err)
		}
		s, err := store.New(signal.DefaultModel())
		if err != nil {
			log.Fatal(err)
		}
		if _, err := s.AddBatch(ctx, train); err != nil {
			log.Fatal(err)
		}
		rel := store.Relation{Subject: recoant.User, Object: tk.object}
		models := []struct {
			name string
			make func() (recoant.Recommender, error)
		}{
			{"popularity", func() (recoant.Recommender, error) {
				m, err := popularity.New(signal.DefaultModel(), popularity.Config{})
				if err != nil {
					return nil, err
				}
				m.SetSeen(s)
				return m, m.Fit(ctx, train)
			}},
			{"item-knn", func() (recoant.Recommender, error) {
				m, err := knn.NewItemKNN(s, knn.Config{Relation: rel, Shrink: 10})
				if err != nil {
					return nil, err
				}
				return m, m.Fit(ctx)
			}},
			{"user-knn", func() (recoant.Recommender, error) {
				m, err := knn.NewUserKNN(s, knn.Config{Relation: rel, Shrink: 10})
				if err != nil {
					return nil, err
				}
				return m, m.Fit(ctx)
			}},
			{"pure-svd", func() (recoant.Recommender, error) {
				m, err := svd.New(s, svd.Config{Relation: rel, Factors: 32, Seed: 1})
				if err != nil {
					return nil, err
				}
				return m, m.Fit(ctx)
			}},
			{"als", func() (recoant.Recommender, error) {
				m, err := als.New(s, als.Config{Relation: rel, Seed: 1})
				if err != nil {
					return nil, err
				}
				return m, m.Fit(ctx)
			}},
			{"bpr", func() (recoant.Recommender, error) {
				m, err := bpr.New(s, bpr.Config{Relation: rel, Seed: 1})
				if err != nil {
					return nil, err
				}
				return m, m.Fit(ctx)
			}},
		}
		catalog := s.Len(tk.target)
		for _, md := range models {
			if tk.target == recoant.User && (md.name == "popularity" || md.name == "item-knn") {
				continue // these do not recommend users
			}
			start := time.Now()
			r, err := md.make()
			if err != nil {
				log.Fatal(err)
			}
			fit := time.Since(start)
			rep, err := evaluation.Evaluate(ctx, r, test, evaluation.Config{
				K: *k, Target: tk.target, Relevant: tk.relevant,
				ExcludeSeen: tk.target != recoant.User, Catalog: catalog,
			})
			if err != nil {
				log.Fatal(err)
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%.4f\t%.4f\t%.4f\t%.4f\t%.3f\t%v\t\n",
				tk.set, md.name, rep.Subjects, rep.Precision, rep.Recall, rep.NDCG, rep.HitRate, rep.Coverage, fit.Round(time.Millisecond))
		}
	}
	_ = w.Flush()
	if err := ratings(ctx, filepath.Join(*data, "latent/explicit-ratings/signals.csv")); err != nil {
		log.Fatal(err)
	}
}

// ratings measures the prediction error of the matrix factorizations on
// star ratings.
func ratings(ctx context.Context, path string) error {
	sigs, err := load(path)
	if err != nil {
		return err
	}
	train, test, err := evaluation.SplitByFraction(sigs, 0.8)
	if err != nil {
		return err
	}
	model := signal.Model{Rules: map[recoant.SignalType]signal.Rule{
		"rating": {Weight: 1, Transform: signal.Transform{Kind: signal.Linear}},
	}}
	s, err := store.New(model)
	if err != nil {
		return err
	}
	if _, err := s.AddBatch(ctx, train); err != nil {
		return err
	}
	var mean float64
	for _, x := range train {
		mean += x.Value
	}
	mean /= float64(len(train))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintf(w, "\nlatent/explicit-ratings\tmodel\tratings\tRMSE\tMAE\tfit\t\n")
	rep := evaluation.Errors(constant(mean), test, nil)
	_, _ = fmt.Fprintf(w, "\tglobal mean\t%d\t%.4f\t%.4f\t\t\n", rep.N, rep.RMSE, rep.MAE)
	rel := store.Relation{Subject: recoant.User, Object: "movie"}
	for _, v := range []mf.Variant{mf.Funk, mf.Biased, mf.SVDPlusPlus} {
		m, err := mf.New(s, mf.Config{Relation: rel, Variant: v, Factors: 10, Epochs: 100, LearnRate: 0.005, Reg: 0.1, Min: 1, Max: 5, Seed: 1})
		if err != nil {
			return err
		}
		start := time.Now()
		if err := m.Fit(ctx); err != nil {
			return err
		}
		fit := time.Since(start)
		rep := evaluation.Errors(m, test, nil)
		_, _ = fmt.Fprintf(w, "\t%s\t%d\t%.4f\t%.4f\t%v\t\n", v, rep.N, rep.RMSE, rep.MAE, fit.Round(time.Millisecond))
	}
	return w.Flush()
}

type constant float64

func (c constant) Predict(recoant.Ref, recoant.Ref) (float64, bool) { return float64(c), true }

// load reads a recoant-data signals file.
func load(path string) ([]recoant.Signal, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	if _, err := r.Read(); err != nil { // header
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
