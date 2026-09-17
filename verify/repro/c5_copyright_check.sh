#!/usr/bin/env bash
# Run from the repo root: bash verify/repro/c5_copyright_check.sh   (exit 1 = a tracked .go file lacks the gRPC copyright header; this is the check from scripts/vet.sh line 40)
set -uo pipefail
missing=$( (grep -L "DO NOT EDIT" $(git grep -L "\(Copyright [0-9]\{4,\} gRPC authors\)" -- '*.go') || true) )
if [ -n "$missing" ]; then
  echo "files without a gRPC copyright header (scripts/vet.sh would fail):"
  printf '  %s\n' $missing
  exit 1
fi
echo "all tracked .go files carry a gRPC copyright header"
