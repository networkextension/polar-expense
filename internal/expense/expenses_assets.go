package expense

// Assets glue (doc/arch/blob-storage-to-assets-migration.md in
// polar-dock): receipt images live exclusively in the central polar-assets
// catalog (single-write, tenant-owned). The existing `expenses.raw_image_id`
// column holds the per-row blob reference as an "asset://<id>" marker (the
// transitional local-path fallback + backfill were removed at cutover).

import (
	"crypto/rand"
	"encoding/hex"
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

// streamExpenseImageFromAssets serves a receipt from assets (raw_image_id
// is an "asset://" marker). Returns false (no body written) when the marker
// is unparseable or the fetch fails, so the caller can emit an error.
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
