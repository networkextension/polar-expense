package expense

// Image storage for expense receipts. Bypasses the generic attachments
// table (doc-only allow-list) — receipts are jpeg/png/heic/webp and have
// a different lifecycle (deleted-with-expense rather than ref-counted).
//
// Layout: <uploadDir>/expense-images/<sha256[0:2]>/<sha256><ext>
// Stored value in expenses.raw_image_id: "expense-images/ab/abc123…png"
// (workspace-agnostic — receipts dedupe naturally across the entire box;
// privacy isolation is at the DB row level via workspace_id on expenses).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var allowedExpenseImageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".heic": "image/heic",
	".heif": "image/heif",
	".webp": "image/webp",
}

const expenseImageMaxBytes = 25 * 1024 * 1024 // 25 MiB; receipts are tiny, anything bigger smells like a screenshot mistake

// saveExpenseImage hashes + stores the image at a content-addressed path
// under uploadDir/expense-images/. Returns (relPath, sha, error). The
// caller stores relPath into expenses.raw_image_id.
//
// Dedup: if the file already exists at the canonical path, skip the write
// and return the existing path. Different receipts that happen to be byte
// identical reuse the same blob, which is fine — they're not secret data.
func (p *Plugin) saveExpenseImage(srcPath, originalFilename string) (relPath, sha string, err error) {
	ext := strings.ToLower(filepath.Ext(originalFilename))
	if _, ok := allowedExpenseImageExts[ext]; !ok {
		return "", "", fmt.Errorf("不支持的图片格式 %q（仅 jpg/png/heic/webp）", ext)
	}
	st, err := os.Stat(srcPath)
	if err != nil {
		return "", "", err
	}
	if st.Size() > expenseImageMaxBytes {
		return "", "", fmt.Errorf("图片超过 %d MiB 上限", expenseImageMaxBytes/(1024*1024))
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return "", "", err
	}
	defer src.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, src); err != nil {
		return "", "", err
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	rel := filepath.Join("expense-images", sum[:2], sum+ext)
	abs := filepath.Join(p.BlobDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(abs); err == nil {
		_ = os.Remove(srcPath)
		return rel, sum, nil
	}
	// Atomic rename within the same filesystem; fall back to copy if the
	// source is on a different mount (unlikely in our deploy but harmless).
	if err := os.Rename(srcPath, abs); err != nil {
		if err := copyExpenseImage(srcPath, abs); err != nil {
			return "", "", err
		}
		_ = os.Remove(srcPath)
	}
	return rel, sum, nil
}

// resolveExpenseImagePath turns a stored raw_image_id (relative path)
// into an absolute on-disk path. Returns "" when the input is empty or
// the file is missing.
func (p *Plugin) resolveExpenseImagePath(rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	abs := filepath.Join(p.BlobDir, rel)
	if _, err := os.Stat(abs); err != nil {
		return ""
	}
	return abs
}

func copyExpenseImage(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

var errEmptyExpenseImage = errors.New("empty expense image")
