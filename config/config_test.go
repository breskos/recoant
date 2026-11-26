package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/internal/testfix"
)

var ctx = context.Background()

func TestExamples(t *testing.T) {
	for _, f := range []string{"../examples/config/shop.json", "../examples/config/people.json"} {
		c, err := config.LoadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Build(); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := config.LoadFile("nope.json"); err == nil {
		t.Error("missing file")
	}
}

// shop loads the shop example with products named like the fixtures.
func shop(t *testing.T) *config.Engine {
	t.Helper()
	c, err := config.LoadFile("../examples/config/shop.json")
	if err != nil {
		t.Fatal(err)
	}
	m := c.Models["main"]
	m.Params = []byte(`{"factors": 4, "seed": 1}`)
	c.Models["main"] = m
	e, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.AddBatch(ctx, testfix.Hidden()); err != nil {
		t.Fatal(err)
	}
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEngine(t *testing.T) {
	e := shop(t)
	res, err := e.Recommend(ctx, "for_you", recoant.U("u4"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := testfix.IDs(res); len(got) != 1 || got[0] != "z" {
		t.Errorf("for_you u4 = %v", got)
	}
	res, _ = e.Recommend(ctx, "for_you", recoant.U("u1"), 6)
	for _, r := range res {
		if r.Ref.ID == "c" || r.Ref.ID == "a" || r.Ref.ID == "b" {
			t.Errorf("u1 got seen or hidden %v", testfix.IDs(res))
		}
	}
	// A new user gets the fallback (trending).
	res, _ = e.Recommend(ctx, "for_you", recoant.U("newbie"), 3)
	if len(res) != 3 {
		t.Errorf("fallback = %v", res)
	}
	res, _ = e.Recommend(ctx, "similar", testfix.P("x"), 0)
	if got := testfix.IDs(res); len(got) == 0 || (got[0] != "y" && got[0] != "z") {
		t.Errorf("similar to x = %v", got)
	}
	req, _ := e.Request("for_you", recoant.U("u1"), 0)
	if req.K != 20 || req.Target != "product" || !req.ExcludeSeen {
		t.Errorf("request = %+v", req)
	}
	req, _ = e.Request("top", recoant.U("u1"), 0)
	if req.K != 10 {
		t.Errorf("default k = %d", req.K)
	}
	if _, err := e.Recommend(ctx, "nope", recoant.U("u1"), 1); !errors.Is(err, config.ErrUnknown) {
		t.Errorf("unknown serving: %v", err)
	}
	if _, ok := e.Model("main"); !ok || e.Store() == nil || e.Config() == nil {
		t.Error("accessors")
	}
	_ = e.Add(testfix.Sig("u7", "x", "purchase", 1))
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Add(recoant.Signal{}); err == nil {
		t.Error("invalid signal accepted")
	}
	if err := e.AddBatch(ctx, []recoant.Signal{{}}); err == nil {
		t.Error("invalid batch accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := e.AddBatch(canceled, testfix.Groups); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled AddBatch: %v", err)
	}
	if err := e.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Fit: %v", err)
	}
	_ = e.Add(testfix.Sig("u8", "x", "purchase", 1))
	if err := e.Refresh(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled Refresh: %v", err)
	}
}

// The models of a relation without signals are skipped by Fit.
func TestEmptyEngine(t *testing.T) {
	c, _ := config.LoadFile("../examples/config/people.json")
	e, _ := c.Build()
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Add(recoant.Signal{Subject: recoant.U("a"), Object: recoant.Ref{Kind: "product", ID: "p"}, Type: "like", Value: 1, Time: testfix.T0}); err == nil {
		t.Error("like on a product accepted")
	}
}

const base = `"kinds": {"user": {}, "product": {}},
  "relations": {"r": {"subject": "user", "object": "product"}},
  "signals": {"visit": {"relation": "r"}},
  "models": {"m": {"algorithm": "als", "relation": "r"}}`

func TestValidate(t *testing.T) {
	cases := map[string]struct{ json, want string }{
		"version":       {`{"recoant": 2, ` + base + `}`, "recoant: version"},
		"unknown field": {`{"recoant": 1, "colour": 1, ` + base + `}`, "unknown field"},
		"matching":      {`{"recoant": 1, "matching": {}, ` + base + `}`, "matching: supported from v0.6"},
		"segments":      {`{"recoant": 1, "segments": {}, ` + base + `}`, "segments: supported from v0.7"},
		"retention":     {`{"recoant": 1, "privacy": {"retention": {"x": "1d"}}, ` + base + `}`, "privacy.retention.x: unknown relation"},
		"no kinds":      {`{"recoant": 1}`, "kinds: at least one"},
		"attribute":     {`{"recoant": 1, "kinds": {"user": {"attributes": {"age": {"type": "int"}}}}}`, "kinds.user.attributes.age.type"},
		"rel subject":   {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "x", "object": "user"}}}`, "relations.r.subject"},
		"rel object":    {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "user", "object": "x"}}}`, "relations.r.object"},
		"aggregate": {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "user", "object": "user",
			"channels": {"positive": {"aggregate": "median"}}}}}`, "relations.r.channels.positive.aggregate"},
		"signal relation": {`{"recoant": 1, "kinds": {"user": {}}, "signals": {"s": {"relation": "x"}}}`, "signals.s.relation"},
		"feedback": {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "user", "object": "user"}},
			"signals": {"s": {"relation": "r", "feedback": "meh"}}}`, "signals.s.feedback"},
		"scale": {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "user", "object": "user"}},
			"signals": {"s": {"relation": "r", "feedback": "rating", "scale": [5, 1]}}}`, "signals.s.scale"},
		"no models":        {`{"recoant": 1, "kinds": {"user": {}}}`, "models: at least one"},
		"algorithm":        {`{"recoant": 1, "kinds": {"user": {}}, "models": {"m": {"algorithm": "magic"}}}`, "models.m.algorithm"},
		"model relation":   {`{"recoant": 1, "kinds": {"user": {}}, "models": {"m": {"algorithm": "als", "relation": "x"}}}`, "models.m.relation"},
		"serving model":    {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "x"}}}`, "serving.s.model"},
		"fallback":         {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "fallback": "x"}}}`, "serving.s.fallback"},
		"target":           {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "target": "x"}}}`, "serving.s.target"},
		"k":                {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "k": -1}}}`, "serving.s.k"},
		"filter attribute": {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "filters": [{"attribute": "price", "min": 1}]}}}`, "serving.s.filters[0]: attr: invalid condition"},
		"filter test":      {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "filters": [{"attribute": "price"}]}}}`, "serving.s.filters[0]"},
		"filter field":     {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "filters": [{"attribute": "price", "above": 1}]}}}`, "unknown field"},
		"serving subject":  {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "subject": "x"}}}`, "serving.s.subject: unknown kind"},
		"needs subject": {`{"recoant": 1, "kinds": {"user": {"attributes": {"city": {"type": "category"}}}}, "models": {"p": {"algorithm": "popularity"}},
			"serving": {"s": {"model": "p", "target": "user", "filters": [{"attribute": "city", "same": true}]}}}`, "serving.s.subject: needed"},
		"popularity target": {`{"recoant": 1, "kinds": {"user": {}}, "models": {"p": {"algorithm": "popularity"}},
			"serving": {"s": {"model": "p"}}}`, "serving.s.target: needed"},
		"transform": {`{"recoant": 1, "kinds": {"user": {}}, "relations": {"r": {"subject": "user", "object": "user"}},
			"signals": {"s": {"relation": "r", "transform": {"kind": "sqrt"}}}, "models": {"p": {"algorithm": "popularity"}}}`, "signals:"},
	}
	for name, c := range cases {
		_, err := config.Load(strings.NewReader(c.json))
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// Deleted products are never recommended by trained models, even before
// the next Fit.
func TestDeleteFromFactorModel(t *testing.T) {
	e := shop(t)
	res, _ := e.Recommend(ctx, "for_you", recoant.U("u4"), 1)
	if got := testfix.IDs(res); len(got) != 1 || got[0] != "z" {
		t.Fatalf("for_you u4 = %v", got)
	}
	if !e.Delete(testfix.P("z")) {
		t.Fatal("Delete")
	}
	for _, serving := range []string{"for_you", "similar", "top"} {
		subject := recoant.U("u4")
		if serving == "similar" {
			subject = testfix.P("x")
		}
		res, err := e.Recommend(ctx, serving, subject, 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if r.Ref.ID == "z" {
				t.Errorf("%s recommends a deleted product", serving)
			}
		}
	}
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFiltersAndPrivacy(t *testing.T) {
	c, err := config.LoadFile("../examples/config/people.json")
	if err != nil {
		t.Fatal(err)
	}
	m := c.Models["attraction"]
	m.Params = []byte(`{"factors": 2, "seed": 1, "iterations": 5}`)
	c.Models["attraction"] = m
	e, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	// Berlin: ann 30 (wants 28–40), bob 35 (wants 25–32), cem 33 (wants 35–45);
	// Munich: dan 34 (wants 25–35).
	for _, u := range []struct {
		id                  string
		age, minAge, maxAge float64
		lat, lon            float64
	}{
		{"ann", 30, 28, 40, 52.52, 13.40},
		{"bob", 35, 25, 32, 52.50, 13.45},
		{"cem", 33, 35, 45, 52.51, 13.39},
		{"dan", 34, 25, 35, 48.14, 11.58},
	} {
		for name, v := range map[string]attr.Value{
			"age": attr.Num(u.age), "min_age": attr.Num(u.minAge), "max_age": attr.Num(u.maxAge),
			"location": attr.At(u.lat, u.lon), "interests": attr.Tags("jazz"),
		} {
			if err := e.Apply(attr.Event{Ref: recoant.U(u.id), Attribute: name, Value: v}); err != nil {
				t.Fatal(err)
			}
		}
	}
	like := func(from, to string, at time.Duration) recoant.Signal {
		return recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: "like", Value: 1, Time: testfix.T0.Add(at)}
	}
	// Everybody is liked by eve, so popularity knows them all.
	for _, to := range []string{"bob", "cem", "dan", "ann"} {
		if err := e.Add(like("eve", to, 0)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	// ann has no likes of her own, so the fallback (popularity) answers,
	// filtered: bob is the only mutual match within 50 km.
	res, err := e.Recommend(ctx, "discover", recoant.U("ann"), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := testfix.IDs(res); len(got) != 1 || got[0] != "bob" {
		t.Errorf("discover ann = %v", got)
	}
	// Extra conditions per request.
	res, _ = e.Recommend(ctx, "discover", recoant.U("ann"), 10, recoant.Condition{Attribute: "interests", In: []string{"chess"}})
	if len(res) != 0 {
		t.Errorf("extra condition ignored: %v", res)
	}
	if _, err := e.Recommend(ctx, "discover", recoant.U("ann"), 10, recoant.Condition{Attribute: "nope", Same: true}); err == nil {
		t.Error("invalid extra condition accepted")
	}
	req, _ := e.Request("discover", recoant.U("ann"), 0)
	if len(req.Where) != 2 {
		t.Errorf("request filters = %v", req.Where)
	}

	// Sensitive attributes are not exported.
	if a := e.ExportAttributes(recoant.U("ann")); len(a) != 3 {
		t.Errorf("export = %v", a)
	}

	// Delete removes signals, popularity and attributes.
	if !e.Delete(recoant.U("bob")) || e.Delete(recoant.U("bob")) {
		t.Fatal("Delete")
	}
	if e.Store().Has(recoant.U("eve"), recoant.U("bob")) || e.Attributes().Count(recoant.User, "age") != 3 {
		t.Error("bob's data remains")
	}
	res, _ = e.Recommend(ctx, "discover", recoant.U("ann"), 10)
	if len(res) != 0 {
		t.Errorf("deleted user recommended: %v", res)
	}

	// Retention: likes are kept for 365 days after the last signal.
	if err := e.Add(like("eve", "ann", 400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := e.Expire(time.Time{}); n != 2 { // eve→cem, eve→dan
		t.Errorf("Expire = %d", n)
	}
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	// Keeping attributes on delete, exporting sensitive ones.
	keep := false
	c.Privacy = &config.Privacy{DeleteAttributesWithEntity: &keep, ExportSensitive: true}
	e, err = c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(attr.Event{Ref: recoant.U("ann"), Attribute: "age", Value: attr.Num(30)}); err != nil {
		t.Fatal(err)
	}
	if err := e.Add(like("ann", "eve", 0)); err != nil {
		t.Fatal(err)
	}
	if !e.Delete(recoant.U("ann")) || e.Attributes().Count(recoant.User, "age") != 1 {
		t.Error("attributes deleted although disabled")
	}
	if a := e.ExportAttributes(recoant.U("ann")); len(a) != 1 {
		t.Errorf("export sensitive = %v", a)
	}
}

func TestParams(t *testing.T) {
	for _, alg := range []string{"popularity", "item-knn", "user-knn", "pure-svd", "mf", "funk-svd", "biased-mf", "svd++", "als", "bpr"} {
		good := `{"recoant": 1, ` + strings.Replace(base, `"als"`, `"`+alg+`"`, 1) + `}`
		c, err := config.Load(strings.NewReader(good))
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if _, err := c.Build(); err != nil {
			t.Errorf("%s: %v", alg, err)
		}
		for _, params := range []string{`{"bogus": 1}`, `{"half_life": "-1h"}`} {
			m := c.Models["m"]
			m.Params = []byte(params)
			c.Models["m"] = m
			if _, err := c.Build(); !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "models.m.params") {
				t.Errorf("%s %s: %v", alg, params, err)
			}
		}
	}
	// Invalid values that decode but do not validate.
	for alg, params := range map[string]string{
		"popularity": `{}`, "item-knn": `{"neighbors": -1}`, "user-knn": `{"neighbors": -1}`, "pure-svd": `{"factors": -1}`,
		"mf": `{"factors": -1}`, "als": `{"factors": -1}`, "bpr": `{"factors": -1}`,
	} {
		c, _ := config.Load(strings.NewReader(`{"recoant": 1, ` + strings.Replace(base, `"als"`, `"`+alg+`"`, 1) + `}`))
		m := c.Models["m"]
		m.Params = []byte(params)
		c.Models["m"] = m
		_, err := c.Build()
		if alg == "popularity" {
			if err != nil {
				t.Errorf("popularity: %v", err)
			}
			continue
		}
		if !errors.Is(err, config.ErrInvalid) {
			t.Errorf("%s %s: %v", alg, params, err)
		}
	}
	c := &config.Config{}
	if _, err := c.Build(); err == nil {
		t.Error("empty config built")
	}
}

func TestDuration(t *testing.T) {
	for s, want := range map[string]time.Duration{"90s": 90 * time.Second, "30d": 720 * time.Hour, "1.5d": 36 * time.Hour} {
		if got, err := config.ParseDuration(s); err != nil || got != want {
			t.Errorf("%s = %v %v", s, got, err)
		}
	}
	for _, s := range []string{"x", "-1h", "-2d", "d"} {
		if _, err := config.ParseDuration(s); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	var d config.Duration
	if err := d.UnmarshalJSON([]byte(`3600`)); err == nil {
		t.Error("number accepted")
	}
	if err := d.UnmarshalJSON([]byte(`"2h"`)); err != nil || time.Duration(d) != 2*time.Hour {
		t.Errorf("unmarshal %v %v", d, err)
	}
	if b, _ := d.MarshalJSON(); string(b) != `"2h0m0s"` {
		t.Errorf("marshal %s", b)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("disk on fire") }

func TestLoadErrors(t *testing.T) {
	if _, err := config.Load(failingReader{}); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Errorf("read error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte(`{"recoant": 2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadFile(path); !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), path) {
		t.Errorf("invalid file: %v", err)
	}
	// An empty attribute name passes the JSON schema but not the attribute store.
	c, err := config.Load(strings.NewReader(`{"recoant": 1, "kinds": {"user": {"attributes": {"": {"type": "number"}}}, "product": {}},
		"relations": {"r": {"subject": "user", "object": "product"}}, "signals": {"visit": {"relation": "r"}},
		"models": {"m": {"algorithm": "als", "relation": "r"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Build(); !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), "kinds") {
		t.Errorf("empty attribute name: %v", err)
	}
}
