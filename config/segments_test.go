package config_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
	"github.com/breskos/recoant/config"
	"github.com/breskos/recoant/internal/testfix"
)

const segShop = `"kinds": {
    "user": {"attributes": {"city": {"type": "category"}, "age": {"type": "number", "sensitive": true}}},
    "product": {}
  },
  "relations": {"buys": {"subject": "user", "object": "product"}, "rates": {"subject": "product", "object": "user"}},
  "signals": {"buy": {"relation": "buys"}, "hide": {"relation": "buys", "feedback": "negative"}, "rate": {"relation": "rates"}},
  "models": {"m": {"algorithm": "als", "relation": "buys", "params": {"factors": 4, "seed": 1}}, "knn": {"algorithm": "item-knn", "relation": "buys"}}`

func TestValidateSegments(t *testing.T) {
	cases := map[string]struct{ seg, want string }{
		"kind":       {`"shop": {"k": 2, "features": ["city"]}`, "segments.shop: unknown kind"},
		"k":          {`"user": {"features": ["city"]}`, "segments.user: segment: invalid configuration"},
		"features":   {`"user": {"k": 2}`, "segments.user.features: at least one"},
		"attribute":  {`"user": {"k": 2, "features": ["city"], "attribute": "city"}`, `segments.user.attribute: "city" is already`},
		"model":      {`"user": {"k": 2, "features": ["taste"], "model": "x"}`, `segments.user.model: unknown model "x"`},
		"knn":        {`"user": {"k": 2, "features": ["taste"], "model": "knn"}`, "segments.user.model: must be a factor model"},
		"taste":      {`"user": {"k": 2, "features": ["taste"]}`, "segments.user.features[0]: taste needs a model"},
		"appearance": {`"user": {"k": 2, "features": ["appearance"], "model": "m"}`, "segments.user.features[0]: appearance needs a model whose relation has user as object"},
		"twice":      {`"user": {"k": 2, "features": ["city", "city"]}`, `segments.user.features[1]: "city" is listed twice`},
		"unknown":    {`"user": {"k": 2, "features": ["height"]}`, "segments.user.features[0]: unknown attribute user.height"},
		"sensitive":  {`"user": {"k": 2, "features": ["age"]}`, "segments.user.features[0]: user.age is sensitive"},
		"weight":     {`"user": {"k": 2, "features": ["city"], "weights": {"age": 1}}`, "segments.user.weights.age"},
		"relation":   {`"user": {"k": 2, "features": ["city"], "relation": "x"}`, `segments.user.relation: unknown relation "x"`},
		"subject":    {`"user": {"k": 2, "features": ["city"], "relation": "rates"}`, `segments.user.relation: "rates" has subject kind "product"`},
	}
	for name, c := range cases {
		_, err := config.Load(strings.NewReader(`{"recoant": 1, ` + segShop + `, "segments": {` + c.seg + `}}`))
		if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// Users segmented by taste and city; "same segment" as a filter.
func TestSegmentEngine(t *testing.T) {
	cfg := `{"recoant": 1, ` + segShop + `,
	  "segments": {"user": {"k": 2, "features": ["taste", "city", "age"], "weights": {"city": 0.5}, "model": "m", "seed": 1, "allow_sensitive": true}},
	  "serving": {"peers": {"model": "m", "target": "user", "subject": "user", "filters": [{"attribute": "segment", "same": true}]}}}`
	c, err := config.Load(strings.NewReader(cfg))
	if err != nil {
		t.Fatal(err)
	}
	e, err := c.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Fit(ctx); err != nil { // no signals yet: nothing to segment
		t.Fatal(err)
	}
	var sigs []recoant.Signal
	for _, s := range testfix.Groups {
		s.Type = "buy"
		sigs = append(sigs, s)
	}
	if err := e.AddBatch(ctx, sigs); err != nil {
		t.Fatal(err)
	}
	for i, u := range []string{"u1", "u2", "u3", "u4", "u5", "u6"} {
		city := "Berlin"
		if i >= 3 {
			city = "Hamburg"
		}
		if err := e.Apply(attr.Event{Ref: recoant.U(u), Attribute: "city", Value: attr.Cat(city)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	seg, ok := e.Segmenter(recoant.User)
	if !ok {
		t.Fatal("no segmenter")
	}
	s1, _ := seg.Segment(recoant.U("u1"))
	s4, _ := seg.Segment(recoant.U("u4"))
	if s1 == s4 {
		t.Fatalf("u1 and u4 in segment %d", s1)
	}
	if v, ok := e.Attributes().Get(recoant.U("u2"), "segment"); !ok || v.Category != "s"+string(rune('0'+s1)) {
		t.Errorf("segment attribute of u2: %v %v", v, ok)
	}
	p := seg.Profiles()[s1]
	if _, ok := p.Attributes["segment"]; ok || p.Activity == 0 || len(p.Typical) == 0 {
		t.Errorf("profile %+v", p)
	}
	res, err := e.Recommend(ctx, "peers", recoant.U("u1"), 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if x, _ := seg.Segment(r.Ref); x != s1 {
			t.Errorf("peer %s of u1 in segment %d", r.Ref, x)
		}
	}
	if _, ok := e.Segmenter("product"); ok {
		t.Error("product segmenter")
	}
	// A deleted user loses its segment.
	e.Delete(recoant.U("u2"))
	if err := e.Fit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Attributes().Get(recoant.U("u2"), "segment"); ok {
		t.Error("deleted user keeps its segment")
	}
}
