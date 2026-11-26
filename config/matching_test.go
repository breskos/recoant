package config_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/internal/testfix"
)

const people = `"kinds": {"user": {}, "job": {}},
  "relations": {
    "likes": {"subject": "user", "object": "user"},
    "applies": {"subject": "user", "object": "job"},
    "recruits": {"subject": "job", "object": "user"}
  },
  "signals": {
    "like": {"relation": "likes"}, "pass": {"relation": "likes", "feedback": "negative"},
    "apply": {"relation": "applies"}, "shortlist": {"relation": "recruits"}
  }`

func TestValidateMatching(t *testing.T) {
	models := func(m string) string { return `, "models": {` + m + `}` }
	als := `"m": {"algorithm": "als", "relation": "likes"}`
	cases := map[string]struct{ json, want string }{
		"relation": {`"matching": {"x": {"mode": "reciprocal"}}` + models(als), "matching.x: unknown relation"},
		"mode":     {`"matching": {"likes": {"mode": "both"}}` + models(als), "matching.likes.mode: must be one_way or reciprocal"},
		"combine":  {`"matching": {"likes": {"mode": "reciprocal", "combine": "mean"}}` + models(als), "matching.likes: match: invalid configuration: combine"},
		"knn": {`"matching": {"likes": {"mode": "reciprocal"}}` + models(`"m": {"algorithm": "user-knn", "relation": "likes"}`),
			"models.m.algorithm: user-knn cannot score the reverse direction"},
		"kinds": {`"matching": {"applies": {"mode": "reciprocal"}}` + models(`"m": {"algorithm": "als", "relation": "applies"}`),
			"matching.applies.reverse_model: needed"},
		"reverse unknown": {`"matching": {"applies": {"mode": "reciprocal", "reverse_model": "r"}}` + models(`"m": {"algorithm": "als", "relation": "applies"}`),
			`matching.applies.reverse_model: unknown model "r"`},
		"reverse algorithm": {`"matching": {"applies": {"mode": "reciprocal", "reverse_model": "r"}}` + models(`"m": {"algorithm": "als", "relation": "applies"}, "r": {"algorithm": "user-knn", "relation": "recruits"}`),
			"matching.applies.reverse_model: must be a factor model"},
		"reverse relation": {`"matching": {"applies": {"mode": "reciprocal", "reverse_model": "r"}}` + models(`"m": {"algorithm": "als", "relation": "applies"}, "r": {"algorithm": "als", "relation": "likes"}`),
			"matching.applies.reverse_model: must learn a relation job → user"},
		"shared algorithm": {models(`"m": {"algorithm": "bpr", "relation": "likes", "shared": ["applies"]}`), "models.m.shared: only als"},
		"shared unknown":   {models(`"m": {"algorithm": "als", "relation": "likes", "shared": ["x"]}`), `models.m.shared[0]: unknown relation "x"`},
		"shared twice":     {models(`"m": {"algorithm": "als", "relation": "likes", "shared": ["applies", "applies"]}`), "models.m.shared[1]: relation \"applies\" is listed twice"},
		"shared kind":      {models(`"m": {"algorithm": "als", "relation": "likes", "shared": ["recruits"]}`), `models.m.shared[0]: relation "recruits" has subject kind "job"`},
	}
	for name, c := range cases {
		_, err := config.Load(strings.NewReader(`{"recoant": 1, ` + people + `, ` + strings.TrimPrefix(c.json, ", ") + `}`))
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	one := `{"recoant": 1, ` + people + `, "matching": {"likes": {"mode": "one_way"}}, "models": {` + als + `}}`
	if _, err := config.Load(strings.NewReader(one)); err != nil {
		t.Errorf("one_way: %v", err)
	}
}

func sig(from, to recoant.Ref, typ string, minute int) recoant.Signal {
	return recoant.Signal{Subject: from, Object: to, Type: recoant.SignalType(typ), Value: 1, Time: testfix.T0.Add(time.Duration(minute) * time.Minute)}
}

func job(id string) recoant.Ref { return recoant.Ref{Kind: "job", ID: id} }

// A reciprocal engine for people and one for candidates and jobs, with
// shared vectors: everything from the configuration.
func TestReciprocalEngine(t *testing.T) {
	cfg := `{"recoant": 1, ` + people + `,
	  "models": {
	    "people": {"algorithm": "als", "relation": "likes", "shared": ["applies"], "params": {"factors": 4, "seed": 1, "iterations": 15}},
	    "jobs": {"algorithm": "als", "relation": "applies", "params": {"factors": 4, "seed": 1}},
	    "talent": {"algorithm": "als", "relation": "recruits", "params": {"factors": 4, "seed": 1}},
	    "popular": {"algorithm": "popularity"}
	  },
	  "matching": {
	    "likes": {"mode": "reciprocal", "min_each": 0.2, "exposure_balancing": 0.3},
	    "applies": {"mode": "reciprocal", "reverse_model": "talent", "combine": "geometric"}
	  },
	  "serving": {
	    "match": {"model": "people", "exclude_seen": true, "fallback": "popular", "k": 2},
	    "jobs": {"model": "jobs", "exclude_seen": true, "k": 2}
	  }}`
	c, err := config.Load(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Fit(ctx); err != nil { // no signals yet
		t.Fatal(err)
	}
	var sigs []recoant.Signal
	n := 0
	a := []string{"a1", "a2", "a3", "a4", "a5"}
	for _, u := range a {
		for _, v := range a {
			if u != v && (u != "a1" || v != "a2") {
				sigs = append(sigs, sig(recoant.U(u), recoant.U(v), "like", n))
				n++
			}
		}
		if u != "a1" {
			sigs = append(sigs, sig(recoant.U(u), recoant.U("s"), "like", n))
		}
	}
	for _, u := range []string{"b1", "b2", "b3"} {
		sigs = append(sigs, sig(recoant.U("s"), recoant.U(u), "like", n), sig(recoant.U(u), recoant.U("s"), "like", n))
	}
	// Jobs: j1 shortlists a1, j2 does not want a1.
	sigs = append(sigs,
		sig(recoant.U("a2"), job("j1"), "apply", n), sig(recoant.U("a2"), job("j2"), "apply", n), sig(recoant.U("a3"), job("j1"), "apply", n),
		sig(recoant.U("a3"), job("j2"), "apply", n), sig(recoant.U("a1"), job("j3"), "apply", n), sig(recoant.U("a2"), job("j3"), "apply", n), sig(recoant.U("a3"), job("j3"), "apply", n),
		sig(job("j1"), recoant.U("a1"), "shortlist", n), sig(job("j1"), recoant.U("a2"), "shortlist", n), sig(job("j2"), recoant.U("b1"), "shortlist", n))
	if err := e.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := e.Recommend(ctx, "match", recoant.U("a1"), 0)
	if got := testfix.IDs(res); err != nil || !slices.Equal(got, []string{"a2"}) {
		t.Errorf("match for a1: %v %v, want [a2]", got, err)
	}
	res, err = e.Recommend(ctx, "jobs", recoant.U("a1"), 0)
	if got := testfix.IDs(res); err != nil || len(got) == 0 || got[0] != "j1" {
		t.Errorf("jobs for a1: %v %v, want j1 first", got, err)
	}
	// A new member has no vector: the fallback answers.
	res, err = e.Recommend(ctx, "match", recoant.U("new"), 0)
	if err != nil || len(res) == 0 {
		t.Errorf("new member: %v %v", res, err)
	}
}
