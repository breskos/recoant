// Command attributes checks attributes, filters, deletion and retention
// end to end on the attribute data sets of recoant-data
// (https://github.com/breskos/recoant-data):
//
//	go run ./examples/attributes -data ../recoant-data/synthetic
//
// For each data set it replays the attribute events and the signals in
// time order into a configured engine up to a split time (80 % of the
// period), deleting accounts and discontinued products when they are
// deleted, applies the retention, trains the models and asks for the top k
// for every subject with relevant signals after the split — once without
// and once with attribute filters. It reports ranking metrics and checks
// the rules: no deleted reference is ever recommended, every filtered
// recommendation meets its filters, retention drops exactly the expected
// pairs and sensitive attributes are not exported. It exits with status 1
// if any rule is broken.
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
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/evaluation"
)

const peopleConfig = `{
  "recoant": 1,
  "kinds": {
    "user": {
      "attributes": {
        "age": {"type": "number", "sensitive": true},
        "min_age": {"type": "number"},
        "max_age": {"type": "number"},
        "gender": {"type": "category", "sensitive": true},
        "location": {"type": "geo", "sensitive": true},
        "interests": {"type": "set"}
      }
    }
  },
  "relations": {
    "likes": {"subject": "user", "object": "user", "half_life": "60d", "channels": {"negative": {"no_decay": true}}}
  },
  "signals": {
    "view": {"relation": "likes", "weight": 0.5},
    "like": {"relation": "likes", "weight": 5},
    "superlike": {"relation": "likes", "weight": 10},
    "message": {"relation": "likes", "weight": 8},
    "pass": {"relation": "likes", "feedback": "negative", "weight": 3},
    "block": {"relation": "likes", "feedback": "negative", "weight": 50}
  },
  "models": {
    "attraction": {"algorithm": "als", "relation": "likes", "params": {"factors": 16, "seed": 1}},
    "popular": {"algorithm": "popularity"}
  },
  "serving": {
    "all": {"model": "attraction", "target": "user", "exclude_seen": true, "fallback": "popular"},
    "filtered": {
      "model": "attraction", "target": "user", "exclude_seen": true, "fallback": "popular",
      "filters": [
        {"attribute": "age", "within": ["min_age", "max_age"], "mutual": true},
        {"attribute": "location", "max_distance_km": 50}
      ]
    }
  },
  "privacy": {"retention": {"likes": "60d"}}
}`

const shopConfig = `{
  "recoant": 1,
  "half_life": "30d",
  "kinds": {
    "user": {
      "attributes": {
        "min_price": {"type": "number"},
        "max_price": {"type": "number"},
        "tags": {"type": "set"},
        "location": {"type": "geo", "sensitive": true}
      }
    },
    "product": {
      "attributes": {
        "category": {"type": "category"},
        "brand": {"type": "category"},
        "price": {"type": "number"},
        "tags": {"type": "set"},
        "store": {"type": "geo"}
      }
    }
  },
  "relations": {"interacts": {"subject": "user", "object": "product"}},
  "signals": {
    "visit": {"relation": "interacts", "weight": 1},
    "click": {"relation": "interacts", "weight": 2},
    "dwell": {"relation": "interacts", "weight": 3, "transform": {"kind": "saturate", "param": 60}},
    "cart": {"relation": "interacts", "weight": 5},
    "purchase": {"relation": "interacts", "weight": 10}
  },
  "models": {
    "main": {"algorithm": "als", "relation": "interacts", "params": {"factors": 32, "seed": 1}},
    "trending": {"algorithm": "popularity", "params": {"half_life": "7d"}}
  },
  "serving": {
    "all": {"model": "main", "exclude_seen": true, "fallback": "trending", "target": "product"},
    "budget": {"model": "main", "exclude_seen": true, "fallback": "trending", "target": "product",
      "filters": [{"attribute": "price", "within": ["min_price", "max_price"]}]},
    "budget_tags": {"model": "main", "exclude_seen": true, "fallback": "trending", "target": "product",
      "filters": [{"attribute": "price", "within": ["min_price", "max_price"]}, {"attribute": "tags", "same": true}]}
  }
}`

type task struct {
	set      string
	config   string
	target   recoant.Kind
	relevant []recoant.SignalType
	span     time.Duration
	// sensitive lists attributes that must not be exported.
	sensitive []string
}

var tasks = []task{
	{"attributes/people", peopleConfig, recoant.User, []recoant.SignalType{"like", "superlike"}, 120 * 24 * time.Hour, []string{"age", "gender", "location"}},
	{"attributes/shop", shopConfig, "product", []recoant.SignalType{"cart", "purchase"}, 90 * 24 * time.Hour, []string{"location"}},
}

var start = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// violations counts broken rules; any is a failure.
type violations struct {
	mu   sync.Mutex
	list []string
}

func (v *violations) add(format string, args ...any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.list) < 20 {
		v.list = append(v.list, fmt.Sprintf(format, args...))
	} else if len(v.list) == 20 {
		v.list = append(v.list, "…")
	}
}

func main() {
	data := flag.String("data", "../recoant-data/synthetic", "folder of the recoant-data synthetic module")
	k := flag.Int("k", 10, "number of recommendations")
	flag.Parse()
	ctx := context.Background()
	bad := &violations{}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	_, _ = fmt.Fprintf(w, "data set\tserving\tusers\tprecision@%d\trecall@%d\tndcg@%d\thit rate\tcoverage\t\n", *k, *k, *k)
	for _, tk := range tasks {
		if err := run(ctx, w, filepath.Join(*data, tk.set), tk, *k, bad); err != nil {
			log.Fatalf("%s: %v", tk.set, err)
		}
	}
	_ = w.Flush()
	if len(bad.list) > 0 {
		fmt.Println("\nBROKEN RULES:")
		for _, v := range bad.list {
			fmt.Println("  " + v)
		}
		os.Exit(1)
	}
	fmt.Println("\nAll rules hold: no deleted reference recommended, every filtered recommendation meets its filters, retention dropped exactly the expected pairs, sensitive attributes not exported.")
}

func run(ctx context.Context, w io.Writer, dir string, tk task, k int, bad *violations) error {
	c, err := config.Load(strings.NewReader(tk.config))
	if err != nil {
		return err
	}
	e, err := c.Build()
	if err != nil {
		return err
	}
	schema := e.Attributes().Schema()
	events, err := loadEvents(filepath.Join(dir, "attributes.csv"), schema)
	if err != nil {
		return err
	}
	sigs, err := loadSignals(filepath.Join(dir, "signals.csv"))
	if err != nil {
		return err
	}
	split := start.Add(tk.span * 8 / 10)
	train, test := evaluation.SplitByTime(sigs, split)

	// Replay attribute events and signals in time order up to the split.
	deleted := map[recoant.Ref]bool{}
	last := map[[2]recoant.Ref]time.Time{} // last signal per pair, for retention
	next := 0
	apply := func(until time.Time) error {
		for ; next < len(events) && !events[next].t.After(until); next++ {
			ev := events[next].ev
			if ev.Op == attr.OpDelete && ev.Attribute == "" {
				if !e.Delete(ev.Ref) {
					bad.add("%s: Delete(%v) found nothing", tk.set, ev.Ref)
				}
				deleted[ev.Ref] = true
				continue
			}
			if err := e.Apply(ev); err != nil {
				return err
			}
		}
		return nil
	}
	for _, s := range train {
		if err := apply(s.Time); err != nil {
			return err
		}
		if err := e.Add(s); err != nil {
			return err
		}
		last[[2]recoant.Ref{s.Subject, s.Object}] = s.Time
	}
	if err := apply(split); err != nil {
		return err
	}

	// Retention: exactly the pairs of live references whose last signal is
	// older than the retention are dropped.
	if ret, ok := retention(c, "likes"); ok {
		want := 0
		for p, t := range last {
			if !deleted[p[0]] && !deleted[p[1]] && t.Before(split.Add(-time.Duration(ret))) {
				want++
			}
		}
		if got := e.Expire(split); got != want {
			bad.add("%s: Expire dropped %d pairs, want %d", tk.set, got, want)
		} else {
			_, _ = fmt.Fprintf(os.Stderr, "%s: retention dropped %d of %d pairs\n", tk.set, got, len(last))
		}
	}
	if err := e.Fit(ctx); err != nil {
		return err
	}

	// Sensitive attributes are not exported.
	exported := 0
	for _, ev := range events[:next] {
		if deleted[ev.ev.Ref] {
			continue
		}
		a := e.ExportAttributes(ev.ev.Ref)
		exported += len(a)
		for _, name := range tk.sensitive {
			if _, ok := a[name]; ok {
				bad.add("%s: sensitive attribute %s exported for %v", tk.set, name, ev.ev.Ref)
			}
		}
	}
	if exported == 0 {
		bad.add("%s: nothing exported", tk.set)
	}

	// Test subjects: live at the split.
	var live []recoant.Signal
	for _, s := range test {
		if !deleted[s.Subject] && !deleted[s.Object] {
			live = append(live, s)
		}
	}
	for _, serving := range sortedServing(c) {
		r := &checked{e: e, serving: serving, filters: c.Serving[serving].Filters, deleted: deleted, bad: bad, set: tk.set}
		rep, err := evaluation.Evaluate(ctx, r, live, evaluation.Config{
			K: k, Target: tk.target, Relevant: tk.relevant, ExcludeSeen: true, Catalog: e.Store().Len(tk.target), Workers: 8,
		})
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%.4f\t%.4f\t%.4f\t%.4f\t%.3f\t\n", tk.set, serving, rep.Subjects, rep.Precision, rep.Recall, rep.NDCG, rep.HitRate, rep.Coverage)
	}
	return nil
}

func retention(c *config.Config, relation string) (config.Duration, bool) {
	if c.Privacy == nil {
		return 0, false
	}
	d, ok := c.Privacy.Retention[relation]
	return d, ok
}

func sortedServing(c *config.Config) []string {
	var names []string
	for n := range c.Serving {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// checked answers requests with a serving declaration of the engine and
// checks every result against the rules.
type checked struct {
	e       *config.Engine
	serving string
	filters []recoant.Condition
	deleted map[recoant.Ref]bool
	bad     *violations
	set     string
}

func (c *checked) Recommend(ctx context.Context, req recoant.Request) ([]recoant.Scored, error) {
	res, err := c.e.Recommend(ctx, c.serving, req.Subject, req.K)
	if err != nil {
		return nil, err
	}
	a := c.e.Attributes()
	for _, r := range res {
		if c.deleted[r.Ref] {
			c.bad.add("%s %s: deleted %v recommended to %v", c.set, c.serving, r.Ref, req.Subject)
		}
		for _, f := range c.filters {
			if !meets(a, req.Subject, r.Ref, f) {
				c.bad.add("%s %s: %v recommended to %v breaks filter on %s", c.set, c.serving, r.Ref, req.Subject, f.Attribute)
			}
		}
	}
	return res, nil
}

// meets checks a filter independently of attr.Compile, from the stored
// values; conditions on missing subject values are skipped like in recoant.
func meets(a *attr.Store, subject, cand recoant.Ref, f recoant.Condition) bool {
	get := func(ref recoant.Ref, name string) (attr.Value, bool) { return a.Get(ref, name) }
	cv, ok := get(cand, f.Attribute)
	switch f.Test() {
	case "within":
		lo, ok1 := get(subject, f.Within[0])
		hi, ok2 := get(subject, f.Within[1])
		if ok1 && ok2 && (!ok || cv.Number < lo.Number || cv.Number > hi.Number) {
			return false
		}
		if f.Mutual {
			sv, ok0 := get(subject, f.Attribute)
			clo, ok1 := get(cand, f.Within[0])
			chi, ok2 := get(cand, f.Within[1])
			if ok0 && ok1 && ok2 && (sv.Number < clo.Number || sv.Number > chi.Number) {
				return false
			}
		}
		return true
	case "distance":
		sv, ok0 := get(subject, f.Attribute)
		return !ok0 || (ok && attr.DistanceKm(sv.Geo, cv.Geo) <= f.MaxDistanceKm)
	case "same":
		sv, ok0 := get(subject, f.Attribute)
		return !ok0 || (ok && slices.ContainsFunc(sv.Set, func(x string) bool { return slices.Contains(cv.Set, x) }))
	}
	return true
}

type event struct {
	t  time.Time
	ev attr.Event
}

// loadEvents reads an attributes.csv file and parses the values with the
// schema.
func loadEvents(path string, schema attr.Schema) ([]event, error) {
	var out []event
	err := readCSV(path, func(rec []string) error {
		t, err := time.Parse(time.RFC3339, rec[0])
		if err != nil {
			return err
		}
		ev := attr.Event{Ref: recoant.Ref{Kind: recoant.Kind(rec[1]), ID: rec[2]}, Op: attr.Op(rec[3]), Attribute: rec[4]}
		if ev.Op == attr.OpSet {
			spec, ok := schema.Spec(ev.Ref.Kind, ev.Attribute)
			if !ok {
				return fmt.Errorf("unknown attribute %s.%s", ev.Ref.Kind, ev.Attribute)
			}
			if ev.Value, err = parseValue(spec.Type, rec[5]); err != nil {
				return fmt.Errorf("%s.%s: %w", ev.Ref.Kind, ev.Attribute, err)
			}
		}
		out = append(out, event{t, ev})
		return nil
	})
	return out, err
}

func parseValue(t attr.Type, s string) (attr.Value, error) {
	switch t {
	case attr.Number:
		v, err := strconv.ParseFloat(s, 64)
		return attr.Num(v), err
	case attr.Set:
		if s == "" {
			return attr.Tags(), nil
		}
		return attr.Tags(strings.Split(s, "|")...), nil
	case attr.Geo:
		a, b, _ := strings.Cut(s, ",")
		lat, err1 := strconv.ParseFloat(a, 64)
		lon, err2 := strconv.ParseFloat(b, 64)
		return attr.At(lat, lon), errors.Join(err1, err2)
	}
	return attr.Cat(s), nil
}

// loadSignals reads a recoant-data signals file.
func loadSignals(path string) ([]recoant.Signal, error) {
	var out []recoant.Signal
	err := readCSV(path, func(rec []string) error {
		t, err := time.Parse(time.RFC3339, rec[0])
		if err != nil {
			return err
		}
		v, err := strconv.ParseFloat(rec[6], 64)
		if err != nil {
			return err
		}
		out = append(out, recoant.Signal{
			Time:    t,
			Subject: recoant.Ref{Kind: recoant.Kind(rec[1]), ID: rec[2]},
			Object:  recoant.Ref{Kind: recoant.Kind(rec[3]), ID: rec[4]},
			Type:    recoant.SignalType(rec[5]), Value: v, Session: rec[7],
		})
		return nil
	})
	return out, err
}

func readCSV(path string, fn func([]string) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	r := csv.NewReader(bufio.NewReaderSize(f, 1<<20))
	if _, err := r.Read(); err != nil { // header
		return err
	}
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(rec); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
}
