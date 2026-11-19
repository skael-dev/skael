#!/bin/sh
# A review leaves the history alone. Exit 1 when the workspace holds a commit.
# safe.directory: the check runs as the sandbox user on a copy it does not own.
if git -c safe.directory='*' log --oneline -1 >/dev/null 2>&1; then
  echo "the workspace has commits:"
  git -c safe.directory='*' log --oneline -5
  exit 1
fi
echo "no commits"
