#!/usr/bin/env bash
# check: private-words
# born: 2026-11-01
# failure: private build documents were committed to the public repository and had to be scrubbed from its history
# rule: git tracks nothing under .build or docs, and no tracked file or commit message holds a word from .build/private-words.txt
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

status=0

tracked=$(git ls-files -- .build docs)
if [ -n "$tracked" ]; then
  echo "private-words: private paths are tracked:"
  echo "$tracked"
  status=1
fi

# A clone without the private directory has no words to look for.
words=.build/private-words.txt
if [ -s "$words" ]; then
  hits=$(git grep -lIiF -f "$words" || true)
  if [ -n "$hits" ]; then
    echo "private-words: private words found in files:"
    echo "$hits"
    status=1
  fi
  commits=$(git log --format='%h %s%n%b' | grep -iF -f "$words" | cut -c1-80 || true)
  if [ -n "$commits" ]; then
    echo "private-words: private words found in commit messages:"
    echo "$commits"
    status=1
  fi
fi

exit "$status"
