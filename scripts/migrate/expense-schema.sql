-- ============================================================
-- polar_expense schema — end-state (post-L2).
--
-- Apply:
--   CREATE DATABASE polar_expense OWNER ideamesh;
--   psql -d polar_expense -f scripts/migrate/expense-schema.sql
--
-- Cross-DB refs (TEXT, resolved via dock SDK):
--   - created_by_user_id → /internal/v1/users/:id
--   - workspace_id       → /internal/v1/teams/:id
--
-- Note vs. dock's original schema:
--   - expense_categories.workspace_id loses its FK on teams(id)
--     (teams lives in dock; cross-DB FKs aren't a thing).
--   - expenses.created_by_user_id loses its FK on users(id) for the
--     same reason.
--   - expenses.raw_image_id is plain TEXT (the L2 migration in
--     dock dropped the FK on attachments(id) — expense images use
--     a dedicated content-addressed store at
--     BlobDir/expense-images/<sha>.<ext>).
-- ============================================================

CREATE TABLE IF NOT EXISTS expense_categories (
    id BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    name TEXT NOT NULL,
    icon TEXT NOT NULL DEFAULT '',
    color TEXT NOT NULL DEFAULT '#888',
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_hidden BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_expense_categories_workspace_name
    ON expense_categories(workspace_id, name);

CREATE TABLE IF NOT EXISTS expenses (
    id BIGSERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL,
    created_by_user_id TEXT NOT NULL,
    amount NUMERIC(12, 2) NOT NULL,
    currency TEXT NOT NULL DEFAULT 'CNY',
    merchant TEXT NOT NULL DEFAULT '',
    category_id BIGINT REFERENCES expense_categories(id) ON DELETE SET NULL,
    consume_time TIMESTAMPTZ NOT NULL,
    has_detail_time BOOLEAN NOT NULL DEFAULT FALSE,
    region TEXT NOT NULL DEFAULT '',
    raw_image_id TEXT,
    status SMALLINT NOT NULL DEFAULT 1,
    confidence SMALLINT NOT NULL DEFAULT 100,
    remark TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_expenses_workspace_consume
    ON expenses(workspace_id, consume_time DESC);
CREATE INDEX IF NOT EXISTS idx_expenses_workspace_status
    ON expenses(workspace_id, status, created_at DESC);
