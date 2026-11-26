package evaluation

import (
	"context"

	"github.com/breskos/recoant"
)

// BySegment evaluates r separately for groups of subjects, e.g. segments:
// group returns the group of a subject, ok false puts it into the group
// "". It returns one report per group, so that a model that serves most
// users well but one segment badly becomes visible.
func BySegment(ctx context.Context, r recoant.Recommender, test []recoant.Signal, cfg Config, group func(recoant.Ref) (string, bool)) (map[string]Report, error) {
	parts := map[string][]recoant.Signal{}
	for _, s := range test {
		g, _ := group(s.Subject)
		parts[g] = append(parts[g], s)
	}
	out := make(map[string]Report, len(parts))
	for g, sigs := range parts {
		rep, err := Evaluate(ctx, r, sigs, cfg)
		if err != nil {
			return nil, err
		}
		out[g] = rep
	}
	return out, nil
}
