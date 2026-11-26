# Music and podcasts

**A weekly mix that follows changing taste, skips that count against a track, a radio of similar tracks, charts, and a family mode.**

```sh
go run ./examples/usecases/music
```

```
Leo played three jazz tracks half a year ago, four techno tracks this week and skipped "Neon Grid – Track A".

Leo's weekly mix:
  1. Subframe – Track B                 techno
  2. Neon Grid – Track B                techno
  3. Subframe – Track A                 techno
  4. Velvet Trio – Track B              jazz
  5. Night Owls – Track B               jazz

Radio after "Paper Kites – Track A":
  1. The Lowlands – Track A             indie
  ...

Charts in family mode (no explicit tracks):
  1. Neon Grid – Track A                techno
  ...
```

Leo's current taste (techno) leads the mix. His old jazz phase has faded but is not forgotten.

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user`, `track` with `genre` and `explicit` | `explicit` drives the family mode |
| Relation | `listens`, half-life 21 days; negative channel 90 days | Taste changes fast, dislikes last longer |
| Signals | `play` (value = seconds, `saturate` at 120), `like` 10, `playlist_add` 6, `skip` (negative, 2) | A full play counts more than a preview, but ten plays do not count ten times as much |
| Models | `mix` (ALS), `radio` (item-kNN), `charts` (popularity, half-life 3 days) | Personal, similar, trending |
| Serving | `weekly_mix`, `track_radio`, `charts` | |

```json
"play": {"relation": "listens", "weight": 1, "transform": {"kind": "saturate", "param": 120}}
```

The play's value is the number of seconds listened. `saturate` turns it into a strength between 0 and 1: 30 seconds counts noticeably, and four minutes counts not much more than two.

## Design decisions worth copying

- **The half-life is your "how fast does taste change" knob.** 21 days suits music. For podcasts or audiobooks, take longer.
- **Separate half-lives per channel:** likes fade with the relation, skips have their own 90 days.
- **Skips are negative but weak** (weight 2). One skip is a mood, many skips are a signal.
- **Radio = entity to entity:** ask with a track as the subject and get tracks back. The same `item-knn` model also serves "fans also like".
- **Family mode is one condition:** `{Attribute: "explicit", In: ["no"]}`. The same works for language, region or licensing.

## Make it yours

- Podcasts: `episode` as the kind, `show` and `language` as attributes, `completion` as a signal with value 0–1.
- Audiobooks, videos and courses follow the same pattern: consumption time as the value, `saturate` as the transform.

## Coming in later versions

Session-based "up next" (v0.8, streaming), artist graphs and diversity in the mix so it is not five tracks by the same artist (v0.11, re-ranking), and new releases without plays recommended from their attributes (v0.10).
