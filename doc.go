// Package recoant is the root of recoant, a framework for recommendation
// engines in pure Go: batch, incremental and online on streams of user
// signals, for user-to-entity, entity-to-entity and user-to-user
// recommendations.
//
// This package holds the types every other package shares: references to
// users and entities ([Ref]), the signals between them ([Signal]), requests
// for recommendations ([Request]) and their results ([Scored]), and the
// interfaces algorithms implement ([Recommender], [Observer]).
//
// IDs are opaque strings. recoant expects them to be anonymized before they
// arrive and never interprets them.
package recoant
