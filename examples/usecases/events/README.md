# Events and meetups

**Events in your own city that match your interests, events near wherever you are right now, a "free only" filter, and events that disappear once they are over.**

```sh
go run ./examples/usecases/events
```

```
Ben lives in Hamburg, signed up for the Go meetup and AI night and is not interested in techno nights.

Events for Ben in his city:
  1. Hackathon          tech     Hamburg  30 €
  2. Cloud summit       tech     Hamburg  15 €
  3. Startup pitch      tech     Hamburg  free
  4. Book club          culture  Hamburg  free

Ben is in Munich this weekend (within 10 km):
  1. Go meetup          tech     Munich   30 €
  2. AI night           tech     Munich   free
  ...

… and only free ones:
  1. AI night           tech     Munich   free
  ...

After the AI night is over:
  1. Theatre premiere   culture  Munich   free
  2. Cloud summit       tech     Munich   free
```

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user` with `city`; `event` with `topic`, `city`, `location`, `price` | The user's `city` is the basis for "in my city" |
| Relation | `attends`, half-life 60 days | |
| Signals | `view` 1, `interested` 4, `rsvp` 8, `not_interested` (negative, 10) | |
| Models | `taste` (item-kNN), `popular` (popularity, half-life 7 days) | item-kNN: "people who went to X also went to Y" |
| Serving | `in_my_city` (filter `city` `same`), `anywhere` | |

Three kinds of location filters:

```json
{"attribute": "city", "same": true}
```

```go
// the phone's current position, per request
recoant.Condition{Attribute: "location", Near: &[2]float64{48.14, 11.58}, MaxDistanceKm: 10}
// "free only" from the UI
recoant.Condition{Attribute: "price", Max: &zero}
```

- **`same`** compares the event's `city` with the user's `city`. That is one rule for every user, with no request parameters.
- **`Near`** takes a point from the request, for travellers and "around me" features.

## Design decisions worth copying

- **Past events are deleted, not filtered.** `engine.Delete(ref)` removes an event from every recommendation at once. The next training forgets it completely.
- **item-kNN over factor models** when the catalogue changes constantly. Similarities between events are quick to recompute with `Refresh`.
- **"Not interested" is negative feedback.** It hides that event and pulls similar ones down.
- **Popularity with a short half-life** shows what is hot this week, not the all-time favourites.

## Make it yours

- Add `date` as a number (Unix days) and filter `Min: today` instead of deleting, if you want to keep past events for analytics.
- Venues, restaurants, cinemas and sports courses follow the same pattern with location, category and price.

## Coming in later versions

"Your friends are going" via the relation graph (v0.11), and brand-new events without RSVPs recommended from topic and venue (v0.10).
