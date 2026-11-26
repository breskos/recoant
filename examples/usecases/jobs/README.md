# Job board

**Two sides in one configuration: jobs for candidates (matching skills, near enough, in the salary range they want) and candidates for jobs (shortlisted for similar jobs).**

```sh
go run ./examples/usecases/jobs
```

```
Lena, Go developer in Berlin, wants 60–80k, applied as backend developer and Go engineer and dismissed data engineer.

Jobs for Lena (matching skills, within 30 km, 60–80k):
  1. SRE                  engineering  Berlin    79k €
  2. Platform engineer    engineering  Berlin    67k €

Candidates who looked at the backend developer job in Berlin also looked at:
  1. Go engineer          engineering  Berlin    61k €
  ...

Candidates for Platform engineer in Berlin (shortlisted for similar jobs, matching skills, within 30 km):
  1. candidate-78   engineering
  ...
```

Only two jobs fit Lena, and recoant shows only those two. A short list that fits beats a long one that does not.

## How it is built

| Part | Here | Why |
| --- | --- | --- |
| Kinds | `user` with `skills`, `location`, `min_salary`, `max_salary`; `job` with `skills`, `location`, `salary` | Salary and location of candidates are `sensitive` |
| Relations | `applies`: user → job, and `recruits`: job → user | One relation per direction. Each side has its own signals. |
| Signals | candidates: `view`, `save`, `apply`, `dismiss` (negative); recruiters: `shortlist`, `interview`, `decline` (negative) | |
| Models | `jobs` (ALS), `similar_jobs` (item-kNN), `candidates` (user-kNN on `recruits`), `new_jobs` | |
| Serving | `jobs_for_you`, `similar_jobs`, `candidates_for_job` | |

The filters for candidates:

```json
"filters": [
  {"attribute": "skills", "same": true},
  {"attribute": "location", "max_distance_km": 30},
  {"attribute": "salary", "within": ["min_salary", "max_salary"]}
]
```

- **`same` on a set** means "at least one skill in common".
- **`within`** checks the job's `salary` against the candidate's own range. No code is needed per user.

## Design decisions worth copying

- **Two relations instead of one.** "Lena applied to the job" and "the recruiter shortlisted Lena" are different statements, so they are not mixed. Each direction gets its own model.
- **user-kNN on `recruits`** finds jobs similar to this one (by who was shortlisted) and proposes their candidates: "recruiters of similar jobs liked these people".
- **`dismiss` and `decline` are negative**, so neither side sees the same mismatch twice.
- **Hard requirements are filters, soft preferences are signals.** Salary and distance are filters; taste in companies and roles is learned.

## Make it yours

- Add `remote` as a category, `seniority` as a number, `languages` as a set.
- The same pattern fits freelancer platforms, tutoring, housing (tenant ↔ flat) and B2B matchmaking.

## Coming in later versions

v0.6 brings **reciprocal matching**: one score for "Lena wants the job *and* the recruiter wants Lena", combined from both relations. Until then, the two directions are served separately, as shown here.
