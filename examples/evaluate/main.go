// Command evaluate compares the recommenders of recoant on the synthetic
// data sets of recoant-data (https://github.com/breskos/recoant-data).
//
//	go run ./examples/evaluate -data ../recoant-data/synthetic
//
// Each data set is split by time (80 % train, 20 % test); every model is
// trained on the train part and asked for the top 10 for each test user,
// excluding what the user has already seen.
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
	"github.com/breskos/recoant/algo/knn"
	"github.com/breskos/recoant/algo/popularity"
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
		}
		catalog := s.Len(tk.target)
		for _, md := range models {
			if tk.target == recoant.User && md.name != "user-knn" {
				continue // only user-kNN recommends users
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
}

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
