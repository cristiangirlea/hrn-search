---
name: check
description: Run what CI runs on this module (gofmt, vet, build, tests, release dry run) before a PR
---
Run the steps in order from the repository root and stop at the first failure. Report each
step's result in one line; on a failure, show the error text and fix it before re-running.

```bash
test -z "$(git ls-files -co --exclude-standard -- '*.go' | xargs -r gofmt -l)"
go vet ./...
go build ./...
go test -count=1 ./...
bash -n .github/release.sh
bash .github/release.sh && (cd dist && { sha256sum --check --strict SHA256SUMS 2>/dev/null || shasum -a 256 -c SHA256SUMS; })
```

CI also runs `go test -race -count=1 ./...` on Linux. It needs cgo: run it only where
`go env CGO_ENABLED` prints 1 and a C compiler is present; otherwise report it as left to CI.
The release dry run builds into `dist/`, which is ignored.
