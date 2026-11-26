package evaluation_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/evaluation"
)

// lists recommends fixed lists per subject.
type lists map[string][]string

func (l lists) Recommend(_ context.Context, req recoant.Request) ([]recoant.Scored, error) {
	if req.Subject.ID == "err" {
		return nil, errors.New("boom")
	}
	var out []recoant.Scored
	for _, id := range l[req.Subject.ID] {
		out = append(out, recoant.Scored{Ref: recoant.U(id)})
	}
	return out, nil
}

func like(from, to string) recoant.Signal {
	return recoant.Signal{Subject: recoant.U(from), Object: recoant.U(to), Type: "like", Value: 1}
}

func TestMutual(t *testing.T) {
	// a likes b and c in test; b liked a in train (a match), c did not.
	// d likes e in test, e never liked d.
	train := []recoant.Signal{like("b", "a")}
	test := []recoant.Signal{like("a", "b"), like("a", "c"), like("d", "e")}
	recs := lists{"a": {"b", "c"}, "d": {"e", "b"}}
	rep, err := evaluation.Mutual(context.Background(), recs, train, test, evaluation.Config{K: 2, Target: recoant.User})
	if err != nil {
		t.Fatal(err)
	}
	want := evaluation.MutualReport{K: 2, Subjects: 2, HitRate: 1, MutualHitRate: 0.5, MutualPrecision: 0.25, Recommended: 3}
	// b is recommended twice, c and e once: Gini of (1, 1, 2) over 4.
	gini := rep.Gini
	rep.Gini = 0
	if rep != want || math.Abs(gini-1.0/6) > 1e-9 {
		t.Errorf("got %+v gini %v, want %+v gini 1/6", rep, gini, want)
	}
	var b strings.Builder
	if _, err := rep.WriteTo(&b); err != nil || !strings.Contains(b.String(), "mutual hit rate@2   0.5000") {
		t.Errorf("table %q %v", b.String(), err)
	}
}

func TestMutualErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := evaluation.Mutual(ctx, lists{}, nil, nil, evaluation.Config{}); err == nil {
		t.Error("empty config accepted")
	}
	if _, err := evaluation.Mutual(ctx, lists{}, nil, []recoant.Signal{like("err", "a")}, evaluation.Config{K: 1, Target: recoant.User}); err == nil {
		t.Error("recommender error not returned")
	}
	rep, err := evaluation.Mutual(ctx, lists{}, nil, nil, evaluation.Config{K: 1, Target: recoant.User})
	if err != nil || rep.Subjects != 0 || rep.Gini != 0 {
		t.Errorf("no subjects: %+v %v", rep, err)
	}
}

func TestBySegment(t *testing.T) {
	// a gets its like recommended, d does not.
	test := []recoant.Signal{like("a", "b"), like("d", "e")}
	recs := lists{"a": {"b"}, "d": {"x"}}
	group := func(r recoant.Ref) (string, bool) {
		if r.ID == "a" {
			return "s1", true
		}
		return "", false
	}
	reps, err := evaluation.BySegment(context.Background(), recs, test, evaluation.Config{K: 1, Target: recoant.User}, group)
	if err != nil || len(reps) != 2 || reps["s1"].HitRate != 1 || reps[""].HitRate != 0 || reps[""].Subjects != 1 {
		t.Errorf("%+v %v", reps, err)
	}
	if _, err := evaluation.BySegment(context.Background(), recs, test, evaluation.Config{}, group); err == nil {
		t.Error("invalid config accepted")
	}
}
