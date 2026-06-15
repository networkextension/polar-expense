package expense

// Receipt-image constants. Bytes live exclusively in the central
// polar-assets catalog (see expenses_assets.go); this file only holds the
// accepted formats + size cap shared by the upload handler + asset upload.

import "errors"

var allowedExpenseImageExts = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".heic": "image/heic",
	".heif": "image/heif",
	".webp": "image/webp",
}

const expenseImageMaxBytes = 25 * 1024 * 1024 // 25 MiB; receipts are tiny, anything bigger smells like a screenshot mistake

var errEmptyExpenseImage = errors.New("empty expense image")
