package evaluation_test

import (
	"testing"

	"github.com/breskos/recoant"
	"github.com/breskos/recoant/evaluation"
)

type fixed map[string]float64

func (f fixed) Predict(_, object recoant.Ref) (float64, bool) {
	v, ok := f[object.ID]
	return v, ok
}

func TestErrors(t *testing.T) {
	test := []recoant.Signal{
		sig("u1", "a", "rating", 0), sig("u1", "b", "rating", 0), sig("u1", "c", "rating", 0), sig("u1", "a", "visit", 0),
	}
	test[0].Value, test[1].Value, test[2].Value = 4, 2, 5
	rep := evaluation.Errors(fixed{"a": 3, "b": 4}, test, []recoant.SignalType{"rating"})
	// errors 1 and 2: RMSE sqrt(2.5), MAE 1.5; c is missing.
	if rep.N != 2 || rep.Missing != 1 || !near(rep.RMSE, 1.5811388300841898) || !near(rep.MAE, 1.5) {
		t.Errorf("report %+v", rep)
	}
	if rep := evaluation.Errors(fixed{}, nil, nil); rep.N != 0 || rep.RMSE != 0 {
		t.Errorf("empty %+v", rep)
	}
}
