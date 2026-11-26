// Command evaluate compares the recommenders of recoant on the synthetic
// data sets of recoant-data (https://github.com/breskos/recoant-data).
//
//	go run ./examples/evaluate -data ../recoant-data/synthetic
//
// Each data set is split by time (80 % train, 20 % test); every model is
// trained on the train part and asked for the top 10 for each test user,
// excluding what the user has already seen. Two references frame the
// results: "random" ranks the catalog at random (the floor) and "oracle"
// ranks it with the hidden structure that generated the data set — true
// popularity ranks, true clusters or true latent factors (the ceiling a
// model could reach from the structure alone). On the explicit ratings, the
// matrix factorizations are measured by the RMSE of predicted ratings.
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
	"os"
	"path/filepath"
	"slices"
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
		oracle, err := oracleFor(filepath.Join(*data, tk.set))
		if err != nil {
			log.Fatal(err)
		}
		models = append(models,
			struct {
				name string
				make func() (recoant.Recommender, error)
			}{"random", func() (recoant.Recommender, error) {
				return ranked{s, func(u, i recoant.Ref) float64 { return hash(u.ID + "|" + i.ID) }}, nil
			}},
			struct {
				name string
				make func() (recoant.Recommender, error)
			}{"oracle", func() (recoant.Recommender, error) { return ranked{s, oracle}, nil }},
		)
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

// ranked recommends the candidates of the target kind known to the store
// in the order of a score function; the references random and oracle.
type ranked struct {
	s     *store.Store
	score func(subject, candidate recoant.Ref) float64
}

func (r ranked) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
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

// oracleFor returns the score of the hidden structure of a data set: the
// true rank for zipf-popularity, the true cluster (then rank in cluster)
// for taste clusters and the social graph, the true affinity p_u·q_i + b_i
// for latent factors. Ties are broken at random.
func oracleFor(dir string) (func(subject, candidate recoant.Ref) float64, error) {
	table := func(name string) (map[string][]string, error) {
		m := map[string][]string{}
		err := readTable(filepath.Join(dir, name), func(rec []string) { m[rec[0]] = slices.Clone(rec[1:]) })
		return m, err
	}
	num := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	switch filepath.Base(dir) {
	case "zipf-popularity":
		items, err := table("items.csv") // item_id, rank, share
		return func(_, c recoant.Ref) float64 { return -num(items[c.ID][0]) }, err
	case "taste-clusters", "social":
		users, err := table("users.csv") // user_id, cluster
		if err != nil {
			return nil, err
		}
		items, err := table("items.csv") // item_id, cluster, rank_in_cluster
		return func(u, c recoant.Ref) float64 {
			if c.Kind == recoant.User {
				if users[c.ID][0] == users[u.ID][0] {
					return 1 + hash(u.ID+"|"+c.ID)
				}
				return hash(u.ID + "|" + c.ID)
			}
			if items[c.ID][0] == users[u.ID][0] {
				return 1e6 - num(items[c.ID][1])
			}
			return -num(items[c.ID][1])
		}, err
	case "implicit-feedback":
		users, err := table("users.csv") // user_id, bias, f1…f8
		if err != nil {
			return nil, err
		}
		items, err := table("items.csv") // item_id, bias, f1…f8
		return func(u, c recoant.Ref) float64 {
			p, q := users[u.ID], items[c.ID]
			v := num(q[0])
			for k := 1; k < len(q); k++ {
				v += num(p[k]) * num(q[k])
			}
			return v
		}, err
	}
	return nil, fmt.Errorf("no oracle for %s", dir)
}

func readTable(path string, fn func(rec []string)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(bufio.NewReader(f))
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
