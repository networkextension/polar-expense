# polar-expense

Household-ledger (家庭账本) plugin for the [Polar](https://github.com/networkextension/Polar) platform.

CRUD over `expense_categories` + `expenses` per workspace, plus a receipt-image upload pipeline that runs OCR (Apple Vision) → text LLM, or an optional multimodal vision-LLM path when configured. Every workspace member has read+write access — this is a household ledger, not an admin tool.

## Status

Extracted from Polar dock on 2026-05-22. The L1 CRUD path is fully wired through the SDK. The L2 OCR + LLM paths build green via `stubs.go`:

- **Multimodal vision-LLM path** (`expense_extract_multimodal.go`): wired end-to-end through `sdk.LLMConfigGet` + `sdk.AgentLLMCallRecord`. Activates when `EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID` is set. The B5 marketplace-quota gate is a temporary no-op in the extracted svc (billing tables stay in dock).
- **OCR-then-LLM path** (`expense_extract.go`): OCR shell-out works; the synchronous `requestChatCompletion` call returns `errChatCompletionNotWired` — needs either a new SDK `ChatCompletion` surface or a direct `/api/proxy/v1/chat/completions` call. Tracked as `TODO(extract)` in `stubs.go`.

The dock-side handlers stay in `internal/app/dock/expense_*.go` until cutover; flip with the nginx snippet + `POLAR_EXPENSE_REMOTE=true` follow-up.

## Install

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /tmp/expense-svc ./cmd/expense-svc
rsync -avz /tmp/expense-svc local@<deploy-box>:/Users/local/.local/bin/
```

Environment (transport / DB / blob):

- `POLAR_DOCK_BASE` (default `http://127.0.0.1:8080`)
- `POLAR_PLUGIN_TOKEN` (plaintext from `/admin-plugins.html` — one-time print)
- `POLAR_EXPENSE_DB_DSN` (Postgres for `polar_expense`; see `scripts/migrate/expense-schema.sql`)
- `POLAR_EXPENSE_LISTEN` (default `127.0.0.1:8097`)
- `POLAR_EXPENSE_BLOB_DIR` (holds `expense-images/<sha[0:2]>/<sha><ext>`; default `/Users/local/expense-svc-data`)
- `POLAR_EXPENSE_METRICS_TOKEN` (Bearer for `/metrics`; unset = 404)

OCR + LLM (consumed inside the plugin, not `Config`-bound):

- `EXPENSE_OCR_BIN` (default `/Users/local/.local/bin/vision-ocr` — Apple Vision Swift CLI from `tools/vision-ocr/`)
- `EXPENSE_OCR_TIMEOUT_SEC` (default 60)
- `EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID` (int; set → route uploads to a vision-capable LLM directly, skipping OCR)

## Endpoints

All endpoints require an authed Bearer (verified against dock):

- `GET / POST /api/expense-categories`
- `PUT / DELETE /api/expense-categories/:id`
- `GET / POST /api/expenses`
- `PUT / DELETE /api/expenses/:id`
- `POST /api/expenses/from-image` — multipart `file`, returns a status=0 draft pre-filled by extraction
- `GET /api/expenses/:id/image` — original receipt bytes
- `GET /healthz` — uptime + DB ping (open)
- `GET /metrics` — Prometheus exposition (gated by `POLAR_EXPENSE_METRICS_TOKEN`)

## Schema migration

```bash
createdb polar_expense
psql -d polar_expense -f scripts/migrate/expense-schema.sql
# copy data from the old monolith DB:
./scripts/migrate/expense-data.sh           # dry-run
./scripts/migrate/expense-data.sh --apply   # write
```

## Related

- [Polar dock](https://github.com/networkextension/Polar)
- [polar-sdk](https://github.com/networkextension/polar-sdk)
- Sibling extractions: polar-library, polar-projects, polar-video, polar-hosts, polar-iosdist, polar-packtunnel

## License

MIT
