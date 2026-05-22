package expense

// OCR for the expense module. L2 ships a single backend: Apple Vision via
// the vendored swift CLI at tools/vision-ocr/ (built + installed on the
// deploy box). No cloud OCR — Vision is free, local, accurate on Chinese,
// and the deploy boxes are all macOS.
//
// If a future deploy needs cloud (e.g. Linux box), introduce an interface
// here. YAGNI for now.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	defaultExpenseOCRBin     = "/Users/local/.local/bin/vision-ocr"
	defaultExpenseOCRTimeout = 60 * time.Second // long screenshots can take 20-30s
)

func expenseOCRBinPath() string {
	if v := strings.TrimSpace(os.Getenv("EXPENSE_OCR_BIN")); v != "" {
		return v
	}
	return defaultExpenseOCRBin
}

func expenseOCRTimeout() time.Duration {
	if n := envIntDefault("EXPENSE_OCR_TIMEOUT_SEC", 0); n > 0 {
		return time.Duration(n) * time.Second
	}
	return defaultExpenseOCRTimeout
}

// visionOCRResult mirrors the JSON shape emitted by tools/vision-ocr (see
// Sources/vision-ocr/main.swift::OCRResult). We only consume FullText —
// the per-record array stays parsed but ignored, available for future
// bbox-aware features (e.g. zoom-to-line on misclassification).
type visionOCRResult struct {
	ImagePath   string `json:"imagePath"`
	RecordCount int    `json:"recordCount"`
	Records     []struct {
		Text       string  `json:"text"`
		Confidence float64 `json:"confidence"`
	} `json:"records"`
	FullText string `json:"fullText"`
}

// runVisionOCR shells out to the vision-ocr CLI and returns its parsed
// JSON. Caller is responsible for writing the image to a path the binary
// can read (typically the same content-addressed location we just saved).
//
// We pass --no-bbox + --lang zh-Hans,en-US: the LLM only needs the text
// flow, bounding boxes add bytes without helping structuring, and most
// Chinese receipts also have English numbers/symbols.
func runVisionOCR(ctx context.Context, imagePath string) (*visionOCRResult, error) {
	bin := expenseOCRBinPath()
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("vision-ocr binary not found at %s (set EXPENSE_OCR_BIN): %w", bin, err)
	}
	timeout := expenseOCRTimeout()
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, bin, imagePath, "json",
		"--lang", "zh-Hans,en-US",
		"--no-bbox",
	)
	stdout, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("vision-ocr exited %d: %s",
				exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("vision-ocr timed out after %s on %s", timeout, imagePath)
		}
		return nil, fmt.Errorf("vision-ocr exec: %w", err)
	}
	var out visionOCRResult
	if err := json.Unmarshal(stdout, &out); err != nil {
		return nil, fmt.Errorf("vision-ocr JSON decode: %w (output: %q)", err, truncateForLog(string(stdout), 200))
	}
	return &out, nil
}
