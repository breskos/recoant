package attr_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/attr"
)

const product recoant.Kind = "product"

func pr(id string) recoant.Ref { return recoant.Ref{Kind: product, ID: id} }

func fp(v float64) *float64 { return &v }

var schema = attr.Schema{
	recoant.User: {
		"age":       {Type: attr.Number, Sensitive: true},
		"min_age":   {Type: attr.Number},
		"max_age":   {Type: attr.Number},
		"location":  {Type: attr.Geo, Sensitive: true},
		"interests": {Type: attr.Set},
		"city":      {Type: attr.Category},
	},
	product: {
		"brand": {Type: attr.Category},
		"price": {Type: attr.Number},
		"tags":  {Type: attr.Set},
		"store": {Type: attr.Geo},
	},
}

func newStore(t *testing.T) *attr.Store {
	t.Helper()
	s, err := attr.New(schema)
	if err != nil {
		t.Fatal(err)
	}
	set := func(ref recoant.Ref, name string, v attr.Value) {
		t.Helper()
		if err := s.Set(ref, name, v); err != nil {
			t.Fatal(err)
		}
	}
	// Users with ages, age preferences, locations (Berlin, Hamburg, Munich).
	for _, u := range []struct {
		id          string
		age, lo, hi float64
		lat, lon    float64
		interests   []string
	}{
		{"ann", 30, 28, 40, 52.52, 13.40, []string{"hiking", "jazz"}},
		{"bob", 35, 25, 32, 52.50, 13.45, []string{"jazz"}},
		{"cem", 45, 30, 50, 53.55, 9.99, []string{"chess"}},
		{"dan", 29, 35, 45, 48.14, 11.58, []string{"hiking"}},
	} {
		set(recoant.U(u.id), "age", attr.Num(u.age))
		set(recoant.U(u.id), "min_age", attr.Num(u.lo))
		set(recoant.U(u.id), "max_age", attr.Num(u.hi))
		set(recoant.U(u.id), "location", attr.At(u.lat, u.lon))
		set(recoant.U(u.id), "interests", attr.Tags(u.interests...))
	}
	set(recoant.U("eve"), "age", attr.Num(31)) // no preferences, no location
	set(recoant.U("ann"), "city", attr.Cat("berlin"))
	set(recoant.U("bob"), "city", attr.Cat("berlin"))
	set(recoant.U("cem"), "city", attr.Cat("hamburg"))
	set(pr("p1"), "brand", attr.Cat("acme"))
	set(pr("p1"), "price", attr.Num(10))
	set(pr("p1"), "tags", attr.Tags("red", "shoe", "red"))
	set(pr("p2"), "brand", attr.Cat("zeta"))
	set(pr("p2"), "price", attr.Num(50))
	set(pr("p2"), "tags", attr.Tags())
	set(pr("p3"), "price", attr.Num(30))
	set(pr("p3"), "store", attr.At(52.52, 13.40))
	return s
}

func TestSchemaValidate(t *testing.T) {
	for _, bad := range []attr.Schema{
		{"": {}},
		{"user": {"": {Type: attr.Number}}},
		{"user": {"x": {Type: "text"}}},
	} {
		if _, err := attr.New(bad); !errors.Is(err, attr.ErrInvalidSchema) {
			t.Errorf("New(%v) = %v", bad, err)
		}
	}
}

func TestSetGet(t *testing.T) {
	s := newStore(t)
	if v, ok := s.Get(pr("p1"), "tags"); !ok || !slices.Equal(v.Set, []string{"red", "shoe"}) {
		t.Errorf("tags = %v", v)
	}
	if v, ok := s.Get(pr("p2"), "tags"); !ok || len(v.Set) != 0 {
		t.Errorf("empty set must be present: %v %v", v, ok)
	}
	if v, ok := s.Get(recoant.U("ann"), "location"); !ok || v.Geo.Lat != 52.52 {
		t.Errorf("location = %v", v)
	}
	if _, ok := s.Get(pr("p3"), "brand"); ok {
		t.Error("missing category")
	}
	if _, ok := s.Get(pr("p9"), "brand"); ok {
		t.Error("unknown entity")
	}
	if _, ok := s.Get(recoant.Ref{Kind: "brand", ID: "x"}, "x"); ok {
		t.Error("unknown kind")
	}
	if err := s.Set(pr("p1"), "brand", attr.Cat("zeta")); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.Get(pr("p1"), "brand"); v.Category != "zeta" {
		t.Errorf("update: %v", v)
	}
	if s.Count(product, "price") != 3 || s.Count(product, "") != 3 || s.Count(product, "nope") != 0 || s.Count("brand", "") != 0 {
		t.Error("Count")
	}
	if !reflect.DeepEqual(s.Schema(), schema) {
		t.Error("Schema")
	}
}

func TestErrorsHideValues(t *testing.T) {
	s := newStore(t)
	cases := []struct {
		ref  recoant.Ref
		name string
		v    attr.Value
		want error
	}{
		{recoant.U("x"), "age", attr.Num(math.NaN()), attr.ErrInvalid},
		{recoant.U("x"), "age", attr.Cat("41"), attr.ErrInvalid},
		{recoant.U("x"), "city", attr.Cat(""), attr.ErrInvalid},
		{recoant.U("x"), "interests", attr.Tags("a", ""), attr.ErrInvalid},
		{recoant.U("x"), "location", attr.At(91.4141, 0), attr.ErrInvalid},
		{recoant.U("x"), "location", attr.At(0, 181), attr.ErrInvalid},
		{recoant.U("x"), "height", attr.Num(1), attr.ErrUnknown},
		{recoant.Ref{Kind: "user"}, "age", attr.Num(1), attr.ErrInvalid},
		{recoant.U("x"), "age", attr.Value{Type: "text"}, attr.ErrInvalid},
	}
	for i, c := range cases {
		err := s.Set(c.ref, c.name, c.v)
		if !errors.Is(err, c.want) {
			t.Errorf("case %d: %v", i, err)
		}
		if err != nil && (strings.Contains(err.Error(), "41") || strings.Contains(err.Error(), "91.4141")) {
			t.Errorf("case %d: error contains the value: %v", i, err)
		}
	}
	if err := (attr.Value{Type: "text"}).Validate("text"); !errors.Is(err, attr.ErrInvalid) {
		t.Error("unknown type validated")
	}
}

func TestEventsAndDelete(t *testing.T) {
	s := newStore(t)
	for _, ev := range []attr.Event{
		{Ref: recoant.U("ann"), Op: attr.OpSet, Attribute: "city", Value: attr.Cat("paris")},
		{Ref: recoant.U("bob"), Op: attr.OpDelete, Attribute: "city"},
		{Ref: recoant.U("cem"), Op: attr.OpDelete},
		{Ref: recoant.U("nobody"), Op: attr.OpDelete},
	} {
		if err := s.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if v, _ := s.Get(recoant.U("ann"), "city"); v.Category != "paris" {
		t.Error("set event")
	}
	if _, ok := s.Get(recoant.U("bob"), "city"); ok {
		t.Error("delete attribute event")
	}
	if s.Attributes(recoant.U("cem"), true) != nil || s.Count(recoant.User, "age") != 4 {
		t.Error("delete entity event")
	}
	if ok, err := s.Unset(recoant.U("bob"), "city"); ok || err != nil {
		t.Error("Unset of a missing attribute")
	}
	if _, err := s.Unset(recoant.U("bob"), "nope"); !errors.Is(err, attr.ErrUnknown) {
		t.Error("Unset of an unknown attribute")
	}
	for _, ev := range []attr.Event{
		{Ref: recoant.U("ann"), Op: "merge"},
		{Ref: recoant.Ref{Kind: "user"}, Op: attr.OpDelete},
	} {
		if err := s.Apply(ev); !errors.Is(err, attr.ErrInvalid) {
			t.Errorf("Apply(%v) = %v", ev, err)
		}
	}
	if s.Delete(recoant.Ref{Kind: "brand", ID: "x"}) {
		t.Error("Delete of an unknown kind")
	}
}

func TestAttributesSensitive(t *testing.T) {
	s := newStore(t)
	public := s.Attributes(recoant.U("ann"), false)
	if _, ok := public["age"]; ok {
		t.Error("sensitive attribute exported")
	}
	if _, ok := public["interests"]; !ok || len(public) != 4 {
		t.Errorf("public attributes: %v", public)
	}
	if all := s.Attributes(recoant.U("ann"), true); len(all) != 6 {
		t.Errorf("all attributes: %v", all)
	}
	if s.Attributes(recoant.U("zed"), true) != nil || s.Attributes(recoant.Ref{Kind: "x", ID: "y"}, true) != nil {
		t.Error("unknown entity")
	}
}

func TestValueJSON(t *testing.T) {
	ev := attr.Event{Ref: recoant.U("ann"), Attribute: "location", Value: attr.At(52.5, 13.4)}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"ref":{"kind":"user","id":"ann"},"attribute":"location","value":{"lat":52.5,"lon":13.4}}` {
		t.Errorf("JSON = %s", b)
	}
	for _, v := range []attr.Value{attr.Cat("x"), attr.Num(2.5), attr.Tags("a", "b"), attr.Tags(), attr.At(1, 2), {}} {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var back attr.Value
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatal(err)
		}
		if back.Type != v.Type || back.Category != v.Category || back.Number != v.Number || len(back.Set) != len(v.Set) || back.Geo != v.Geo {
			t.Errorf("%s: %+v != %+v", b, back, v)
		}
	}
	b, _ = json.Marshal(attr.Value{Type: attr.Set})
	if string(b) != "[]" {
		t.Errorf("nil set = %s", b)
	}
	for _, bad := range []string{`true`, `[1]`, `{"lat": 1}`, `{`} {
		var v attr.Value
		if err := json.Unmarshal([]byte(bad), &v); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestDistanceKm(t *testing.T) {
	berlin, hamburg := attr.Point{Lat: 52.52, Lon: 13.405}, attr.Point{Lat: 53.551, Lon: 9.994}
	if d := attr.DistanceKm(berlin, hamburg); math.Abs(d-255) > 3 {
		t.Errorf("Berlin–Hamburg = %.1f km", d)
	}
	if attr.DistanceKm(berlin, berlin) != 0 {
		t.Error("zero distance")
	}
}

func ids(t *testing.T, s *attr.Store, subject recoant.Ref, target recoant.Kind, conds ...recoant.Condition) []string {
	t.Helper()
	f, err := s.Compile(subject, target, conds)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	all := []string{"ann", "bob", "cem", "dan", "eve", "zed"}
	if target == product {
		all = []string{"p1", "p2", "p3", "p9"}
	}
	for _, id := range all {
		if f(recoant.Ref{Kind: target, ID: id}) {
			out = append(out, id)
		}
	}
	return out
}

func TestCompile(t *testing.T) {
	s := newStore(t)
	ann, eve := recoant.U("ann"), recoant.U("eve")
	cases := []struct {
		name    string
		subject recoant.Ref
		target  recoant.Kind
		conds   []recoant.Condition
		want    []string
	}{
		{"none", ann, product, nil, []string{"p1", "p2", "p3", "p9"}},
		{"category in", ann, product, []recoant.Condition{{Attribute: "brand", In: []string{"acme", "new"}}}, []string{"p1"}},
		{"set overlap", ann, product, []recoant.Condition{{Attribute: "tags", In: []string{"shoe"}}}, []string{"p1"}},
		{"range", ann, product, []recoant.Condition{{Attribute: "price", Min: fp(20)}}, []string{"p2", "p3"}},
		{"range max", ann, product, []recoant.Condition{{Attribute: "price", Max: fp(30)}}, []string{"p1", "p3"}},
		{"near", ann, product, []recoant.Condition{{Attribute: "store", Near: &[2]float64{52.5, 13.4}, MaxDistanceKm: 10}}, []string{"p3"}},
		{"two conditions", ann, product, []recoant.Condition{{Attribute: "price", Min: fp(20)}, {Attribute: "brand", In: []string{"zeta"}}}, []string{"p2"}},
		{"distance from subject", ann, recoant.User, []recoant.Condition{{Attribute: "location", MaxDistanceKm: 50}}, []string{"ann", "bob"}},
		{"distance, subject without location", eve, recoant.User, []recoant.Condition{{Attribute: "location", MaxDistanceKm: 50}}, []string{"ann", "bob", "cem", "dan", "eve", "zed"}},
		// ann wants 28–40: ann 30, bob 35, dan 29, eve 31; cem 45 is too old.
		{"within", ann, recoant.User, []recoant.Condition{{Attribute: "age", Within: []string{"min_age", "max_age"}}}, []string{"ann", "bob", "dan", "eve"}},
		// mutual: bob wants 25–32 (ann 30 ok), dan wants 35–45 (ann 30 no), eve has no preference.
		{"within mutual", ann, recoant.User, []recoant.Condition{{Attribute: "age", Within: []string{"min_age", "max_age"}, Mutual: true}}, []string{"ann", "bob", "eve"}},
		// eve has no preference: only the reverse direction applies (eve is 31).
		{"mutual, subject without preference", eve, recoant.User, []recoant.Condition{{Attribute: "age", Within: []string{"min_age", "max_age"}, Mutual: true}}, []string{"ann", "bob", "cem", "eve"}},
		{"same category", ann, recoant.User, []recoant.Condition{{Attribute: "city", Same: true}}, []string{"ann", "bob"}},
		{"same set", ann, recoant.User, []recoant.Condition{{Attribute: "interests", Same: true}}, []string{"ann", "bob", "dan"}},
		{"same, subject without value", eve, recoant.User, []recoant.Condition{{Attribute: "city", Same: true}}, []string{"ann", "bob", "cem", "dan", "eve", "zed"}},
	}
	for _, c := range cases {
		if got := ids(t, s, c.subject, c.target, c.conds...); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	f, _ := s.Compile(ann, product, []recoant.Condition{{Attribute: "price", Min: fp(0)}})
	if f(recoant.U("bob")) {
		t.Error("filter accepted another kind")
	}
	// within without mutual and a subject without preferences is skipped.
	if got := ids(t, s, eve, recoant.User, recoant.Condition{Attribute: "age", Within: []string{"min_age", "max_age"}}); len(got) != 6 {
		t.Errorf("skipped within: %v", got)
	}
}

func TestCheck(t *testing.T) {
	s := newStore(t)
	for i, c := range []recoant.Condition{
		{Attribute: "brand"},
		{Attribute: "nope", In: []string{"x"}},
		{Attribute: "price", In: []string{"x"}},
		{Attribute: "brand", Min: fp(1)},
		{Attribute: "price", MaxDistanceKm: 5},
		{Attribute: "store", MaxDistanceKm: 5}, // users have no "store"
		{Attribute: "price", Within: []string{"min_age", "max_age"}, Mutual: true},
		{Attribute: "price", Within: []string{"city", "max_age"}},
		{Attribute: "price", Same: true},
		{Attribute: "tags", Same: true},
	} {
		if _, err := s.Compile(recoant.U("ann"), product, []recoant.Condition{c}); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if err := s.Check(recoant.User, recoant.User, []recoant.Condition{{Attribute: "city", Same: true}}); err != nil {
		t.Error(err)
	}
}

type fixed []recoant.Scored

func (f fixed) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	var out []recoant.Scored
	for _, r := range f {
		if req.Filter == nil || req.Filter(r.Ref) {
			out = append(out, r)
		}
	}
	return out, nil
}

func TestFiltered(t *testing.T) {
	s := newStore(t)
	base := fixed{{Ref: pr("p1"), Score: 3}, {Ref: pr("p2"), Score: 2}, {Ref: pr("p3"), Score: 1}}
	req := recoant.Request{Subject: recoant.U("ann"), Target: product, K: 3, Where: []recoant.Condition{{Attribute: "price", Min: fp(20)}}}
	if _, err := base.Recommend(context.Background(), req); !errors.Is(err, recoant.ErrUnfiltered) {
		t.Fatalf("unwrapped recommender: %v", err)
	}
	rec := attr.Filtered(base, s)
	got := func(req recoant.Request) []string {
		t.Helper()
		res, err := rec.Recommend(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, r := range res {
			out = append(out, r.Ref.ID)
		}
		return out
	}
	if g := got(req); !slices.Equal(g, []string{"p2", "p3"}) {
		t.Errorf("Where: %v", g)
	}
	req.Filter = func(r recoant.Ref) bool { return r.ID != "p2" }
	if g := got(req); !slices.Equal(g, []string{"p3"}) {
		t.Errorf("Where and Filter: %v", g)
	}
	req.Where = nil
	if g := got(req); !slices.Equal(g, []string{"p1", "p3"}) {
		t.Errorf("Filter only: %v", g)
	}
	req.Where = []recoant.Condition{{Attribute: "nope", Same: true}}
	if _, err := rec.Recommend(context.Background(), req); !errors.Is(err, attr.ErrCondition) {
		t.Errorf("bad condition: %v", err)
	}
}

func TestSnapshot(t *testing.T) {
	s := newStore(t)
	s.Delete(recoant.U("dan"))
	b, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var sn attr.Snapshot
	if err := json.Unmarshal(b, &sn); err != nil {
		t.Fatal(err)
	}
	back, err := attr.FromSnapshot(&sn)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []recoant.Ref{recoant.U("ann"), recoant.U("bob"), recoant.U("eve"), pr("p1"), pr("p2"), pr("p3")} {
		if a, b := s.Attributes(ref, true), back.Attributes(ref, true); !reflect.DeepEqual(a, b) {
			t.Errorf("%v: %v != %v", ref, b, a)
		}
	}
	if back.Attributes(recoant.U("dan"), true) != nil || back.Count(recoant.User, "") != 4 {
		t.Error("deleted entity restored")
	}

	mutations := map[string]func(sn *attr.Snapshot){
		"version":        func(sn *attr.Snapshot) { sn.Version = 9 },
		"schema":         func(sn *attr.Snapshot) { sn.Schema = attr.Schema{"": nil} },
		"kind":           func(sn *attr.Snapshot) { sn.Kinds[0].Kind = "brand" },
		"duplicate kind": func(sn *attr.Snapshot) { sn.Kinds[1].Kind = sn.Kinds[0].Kind },
		"duplicate id":   func(sn *attr.Snapshot) { sn.Kinds[0].IDs[1] = sn.Kinds[0].IDs[0] },
		"column":         func(sn *attr.Snapshot) { sn.Kinds[0].Columns[0].Name = "nope" },
		"dup column":     func(sn *attr.Snapshot) { sn.Kinds[0].Columns[1] = sn.Kinds[0].Columns[0] },
		"lengths":        func(sn *attr.Snapshot) { sn.Kinds[0].Columns[0].Codes = nil },
		"entity":         func(sn *attr.Snapshot) { sn.Kinds[0].Columns[0].Index[0] = 99 },
		"code":           func(sn *attr.Snapshot) { sn.Kinds[0].Columns[0].Codes[0] = 99 },
		"values":         func(sn *attr.Snapshot) { sn.Kinds[0].Columns[0].Values = []string{"a", "a"} },
		"set code":       func(sn *attr.Snapshot) { sn.Kinds[0].Columns[3].Sets[0] = []uint32{99} },
		"number":         func(sn *attr.Snapshot) { sn.Kinds[0].Columns[1].Numbers[0] = math.Inf(1) },
	}
	for name, mutate := range mutations {
		var sn attr.Snapshot
		if err := json.Unmarshal(b, &sn); err != nil {
			t.Fatal(err)
		}
		mutate(&sn)
		if _, err := attr.FromSnapshot(&sn); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := attr.FromSnapshot(nil); !errors.Is(err, attr.ErrInvalidSnapshot) {
		t.Error("nil snapshot")
	}
}

func TestNeedsSubject(t *testing.T) {
	for _, c := range []struct {
		c    recoant.Condition
		want bool
	}{
		{recoant.Condition{Attribute: "a", In: []string{"x"}}, false},
		{recoant.Condition{Attribute: "a", Min: fp(1)}, false},
		{recoant.Condition{Attribute: "a", MaxDistanceKm: 5}, true},
		{recoant.Condition{Attribute: "a", MaxDistanceKm: 5, Near: &[2]float64{1, 2}}, false},
		{recoant.Condition{Attribute: "a", Within: []string{"lo", "hi"}}, true},
		{recoant.Condition{Attribute: "a", Same: true}, true},
	} {
		if got := attr.NeedsSubject(c.c); got != c.want {
			t.Errorf("NeedsSubject(%+v) = %v", c.c, got)
		}
	}
}

func TestUnmarshalJSONSyntax(t *testing.T) {
	var v attr.Value
	if err := v.UnmarshalJSON([]byte("{")); err == nil {
		t.Error("invalid JSON accepted")
	}
}
