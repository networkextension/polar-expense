package expense

// L1 HTTP handlers for the expense module. Workspace-scoped via
// requireWorkspaceID; auth via AuthMiddleware (every workspace member
// has read+write — no admin gate, this is a household ledger).
//
// OCR / LLM extraction lives in expense_extract.go (L2 — separate PR).

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ---- categories ----

func (p *Plugin) handleExpenseCategoryList(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	// First read for a workspace seeds the 7 presets so the user never
	// sees an empty dropdown.
	items, err := p.listExpenseCategories(workspaceID, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	if len(items) == 0 {
		if err := p.bootstrapExpenseCategories(workspaceID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "无法初始化默认分类"})
			return
		}
		items, err = p.listExpenseCategories(workspaceID, true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"categories": items})
}

func (p *Plugin) handleExpenseCategoryCreate(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	var req struct {
		Name      string `json:"name" binding:"required"`
		Icon      string `json:"icon"`
		Color     string `json:"color"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入"})
		return
	}
	cat, err := p.createExpenseCategory(workspaceID, req.Name, req.Icon, req.Color, req.SortOrder)
	if err != nil {
		// Most likely a UNIQUE-violation on (workspace_id, name).
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") || strings.Contains(err.Error(), "unique") {
			c.JSON(http.StatusConflict, gin.H{"error": "分类名已存在"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"category": cat})
}

func (p *Plugin) handleExpenseCategoryUpdate(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	var req struct {
		Name      *string `json:"name"`
		Icon      *string `json:"icon"`
		Color     *string `json:"color"`
		SortOrder *int    `json:"sort_order"`
		IsHidden  *bool   `json:"is_hidden"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入"})
		return
	}
	if err := p.updateExpenseCategory(id, workspaceID, ExpenseCategoryUpdate{
		Name: req.Name, Icon: req.Icon, Color: req.Color,
		SortOrder: req.SortOrder, IsHidden: req.IsHidden,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (p *Plugin) handleExpenseCategoryDelete(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	// ON DELETE SET NULL on expenses.category_id keeps the flow rows alive.
	if err := p.deleteExpenseCategory(id, workspaceID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---- expenses ----

func (p *Plugin) handleExpenseList(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	filter := ExpenseListFilter{}
	if v := c.Query("start"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.Start = &t
		} else if t, err := time.Parse("2006-01-02", v); err == nil {
			filter.Start = &t
		}
	}
	if v := c.Query("end"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			filter.End = &t
		} else if t, err := time.Parse("2006-01-02", v); err == nil {
			// End is half-open; bump to next day so 2026-05-31 includes
			// transactions on that day.
			t = t.Add(24 * time.Hour)
			filter.End = &t
		}
	}
	if v := c.Query("category_id"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			filter.CategoryID = &n
		}
	}
	if v := c.Query("status"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Status = &n
		}
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := c.Query("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}
	items, total, err := p.listExpenses(workspaceID, filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"expenses": items,
		"total":    total,
	})
}

func (p *Plugin) handleExpenseCreate(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	userIDAny, _ := c.Get("user_id")
	userID, _ := userIDAny.(string)
	if strings.TrimSpace(userID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}
	var req struct {
		Amount        float64 `json:"amount" binding:"required"`
		Currency      string  `json:"currency"`
		Merchant      string  `json:"merchant"`
		CategoryID    *int64  `json:"category_id"`
		ConsumeTime   string  `json:"consume_time"` // RFC3339 or YYYY-MM-DD
		HasDetailTime bool    `json:"has_detail_time"`
		Region        string  `json:"region"`
		Remark        string  `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入"})
		return
	}
	if req.Amount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "金额必须大于 0"})
		return
	}
	e := &Expense{
		WorkspaceID:     workspaceID,
		CreatedByUserID: userID,
		Amount:          req.Amount,
		Currency:        req.Currency,
		Merchant:        strings.TrimSpace(req.Merchant),
		CategoryID:      req.CategoryID,
		HasDetailTime:   req.HasDetailTime,
		Region:          strings.TrimSpace(req.Region),
		Status:          ExpenseStatusConfirmed, // manual entry = trusted
		Remark:          strings.TrimSpace(req.Remark),
	}
	if req.ConsumeTime != "" {
		if t, err := time.Parse(time.RFC3339, req.ConsumeTime); err == nil {
			e.ConsumeTime = t
		} else if t, err := time.Parse("2006-01-02", req.ConsumeTime); err == nil {
			e.ConsumeTime = t
			e.HasDetailTime = false // date-only input
		}
	}
	if err := p.createExpense(e); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"expense": e})
}

func (p *Plugin) handleExpenseUpdate(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	var req struct {
		Amount        *float64 `json:"amount"`
		Currency      *string  `json:"currency"`
		Merchant      *string  `json:"merchant"`
		CategoryID    *int64   `json:"category_id"`
		ClearCategory bool     `json:"clear_category"` // explicit clear vs leave-alone
		ConsumeTime   *string  `json:"consume_time"`
		HasDetailTime *bool    `json:"has_detail_time"`
		Region        *string  `json:"region"`
		Status        *int     `json:"status"`
		Remark        *string  `json:"remark"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效的输入"})
		return
	}
	u := ExpenseUpdate{
		Amount:        req.Amount,
		Currency:      req.Currency,
		Merchant:      req.Merchant,
		CategoryID:    req.CategoryID,
		ClearCategory: req.ClearCategory,
		HasDetailTime: req.HasDetailTime,
		Region:        req.Region,
		Status:        req.Status,
		Remark:        req.Remark,
	}
	if req.ConsumeTime != nil {
		if t, err := time.Parse(time.RFC3339, *req.ConsumeTime); err == nil {
			u.ConsumeTime = &t
		} else if t, err := time.Parse("2006-01-02", *req.ConsumeTime); err == nil {
			u.ConsumeTime = &t
		}
	}
	if err := p.updateExpense(id, workspaceID, u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (p *Plugin) handleExpenseDelete(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	if err := p.deleteExpense(id, workspaceID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// handleExpenseFromImage — L2 upload flow. multipart `file` field
// (jpg/png/heic/webp ≤ 25 MiB). Saves the image, runs OCR + LLM
// extraction, inserts a status=0 draft expense, returns the row so the
// UI can pre-fill its confirm modal. Heavy-lifting is in extract_*.go;
// this handler is just the HTTP shell.
func (p *Plugin) handleExpenseFromImage(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	userIDAny, _ := c.Get("user_id")
	userID, _ := userIDAny.(string)
	if strings.TrimSpace(userID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 file 字段"})
		return
	}
	if file.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件为空"})
		return
	}
	if file.Size > expenseImageMaxBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "图片超过 25 MiB 上限"})
		return
	}

	// Stage to an ephemeral temp file: OCR/extract reads it, then it's
	// single-written to assets + removed. No expense-svc-local blob storage.
	stage := filepath.Join(os.TempDir(), "expense-staging-"+generateResourceID()+filepath.Ext(file.Filename))
	if err := c.SaveUploadedFile(file, stage); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件保存失败"})
		return
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if _, ok := allowedExpenseImageExts[ext]; !ok {
		_ = os.Remove(stage)
		c.JSON(http.StatusBadRequest, gin.H{"error": "不支持的图片格式（仅 jpg/png/heic/webp）"})
		return
	}
	// Single-write: the asset platform owns the bytes; the staged file is
	// transient (OCR reads it, then it's uploaded to assets + removed).
	defer os.Remove(stage)
	log.Printf("expense from-image: workspace=%s user=%s file=%s", workspaceID, userID, file.Filename)

	// Extract. Pipeline branch:
	//   EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID set
	//     → multimodal vision LLM directly (better on visual-layout
	//        receipts like 支付宝/微信账单详情 where numbers cluster)
	//   else → OCR (Apple Vision) → text LLM (cheaper, fine for plain
	//        text receipts)
	// Both produce ExpenseExtractDraft. Failures still create a status=0
	// draft (image is preserved, user can manually fill).
	var draft *ExpenseExtractDraft
	var extErr error
	if expenseMultimodalLLMConfigID() > 0 {
		draft, extErr = p.extractExpenseFromImageMultimodal(c.Request.Context(), workspaceID, stage)
		if extErr != nil {
			log.Printf("expense from-image: multimodal failed, falling back to OCR: %v", extErr)
			draft, extErr = p.extractExpenseFromImage(c.Request.Context(), workspaceID, stage)
		}
	} else {
		draft, extErr = p.extractExpenseFromImage(c.Request.Context(), workspaceID, stage)
	}
	// Single-write the receipt to the tenant's assets catalog; the marker
	// goes into raw_image_id. Hard-fail (assets owns the bytes).
	marker, upErr := p.uploadExpenseImageAsset(workspaceID, stage)
	if upErr != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "图片上传失败：" + upErr.Error()})
		return
	}
	e := &Expense{
		WorkspaceID:     workspaceID,
		CreatedByUserID: userID,
		RawImageID:      &marker,
		Status:          ExpenseStatusDraft,
		Currency:        "CNY",
		Confidence:      0,
		ConsumeTime:     time.Now().UTC(),
	}
	if extErr != nil {
		log.Printf("expense from-image: extract failed: %v", extErr)
		e.Remark = "自动抽取失败：" + extErr.Error()
	} else if draft != nil {
		e.Amount = draft.Amount
		e.Currency = draft.Currency
		e.Merchant = draft.Merchant
		e.CategoryID = draft.CategoryID
		e.Region = draft.Region
		e.Confidence = draft.Confidence
		e.Remark = draft.Remark
		// Use the LLM-extracted consume_time as-is. The receipt date is
		// real data — earlier "looks too old" guards were wrong: the
		// list view defaults to 全部 so out-of-month rows still show up,
		// and the user can edit on the draft if anything's off.
		t, hasDetail := parseExpenseConsumeTime(draft.ConsumeTime)
		e.ConsumeTime = t
		e.HasDetailTime = hasDetail
	}
	if err := p.createExpense(e); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	resp := gin.H{"expense": e}
	if extErr != nil {
		resp["extract_error"] = extErr.Error()
		// Classify so the UI can swap the generic alert for a
		// targeted "go top up at /llm-billing.html" banner. Keywords
		// match the strings produced by checkChatBillingGate
		// (llm_billing_store.go:418-441) and the 4b/4c proxy gates.
		msg := extErr.Error()
		if strings.Contains(msg, "本月该 LLM") || strings.Contains(msg, "额度已用完") ||
			strings.Contains(msg, "余额已耗尽") || strings.Contains(msg, "充值余额") ||
			strings.Contains(msg, "team_quota_exceeded") || strings.Contains(msg, "insufficient_credit") {
			resp["extract_error_kind"] = "quota_exhausted"
		}
	}
	c.JSON(http.StatusCreated, resp)
}

// handleExpenseImageDownload serves the original image bytes for a stored
// expense. Workspace-scoped via the expense row's workspace_id, not the
// blob path (since blobs are deduped across workspaces).
func (p *Plugin) handleExpenseImageDownload(c *gin.Context) {
	workspaceID, ok := requireWorkspaceID(c)
	if !ok {
		return
	}
	id, ok := parseInt64Param(c, "id")
	if !ok {
		return
	}
	e, err := p.getExpense(id, workspaceID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器错误"})
		return
	}
	if e == nil || e.RawImageID == nil || *e.RawImageID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "没有原始图片"})
		return
	}
	// Assets-only: receipt bytes live in the tenant's central catalog.
	if !p.streamExpenseImageFromAssets(c, *e.RawImageID) {
		c.JSON(http.StatusBadGateway, gin.H{"error": "图片暂时不可用"})
	}
}
