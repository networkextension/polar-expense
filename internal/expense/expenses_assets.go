package expense

// Assets migration (doc/arch/blob-storage-to-assets-migration.md in
// polar-dock): receipt images move from expense-svc-local disk to the
// central polar-assets catalog (single-write, tenant-owned). We reuse the
// existing `expenses.raw_image_id` column as the per-row blob reference:
// new rows store "asset://<id>"; legacy rows keep the
// "expense-images/<sha>.<ext>" relative path and read from local disk
// until the boot backfill migrates them.

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	sdk "github.com/networkextension/polar-sdk"
)

func expenseRandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// expenseImageAssetID parses an "asset://<id>" marker. (0,false) if the
// value is a legacy local relative path or empty.
func expenseImageAssetID(rawImageID string) (int64, bool) {
	if !strings.HasPrefix(rawImageID, "asset://") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(rawImageID, "asset://"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// uploadExpenseImageAsset streams a staged receipt image into the tenant's
// assets catalog (private, per-workspace) and returns the "asset://<id>"
// marker to store in raw_image_id.
func (p *Plugin) uploadExpenseImageAsset(ws, localPath string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	ext := strings.ToLower(filepath.Ext(localPath))
	mime := allowedExpenseImageExts[ext]
	if mime == "" {
		mime = "application/octet-stream"
	}
	meta, err := p.Dock.AssetUpload(sdk.AssetUploadInput{
		WorkspaceID: &ws,
		Kind:        "media",
		Name:        "expense-receipts/" + ws + "/" + expenseRandHex(6) + ext,
		Version:     "v1",
		Visibility:  "private",
		Mime:        mime,
	}, f)
	if err != nil {
		return "", err
	}
	return "asset://" + strconv.FormatInt(meta.ID, 10), nil
}

// streamExpenseImageFromAssets serves a receipt from assets when
// raw_image_id is an "asset://" marker. Returns false (caller falls back
// to the local file) for legacy rows or on fetch failure.
func (p *Plugin) streamExpenseImageFromAssets(c *gin.Context, rawImageID string) bool {
	id, ok := expenseImageAssetID(rawImageID)
	if !ok {
		return false
	}
	resp, err := p.Dock.AssetDownload(&sdk.AssetMeta{ID: id})
	if err != nil || resp == nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return false
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	c.DataFromReader(http.StatusOK, resp.ContentLength, ct, resp.Body, nil)
	return true
}

// backfillExpenseImagesOnce migrates any local-file receipts into the
// assets catalog on startup, rewriting raw_image_id to "asset://<id>".
// Idempotent goroutine from Start().
func (p *Plugin) backfillExpenseImagesOnce() {
	rows, err := p.DB.Query(`SELECT id, workspace_id, raw_image_id FROM expenses
		WHERE raw_image_id IS NOT NULL AND raw_image_id <> '' AND raw_image_id NOT LIKE 'asset://%'`)
	if err != nil {
		log.Printf("expense: image backfill: query: %v", err)
		return
	}
	type row struct {
		id  int64
		ws  string
		rel string
	}
	var pending []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.ws, &r.rel); err != nil {
			continue
		}
		pending = append(pending, r)
	}
	rows.Close()

	migrated := 0
	for _, r := range pending {
		abs := p.resolveExpenseImagePath(r.rel)
		if abs == "" {
			log.Printf("expense: image backfill: expense %d: local file missing (skip)", r.id)
			continue
		}
		marker, err := p.uploadExpenseImageAsset(r.ws, abs)
		if err != nil {
			log.Printf("expense: image backfill: expense %d: upload: %v", r.id, err)
			continue
		}
		if _, err := p.DB.Exec(`UPDATE expenses SET raw_image_id = $2 WHERE id = $1`, r.id, marker); err != nil {
			log.Printf("expense: image backfill: expense %d: update: %v", r.id, err)
			continue
		}
		migrated++
	}
	if migrated > 0 {
		log.Printf("expense: image backfill: migrated %d receipt(s) to assets", migrated)
	}
}
