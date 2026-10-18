#!/usr/bin/env bash
# Guard the .gitignore rules that protect the memory database.
#
# Why this exists: all five required CI contexts pass without ever reading
# .gitignore. A regression that negates /.wrongstack/* at directory level would
# commit a live SQLite database and the suite would stay green.
#
# Two independent checks, because either alone is insufficient:
#   1. .gitignore must not negate /.wrongstack/ at directory/wildcard level.
#   2. No sage.db family path may appear in `git ls-files`.
# Check 2 is the real safety net: it catches a `git add -f`, or a future
# negation, even if check 1's pattern matching misses a rewrite of the file.
#
# Exit: 0 both checks pass, 1 a rule was violated.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

fail=0

# --- check 1: no directory-level negation of .wrongstack/ ------------------
# `!/.wrongstack/` alone is REQUIRED -- without it git refuses to descend into
# the directory at all, so a file-level negation beneath it would never match.
# What must never appear is a wildcard that re-includes everything inside.
bad_patterns=$(grep -nE '^[[:space:]]*![[:space:]]*/?\.wrongstack/(\*|\*\*)' .gitignore || true)
if [ -n "$bad_patterns" ]; then
    echo "check-gitignore-safety: FAIL — .gitignore negates .wrongstack/ at directory level"
    echo "$bad_patterns" | sed 's/^/  .gitignore:/'
    echo "  Negate individual FILES (e.g. !/.wrongstack/project.json), never the"
    echo "  directory contents: memories/ holds sage.db and its open SQLite WAL."
    fail=1
fi

# --- check 2: no tracked memory-database path ------------------------------
tracked=$(git ls-files | grep -E '(^|/)sage\.db(-wal|-shm|-journal)?$' || true)
if [ -n "$tracked" ]; then
    echo "check-gitignore-safety: FAIL — memory database paths are tracked"
    echo "$tracked" | sed 's/^/  tracked: /'
    echo "  sage.db is a live SQLite database. Committing it corrupts concurrent"
    echo "  access and bypasses graph, supersede, revision and audit invariants."
    fail=1
fi

if [ "$fail" -ne 0 ]; then
    exit 1
fi

echo "check-gitignore-safety: OK — .wrongstack/ negations are file-level, no sage.db tracked"