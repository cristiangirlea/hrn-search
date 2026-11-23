# hrn-search, a plugin for hrn

`hrn-search` is a program that hrn starts as a child process and talks to over JSON lines on
stdin and stdout: the plugin protocol v1
(<https://github.com/cristiangirlea/harness/blob/main/docs/plugins.md>). It offers one capability,
`search`, a bounded and deterministic synthesizer for integer expressions. README.md describes the
task specification, the verdicts and the limits.

## Layout

| Path | Holds |
|---|---|
| `main.go` | The program: reads stdin, writes stdout, exits 0, 1 or 2 as the protocol says. |
| `internal/serve/` | The protocol loop: handshake, one call at a time, cancel, timeouts, progress, results. |
| `internal/task/` | The typed task specification and its strict decoding. |
| `internal/search/` | The enumerator and the benchmark gate (`bench_test.go`). |
| `.github/release.sh` | Builds the six release binaries and `SHA256SUMS` into `dist/`. |

## Build and test

Go 1.26.x (`go.mod`). Before a PR run the steps in `.claude/skills/check/SKILL.md` (`/check`);
CI runs the same on GitHub's runners, plus `go test -race` on Linux. `-race` needs cgo: where it
is missing, say so and leave it to CI.

## Rules of this repository

- **Standard library plus the contracts module only.** The protocol types come from
  `github.com/cristiangirlea/harness/contracts`, pinned in `go.mod`; never copy them. Moving the
  pin is its own PR, with the golden fixture tests passing at the new version.
- **The protocol page is the contract.** Every line the plugin writes passes
  `pluginproto.ParsePluginLine`; a change in behaviour on the wire starts on that page in harness.
- **Validation stays held out.** The search sees training examples only; validation scores the
  first fit afterwards. A change that lets validation steer selection is a defect.
- **The benchmark gate only gets stricter.** A slower search raises no budget in
  `bench_test.go` without a maintainer's word.
- **No model calls**, no network, no files: input comes from the call, output goes to stdout.
- **Comments say why, not what**: one line, three at most; no issue numbers, dates or review
  history. Never name other products in code, docs, commits or PRs.
- **Releases.** A `v*` tag publishes a release through `.github/workflows/release.yml`; asset
  names must stay `hrn-search_<version>_<os>_<arch>[.exe]` with `SHA256SUMS`, as
  `hrn plugin install` expects. Only a maintainer tags.

## Working rules

- **A branch delivers what it is for, nothing else**: one issue, a short name, one PR whose
  description lists the files it touches and why. Larger work is a feature branch whose pieces
  merge into it; the feature branch reaches `main` through one PR.
- **Reviews and fixes.** Get an independent review. Every defect, missing test or security point
  in what the branch touches is fixed before the push, with the smallest change; one review
  round's fixes go in one commit. Nits are dropped, at most a line in the PR. Only work outside
  the branch or a maintainer's choice becomes an issue.
- **Merge only what is tested.** Merge `main` in once before the final checks. Then the check
  steps pass, every changed behaviour has a test, CI is green, the review's defects are fixed and
  a security read has nothing open, all on the head being merged. Merge through the PR only:
  `gh pr merge <n> --merge --delete-branch --match-head-commit <sha>`. Never `--auto` or
  `--admin`, never push to `main`.
- **Commits**: the area first, lower case, then a plain sentence (`search: keep validation out of
  selection`). No emoji and no AI attribution anywhere. Use the repository's git identity.
- Tests first, seen failing; name a test as a sentence about behaviour. Files are LF.
