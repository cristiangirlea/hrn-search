# hrn-search

`hrn-search` is a bounded, deterministic expression synthesizer for hrn. Its `search` capability tries integer expressions built from `x`, `y`, supplied constants, and the operators `+`, `-`, and `*`. It runs locally with no model calls.

## Install and call

Install the latest release, or select a tag for reproducible runs.

```sh
hrn plugin install cristiangirlea/hrn-search
hrn plugin install cristiangirlea/hrn-search@v0.1.0
```

Save this task as `task.json`.

```json
{
  "training": [{"x":1,"y":2,"out":3},{"x":4,"y":-3,"out":1}],
  "validation": [{"x":7,"y":4,"out":11},{"x":-5,"y":3,"out":-2}],
  "constants": [1,2],
  "max_depth": 3,
  "max_candidates": 25000
}
```

```sh
hrn plugin call search search --input-file task.json
```

The capability output for this task is

```json
{"verdict":"verified","expression":"(x+y)","depth":1,"explored":8,"training":2,"validation":2,"validation_matched":2}
```

## Task specification

| Field | Meaning | Limits and default |
| --- | --- | --- |
| `training` | Examples used to select the expression | Required, 1–1,000 rows |
| `validation` | Held-out examples scored after selection | Required, 1–1,000 rows |
| `constants` | Integer literals available to the search | 0–16 distinct values, default `[]` |
| `max_depth` | Maximum nesting of binary operators | 0–5 inclusive, default 3 |
| `max_candidates` | Maximum number of candidates attempted | 1–5,000,000 inclusive, default 100,000 |

Each call retains at most 65,536 terms. Once the search reaches this ceiling, it still tests candidates built from the retained terms, but later terms cannot be used to build deeper expressions.

Each example requires `x`, `y`, and `out`. Field names are case-sensitive. Unknown fields, repeated keys, nulls, and numeric strings are refused. All numbers must represent signed 64-bit integers exactly. For example, `30e-1` is 3 and `3e-1` is invalid. A complete protocol line fits in 4 MiB before its line ending.

## Selection, verdicts, and evidence

Search starts at depth zero with `x`, `y`, and the constants in ascending order. It tries binary expressions in increasing depth and keeps the first representative of each distinct vector of training outputs for later combinations, up to the retention limit. An overflow on any training row discards the candidate. The candidate count includes duplicates and overflow attempts.

The first expression matching every training row ends selection. The plugin then evaluates that one expression on **every** validation row in input order. For example, training on `x=2, y=2, out=2` selects `x`. Validation on `x=7, y=1, out=1` rejects it, even though `y` would fit both rows.

| Verdict | Meaning and output |
| --- | --- |
| `verified` | Every validation row matches. Returns the selected `expression` and `validation_matched`. |
| `rejected` | At least one validation row differs or overflows. Returns the expression, total matched, and `first_mismatch` with zero-based `index`, `x`, `y`, `expected`, and either `actual` or `overflow: true`. |
| `not_found` | No candidate matched the training rows within the search limits. Returns search accounting only. |

All three verdicts are successful protocol results. Every output includes `training` and `validation` row counts, `explored` candidates, and the depth of the last attempted candidate. Cancellation and timeouts produce a `canceled` result. An invalid task produces a `failed` result with a field-specific message.

For a found expression, the result frame also carries training and validation evidence with their match counts. Each SHA-256 digest covers the compact UTF-8 JSON array in received row order, with fields `x`, `y`, `out` and integers in ordinary decimal notation, without a trailing newline. Evidence describes those examples. The host decides what the result establishes. See the [hrn plugin protocol](https://github.com/cristiangirlea/harness/blob/main/docs/plugins.md).

## Build, test, and release

```sh
go build -o hrn-search .
go vet ./...
go test -count=1 ./...
go test -v ./internal/search -run TestReferenceExpressionsVerifyWithinTheirCandidateBudgets
```

The benchmark gate runs as a normal test. It covers 12 reference expressions from leaves through depth three. Each uses fixed, independent random draws for training and validation, with disjoint input ranges. The test logs attempted candidates and requires every reference to verify within its individual budget.

A local build reports version `dev`. The release script requires Bash with `sha256sum` or `shasum` (Linux, macOS, or Git Bash). Run `bash .github/release.sh` to dry-run a release at `0.0.0-dev`, or `bash .github/release.sh v0.1.0` to build that version. The script cross-builds standalone binaries for Linux, macOS, and Windows on amd64 and arm64, then writes `dist/SHA256SUMS`. Asset names follow `hrn-search_<version>_<os>_<arch>` with `.exe` on Windows. A maintainer publishes by pushing a `v`-prefixed semantic version tag of at most 64 characters without build metadata. The release workflow runs vet and tests before publishing the binaries and checksums.

[MIT license](LICENSE)
