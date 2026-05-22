#!/usr/bin/env bash
# expense-data.sh — copy expense_* tables from ideamesh → polar_expense.
# Same shape as wg-data.sh / packtunnel-data.sh / iosdist-data.sh.
set -euo pipefail

SRC_DSN="${SRC_DSN:-postgres://ideamesh:test123456@127.0.0.1:5432/ideamesh}"
DST_DSN="${DST_DSN:-postgres://ideamesh:test123456@127.0.0.1:5432/polar_expense}"
PSQL="${PSQL:-/Applications/Postgres.app/Contents/Versions/latest/bin/psql}"
PG_DUMP="${PG_DUMP:-/Applications/Postgres.app/Contents/Versions/latest/bin/pg_dump}"

APPLY=0
if [[ "${1:-}" == "--apply" ]]; then APPLY=1; fi

# Categories first (expenses.category_id FKs into it).
TABLES=(expense_categories expenses)

echo "=== expense-data.sh — $(if [[ $APPLY -eq 1 ]]; then echo APPLY; else echo DRY-RUN; fi) ==="
echo "source: $SRC_DSN"
echo "target: $DST_DSN"
echo
echo "--- source row counts ---"
for t in "${TABLES[@]}"; do
    n=$("$PSQL" "$SRC_DSN" -At -c "SELECT COUNT(*) FROM $t;" 2>/dev/null || echo "ERR")
    printf "  %-25s %s\n" "$t" "$n"
done
echo
if [[ $APPLY -eq 0 ]]; then
    echo "Dry run — pass --apply to perform the copy."
    exit 0
fi

TMPDIR=$(mktemp -d -t exmigrate)
trap 'rm -rf "$TMPDIR"' EXIT
DUMP="$TMPDIR/expense-data.sql"
"$PG_DUMP" "$SRC_DSN" --data-only --column-inserts --no-owner --no-privileges \
    $(printf -- '--table=%s ' "${TABLES[@]}") > "$DUMP"
echo "wrote $(wc -l < "$DUMP") lines to $DUMP"
{
    echo "BEGIN;"
    # Reverse order on truncate so the FK chain (expenses → expense_categories)
    # unwinds cleanly. CASCADE handles it either way but explicit is friendlier.
    echo "TRUNCATE expenses, expense_categories RESTART IDENTITY CASCADE;"
    cat "$DUMP"
    echo "COMMIT;"
} | "$PSQL" "$DST_DSN" -v ON_ERROR_STOP=1
echo
echo "--- target row counts (post-load) ---"
for t in "${TABLES[@]}"; do
    n=$("$PSQL" "$DST_DSN" -At -c "SELECT COUNT(*) FROM $t;")
    printf "  %-25s %s\n" "$t" "$n"
done
echo "Done."
