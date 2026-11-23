#!/bin/sh
# After an edit: CI refuses unformatted Go, so say so at once instead of at the push.
cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v gofmt >/dev/null 2>&1 || exit 0
unformatted=$(git ls-files -co --exclude-standard -- '*.go' | xargs -r gofmt -l 2>/dev/null)
if [ -n "$unformatted" ]; then
  printf 'gofmt would change these files (run gofmt -w on them):\n%s\n' "$unformatted" >&2
  exit 2
fi
exit 0
