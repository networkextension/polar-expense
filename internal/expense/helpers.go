package expense

// helpers.go — small functions copied from dock that the moved
// handlers depend on. Kept here so expense-svc has no compile-time
// dependency on the dock package.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// systemUserID — sentinel "system" user. Matches dock's ai_agent.go
// constant; kept here to avoid the dock dep.
const systemUserID = "system"

// generateSessionID — random base64-URL token. Used by
// buildUploadFilename so stored filenames are unique even on
// collision.
func generateSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.URLEncoding.EncodeToString(b)
}

// generateResourceID — local copy of dock's helper (same shape:
// 16 random bytes → 32-char hex). Used as the default PK for
// expense-image staging filenames.
func generateResourceID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// buildUploadFilename mirrors dock's handler_helpers.go function:
// timestamp + 8-char random + lower-case extension. Validates the
// extension is ASCII alnum or falls back to .img. Expense uploads
// receipt images (jpg/png/heic/webp); .img is the safe fallback.
func buildUploadFilename(original string) string {
	ext := strings.ToLower(filepath.Ext(original))
	if ext == "" || len(ext) > 8 {
		ext = ".img"
	} else {
		for _, r := range ext[1:] {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				ext = ".img"
				break
			}
		}
	}
	return fmt.Sprintf("%s_%s%s", time.Now().Format("20060102_150405"), generateSessionID()[:8], ext)
}

// ── gin context helpers (mirror dock/teams_handlers.go) ──

func requireWorkspaceID(c *gin.Context) (string, bool) {
	v, _ := c.Get(ctxKeyWorkspaceID)
	id, ok := v.(string)
	if !ok || id == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return "", false
	}
	return id, true
}

func requireUserID(c *gin.Context) (string, bool) {
	v, _ := c.Get(ctxKeyUserID)
	id, ok := v.(string)
	if !ok || id == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return "", false
	}
	return id, true
}

func parseInt64Param(c *gin.Context, name string) (int64, bool) {
	raw := c.Param(name)
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入数据"})
		return 0, false
	}
	return id, true
}

// ── string utility helpers ──

// truncateForLog caps long debug strings before they hit logs/DB.
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

// envIntDefault parses an integer env var, falling back on bad input.
func envIntDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
