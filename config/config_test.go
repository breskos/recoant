package config_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/breskos/recoant"
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
		"privacy":       {`{"recoant": 1, "privacy": {}, ` + base + `}`, "privacy: supported from v0.5"},
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
		"no models":      {`{"recoant": 1, "kinds": {"user": {}}}`, "models: at least one"},
		"algorithm":      {`{"recoant": 1, "kinds": {"user": {}}, "models": {"m": {"algorithm": "magic"}}}`, "models.m.algorithm"},
		"model relation": {`{"recoant": 1, "kinds": {"user": {}}, "models": {"m": {"algorithm": "als", "relation": "x"}}}`, "models.m.relation"},
		"serving model":  {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "x"}}}`, "serving.s.model"},
		"fallback":       {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "fallback": "x"}}}`, "serving.s.fallback"},
		"target":         {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "target": "x"}}}`, "serving.s.target"},
		"k":              {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "k": -1}}}`, "serving.s.k"},
		"filters":        {`{"recoant": 1, ` + base + `, "serving": {"s": {"model": "m", "filters": []}}}`, "serving.s.filters: supported from v0.5"},
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
