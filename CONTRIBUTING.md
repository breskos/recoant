# Contributing to recoant

Thanks for your interest in recoant! Contributions of all sizes are welcome: bug reports, documentation fixes, tests and new features.

## Scope

The scope of recoant is described in the [roadmap](ROADMAP.md). Before working on a larger feature, check the roadmap and open an issue to discuss it.

Issues labeled [`good first issue`](https://github.com/breskos/recoant/labels/good%20first%20issue) are a good place to start.

## Ground rules

- **Standard library only.** The library must not add dependencies or require cgo.
- **Errors, not panics.** Library code returns errors and never calls `log.Fatal`, `os.Exit` or `panic` on bad input.
- **Reproducible.** Anything random takes a seed or a `*rand.Rand`.
- **Tested.** New code comes with tests. CI fails if total coverage drops below 90 %.
- **Documented.** Exported identifiers have doc comments and every package has runnable `Example…` tests. User-visible changes go into [CHANGELOG.md](CHANGELOG.md) under "Unreleased".

## Development

Requires Go 1.24 or newer and, for linting, [golangci-lint](https://golangci-lint.run/).

```
make vet     # go vet
make test    # all tests
make race    # tests with the race detector
make lint    # golangci-lint
make cover   # coverage report
make bench   # benchmarks
```

Example programs live in [examples](examples). Unit tests use small hand-made fixtures; synthetic data sets for quality tests and benchmarks are in [recoant-data](https://github.com/breskos/recoant-data).

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Keep the change focused: one feature or fix per pull request.
3. Run `make vet test lint` before pushing.
4. Describe what changed and why in the pull request.

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE).
