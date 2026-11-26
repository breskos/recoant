package segment_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/algo/als"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/internal/testfix"
	"github.com/breskos/recoant/segment"
	"github.com/breskos/recoant/store"
)

var ctx = context.Background()

func TestKMeans(t *testing.T) {
	// Two clear groups around (0, 0) and (10, 10).
	var pts [][]float64
	for i := range 20 {
		d := float64(i%5) * 0.1
		pts = append(pts, []float64{d, -d}, []float64{10 + d, 10 - d})
	}
	for _, cfg := range []segment.KMeansConfig{{K: 2, Seed: 1}, {K: 2, Seed: 3, BatchSize: 8}} {
		c, err := segment.KMeans(ctx, pts, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < len(pts); i += 2 {
			if c.Assign[i] != c.Assign[0] || c.Assign[i+1] == c.Assign[0] {
				t.Fatalf("%+v: assignment %v", cfg, c.Assign)
			}
		}
		if len(c.Centers) != 2 || c.Inertia > 2 {
			t.Errorf("%+v: centers %v inertia %v", cfg, c.Centers, c.Inertia)
		}
	}
	// Fewer distinct points than k.
	c, err := segment.KMeans(ctx, [][]float64{{1}, {1}, {1}}, segment.KMeansConfig{K: 3})
	if err != nil || len(c.Centers) != 1 {
		t.Errorf("one distinct point: %v %v", c.Centers, err)
	}
	if c, err := segment.KMeans(ctx, nil, segment.KMeansConfig{K: 3}); err != nil || c.Centers != nil {
		t.Errorf("no points: %v %v", c, err)
	}
	if _, err := segment.KMeans(ctx, pts, segment.KMeansConfig{}); !errors.Is(err, segment.ErrConfig) {
		t.Errorf("k 0: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, cfg := range []segment.KMeansConfig{{K: 2}, {K: 2, BatchSize: 4}} {
		if _, err := segment.KMeans(canceled, pts, cfg); !errors.Is(err, context.Canceled) {
			t.Errorf("canceled %+v: %v", cfg, err)
		}
	}
}

// An empty cluster moves to the farthest point: three points, k 3, where
// the seeds can coincide in value.
func TestKMeansEmptyCluster(t *testing.T) {
	pts := [][]float64{{0}, {0}, {0}, {0}, {1}, {100}}
	for seed := range uint64(20) {
		c, err := segment.KMeans(ctx, pts, segment.KMeansConfig{K: 3, Seed: seed})
		if err != nil || len(c.Centers) != 3 {
			t.Fatalf("seed %d: %v %v", seed, c, err)
		}
	}
}

// fixture: Groups (u1–u3 like a, b, c; u4–u6 like x, y, z), u3 hides z,
// users have a city (group A Berlin, group B Hamburg), an age, tags and a
// sensitive location.
func fixture(t *testing.T) (*store.Store, *attr.Store, *als.ALS) {
	t.Helper()
	sigs := append([]recoant.Signal(nil), testfix.Groups...)
	sigs = append(sigs, testfix.Sig("u3", "z", "hide", 1))
	s, err := store.New(testfix.NegativeModel())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	a, err := attr.New(attr.Schema{recoant.User: {
		"city": {Type: attr.Category}, "age": {Type: attr.Number}, "tags": {Type: attr.Set},
		"location": {Type: attr.Geo, Sensitive: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for i, u := range []string{"u1", "u2", "u3", "u4", "u5", "u6"} {
		city, tags := "Berlin", []string{"hiking", "jazz"}
		if i >= 3 {
			city, tags = "Hamburg", []string{"sailing"}
		}
		for name, v := range map[string]attr.Value{
			"city": attr.Cat(city), "age": attr.Num(float64(20 + 5*i)), "tags": attr.Tags(tags...), "location": attr.At(52+float64(i), 10),
		} {
			if err := a.Set(recoant.U(u), name, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	m, err := als.New(s, als.Config{Relation: testfix.Rel, Factors: 4, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	return s, a, m
}

func TestSegmenter(t *testing.T) {
	s, a, m := fixture(t)
	for _, features := range [][]segment.Feature{
		{{Name: segment.Taste}},
		{{Name: "city"}},
		{{Name: segment.Taste}, {Name: "tags", Weight: 2}, {Name: "age", Weight: 0.1}},
	} {
		seg, err := segment.New(s, a, m, segment.Config{Kind: recoant.User, Features: features, Relation: testfix.Rel, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := seg.Segment(recoant.U("u1")); ok || seg.Profiles() != nil || seg.Members(0) != nil {
			t.Error("segment before Fit")
		}
		if err := seg.Fit(ctx); err != nil {
			t.Fatal(err)
		}
		a1, _ := seg.Segment(recoant.U("u1"))
		b1, _ := seg.Segment(recoant.U("u4"))
		for _, u := range []string{"u2", "u3"} {
			if x, _ := seg.Segment(recoant.U(u)); x != a1 {
				t.Errorf("%v: %s not with u1", features, u)
			}
		}
		if x, _ := seg.Segment(recoant.U("u5")); x != b1 || a1 == b1 {
			t.Errorf("%v: u5 in %d, u4 in %d, u1 in %d", features, x, b1, a1)
		}
		if got := seg.Members(a1); len(got) != 3 {
			t.Errorf("%v: members %v", features, got)
		}
	}
}

func TestProfiles(t *testing.T) {
	s, a, m := fixture(t)
	seg, _ := segment.New(s, a, m, segment.Config{Kind: recoant.User, Features: []segment.Feature{{Name: "city"}}, Relation: testfix.Rel, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
	if err := seg.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	berlin, _ := seg.Segment(recoant.U("u1"))
	p := seg.Profiles()[berlin]
	if p.Size != 3 || p.Share != 0.5 {
		t.Errorf("size %d share %v", p.Size, p.Share)
	}
	// u1–u3 have 2, 3 and 4 pairs (u3 hid z); one of 9 is negative.
	if math.Abs(p.Activity-3) > 1e-9 || math.Abs(p.NegativeShare-1.0/9) > 1e-9 {
		t.Errorf("activity %v negative share %v", p.Activity, p.NegativeShare)
	}
	if got := testfix.IDs(p.Typical); len(got) != 3 || !slices.Contains(got, "a") || slices.Contains(got, "z") {
		t.Errorf("typical %v", got)
	}
	city, age, tags := p.Attributes["city"], p.Attributes["age"], p.Attributes["tags"]
	if city.Coverage != 1 || len(city.Values) != 1 || city.Values[0] != (segment.ValueShare{Value: "Berlin", Share: 1}) {
		t.Errorf("city %+v", city)
	}
	if age.Mean != 25 || age.Min != 20 || age.Max != 30 {
		t.Errorf("age %+v", age)
	}
	if len(tags.Values) != 2 || tags.Values[0].Value != "hiking" {
		t.Errorf("tags %+v", tags)
	}
	if _, ok := p.Attributes["location"]; ok {
		t.Error("sensitive location in profile")
	}

	prof, ok := seg.Profile(recoant.U("u3"), 2)
	if !ok || prof.Segment != berlin || prof.Interactions != 4 || prof.Positive != 3 || prof.Negative != 1 {
		t.Errorf("profile of u3: %+v %v", prof, ok)
	}
	if got := testfix.IDs(prof.Nearest); len(got) != 2 || slices.Contains(got, "u3") || prof.Nearest[0].Score != 1 {
		t.Errorf("nearest of u3: %v", prof.Nearest)
	}
	// A new user in Berlin without signals is assigned by attributes.
	_ = a.Set(recoant.U("new"), "city", attr.Cat("Berlin"))
	_ = s.Add(recoant.Signal{Subject: recoant.U("new"), Object: testfix.P("zz"), Type: recoant.Visit, Value: 1, Time: testfix.T0})
	if x, ok := seg.Segment(recoant.U("new")); !ok || x != berlin {
		t.Errorf("new user in %d %v", x, ok)
	}
	if prof, ok := seg.Profile(recoant.U("new"), 0); !ok || prof.Nearest != nil || prof.Distance != 0 {
		t.Errorf("profile of new user: %+v %v", prof, ok)
	}
	for _, ref := range []recoant.Ref{recoant.U("nobody"), testfix.P("a")} {
		if _, ok := seg.Profile(ref, 1); ok {
			t.Errorf("profile of %s", ref)
		}
	}
}

// Without a relation, or before it has signals, profiles have no activity.
func TestNoRelation(t *testing.T) {
	s, a, m := fixture(t)
	for _, rel := range []store.Relation{{}, {Subject: recoant.User, Object: "job"}} {
		seg, err := segment.New(s, a, m, segment.Config{Kind: recoant.User, Features: []segment.Feature{{Name: "age"}}, Relation: rel, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if err := seg.Fit(ctx); err != nil {
			t.Fatal(err)
		}
		if p := seg.Profiles(); len(p) != 2 || p[0].Activity != 0 || p[0].Typical != nil {
			t.Errorf("%v: %+v", rel, p)
		}
		if prof, _ := seg.Profile(recoant.U("u1"), 0); prof.Interactions != 0 {
			t.Errorf("%v: %+v", rel, prof)
		}
	}
}

func TestSensitive(t *testing.T) {
	s, a, m := fixture(t)
	cfg := segment.Config{Kind: recoant.User, Features: []segment.Feature{{Name: "location"}}, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}}
	if _, err := segment.New(s, a, m, cfg); !errors.Is(err, segment.ErrConfig) {
		t.Errorf("sensitive accepted: %v", err)
	}
	cfg.AllowSensitive = true
	seg, err := segment.New(s, a, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := seg.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := seg.Profiles()[0].Attributes["location"]; ok {
		t.Error("geo in profile")
	}
	if seg.Config().Features[0].Weight != 1 {
		t.Errorf("default weight %+v", seg.Config())
	}
}

func TestConfig(t *testing.T) {
	s, a, m := fixture(t)
	km := segment.KMeansConfig{K: 2}
	for name, c := range map[string]segment.Config{
		"k":        {Kind: recoant.User, Features: []segment.Feature{{Name: "age"}}},
		"kind":     {KMeansConfig: km, Features: []segment.Feature{{Name: "age"}}},
		"features": {KMeansConfig: km, Kind: recoant.User},
		"relation": {KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: "age"}}, Relation: store.Relation{Subject: "product", Object: recoant.User}},
		"weight":   {KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: "age", Weight: -1}}},
		"twice":    {KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: "age"}, {Name: "age"}}},
		"unknown":  {KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: "height"}}},
	} {
		if _, err := segment.New(s, a, m, c); !errors.Is(err, segment.ErrConfig) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := segment.New(s, a, nil, segment.Config{KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: segment.Taste}}}); !errors.Is(err, segment.ErrConfig) {
		t.Errorf("taste without model: %v", err)
	}
	if _, err := segment.New(nil, a, m, segment.Config{}); !errors.Is(err, segment.ErrConfig) {
		t.Errorf("nil store: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	seg, _ := segment.New(s, a, m, segment.Config{KMeansConfig: km, Kind: recoant.User, Features: []segment.Feature{{Name: "age"}}})
	if err := seg.Fit(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
}

// Products segmented by appearance; users without attributes are left out
// of attribute-only segments and summarized as missing.
func TestAppearanceAndMissing(t *testing.T) {
	s, a, m := fixture(t)
	seg, err := segment.New(s, a, m, segment.Config{Kind: "product", Features: []segment.Feature{{Name: segment.Appearance}}, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := seg.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	pa, _ := seg.Segment(testfix.P("a"))
	px, _ := seg.Segment(testfix.P("x"))
	if pc, _ := seg.Segment(testfix.P("c")); pc != pa || pa == px {
		t.Errorf("a %d, c %d, x %d", pa, pc, px)
	}
	_ = s.Add(testfix.Sig("lonely", "a", recoant.Purchase, 1))
	_ = m.Fit(ctx)
	byCity, _ := segment.New(s, a, m, segment.Config{Kind: recoant.User, Features: []segment.Feature{{Name: "city"}}, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
	_ = byCity.Fit(ctx)
	if _, ok := byCity.Segment(recoant.U("lonely")); ok {
		t.Error("user without city segmented by city")
	}
	byTaste, _ := segment.New(s, a, m, segment.Config{Kind: recoant.User, Features: []segment.Feature{{Name: segment.Taste}}, KMeansConfig: segment.KMeansConfig{K: 2, Seed: 1}})
	_ = byTaste.Fit(ctx)
	j, ok := byTaste.Segment(recoant.U("lonely"))
	if !ok || byTaste.Profiles()[j].Attributes["city"].Coverage == 1 {
		t.Errorf("lonely: %d %v %+v", j, ok, byTaste.Profiles()[j].Attributes["city"])
	}
}
