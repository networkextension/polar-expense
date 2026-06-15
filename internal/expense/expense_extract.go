package expense

// expense_extract.go — orchestrates OCR → LLM structuring → draft Expense
// for the upload-receipt flow. Called by handleExpenseFromImage in
// expense_handlers.go.
//
// Two ASC-style failure modes:
//   - ocr error: vision-ocr crashed or returned bad JSON. Caller maps to 502.
//   - llm error: workspace has no LLM configured / hit quota. Caller maps to
//     a typed error so the UI can show a useful hint.
//
// On success we return a *fully-populated* draft Expense (status=0,
// status remains so the user has to confirm). The handler inserts it and
// returns the id; UI navigates the user to a confirm modal pre-filled
// with the LLM's extraction.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

// ExpenseExtractDraft is the parsed-and-validated LLM output, used to
// build the Expense row. Field semantics mirror prod.md / design.md.
type ExpenseExtractDraft struct {
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
	Merchant      string  `json:"merchant"`
	CategoryID    *int64  `json:"category_id"`
	ConsumeTime   string  `json:"consume_time"`    // "YYYY-MM-DD HH:mm" or "YYYY-MM-DD"
	HasDetailTime bool    `json:"has_detail_time"`
	Region        string  `json:"region"`
	Confidence    int     `json:"confidence"`      // 0-100 self-assessment
	Remark        string  `json:"remark"`
}

// extractExpenseFromImage runs the full OCR → LLM pipeline. workspaceID
// gates which LLM gets used + which categories the LLM sees. imagePath
// is the absolute path of the staged temp file (os.TempDir).
func (p *Plugin) extractExpenseFromImage(ctx context.Context, workspaceID, imagePath string) (*ExpenseExtractDraft, error) {
	if strings.TrimSpace(imagePath) == "" {
		return nil, errEmptyExpenseImage
	}

	// 1) OCR — local Apple Vision, no quota.
	ocrStart := time.Now()
	ocrResult, err := runVisionOCR(ctx, imagePath)
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	rawText := strings.TrimSpace(ocrResult.FullText)
	log.Printf("expense extract: ocr ok records=%d chars=%d elapsed=%s", ocrResult.RecordCount, len(rawText), time.Since(ocrStart))
	if rawText == "" {
		return nil, errors.New("ocr: no text recognized in image")
	}

	// 2) Workspace categories — the LLM picks one of these ids when it
	// can. Listing only non-hidden so suggestions stay relevant.
	cats, err := p.listExpenseCategories(workspaceID, false)
	if err != nil {
		return nil, fmt.Errorf("load categories: %w", err)
	}
	catLines := make([]string, 0, len(cats))
	for _, c := range cats {
		catLines = append(catLines, fmt.Sprintf("  %d  %s %s", c.ID, c.Icon, c.Name))
	}

	// 3) LLM — use the workspace's system-agent default LLM (same one
	// powering thread_summary / task_sharpen). Falls through B5 quota
	// gates automatically.
	runtimeCfg, _, err := p.resolveSystemAgentLLM(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	runtimeCfg.WorkspaceID = workspaceID
	runtimeCfg.Endpoint = "expense_extract"
	runtimeCfg.SystemPrompt = expenseExtractSystemPrompt

	userPrompt := fmt.Sprintf(`OCR 文本：
"""
%s
"""

可选分类（请从下列 id 中选一个最匹配的；找不到合适的就给 null）：
%s

请只输出符合 schema 的 JSON，不要包裹 markdown 代码块、不要说明。`,
		truncateForLog(rawText, 4000), strings.Join(catLines, "\n"))

	llmStart := time.Now()
	resp, err := p.aiAgent.requestChatCompletion(runtimeCfg, aiChatCompletionRequest{
		Model: runtimeCfg.Model,
		Messages: []aiChatCompletionMessage{
			{Role: "system", Content: expenseExtractSystemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens: 512,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	if resp == nil || len(resp.Choices) == 0 {
		return nil, errors.New("llm: empty response")
	}
	body := strings.TrimSpace(resp.Choices[0].Message.Content)
	log.Printf("expense extract: llm ok elapsed=%s tokens_in≈%d body_len=%d", time.Since(llmStart),
		usagePromptTokens(resp.Usage), len(body))

	// 4) Parse — LLMs sometimes wrap JSON in ```json fences. Strip them
	// then unmarshal. If it's still not valid JSON, return the raw body
	// so the operator can debug from the audit log.
	draft, err := parseExpenseDraftJSON(body)
	if err != nil {
		return nil, fmt.Errorf("llm: parse JSON: %w (raw: %s)", err, truncateForLog(body, 200))
	}
	if draft.Amount <= 0 {
		return nil, fmt.Errorf("llm: amount missing or <= 0 (raw: %s)", truncateForLog(body, 200))
	}
	return draft, nil
}

// expenseExtractSystemPrompt — kept inline (not a prompts-table slug)
// since the format JSON contract has to match parseExpenseDraftJSON.
// Easy to externalize later if we want admin-tunable phrasing.
const expenseExtractSystemPrompt = `你是中文消费凭证结构化助手。输入是 OCR 提取的微信/支付宝/小票/银行短信文本，输出一个 JSON 描述这笔消费。

JSON schema（严格遵守，必须只输出 JSON，不要 markdown 代码块，不要解释）：
{
  "amount": float,              // CNY 金额，必填，> 0
  "currency": "CNY",            // 多币种 v2 再做
  "merchant": string,           // 商户名，简洁，如 "山姆会员店" "全家便利店"，找不到留空字符串
  "category_id": int|null,      // 从用户给的可选列表里挑一个 id，找不到合适的给 null
  "consume_time": string,       // "YYYY-MM-DD HH:mm" 或仅 "YYYY-MM-DD"（凭证里没具体时间时）
  "has_detail_time": boolean,   // true=有时分秒，false=只有日期
  "region": string,             // 城市，如 "北京"，找不到留空
  "confidence": int,            // 0-100 自评，关键字段缺失/猜的多就给低分
  "remark": string              // 任何重要的额外信息，如订单号末四位、备注，没就空
}

注意事项：
- 微信支付截图里的"-26.00 元"是支出金额，按正数返回 26.00
- 时间优先取"交易时间"或"付款时间"，跳过"消费时间"以外的辅助时间戳
- 凭证里的"商品名"或"订单标题"不是商户名 — 商户通常在标题上方或下方独立一行
- 不要瞎编：找不到就给空字符串/null/0，把 confidence 拉低`

// parseExpenseDraftJSON strips optional ```json fences and decodes.
var jsonFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")

func parseExpenseDraftJSON(body string) (*ExpenseExtractDraft, error) {
	body = strings.TrimSpace(body)
	if m := jsonFenceRe.FindStringSubmatch(body); len(m) == 2 {
		body = strings.TrimSpace(m[1])
	}
	var d ExpenseExtractDraft
	if err := json.Unmarshal([]byte(body), &d); err != nil {
		return nil, err
	}
	if d.Currency == "" {
		d.Currency = "CNY"
	}
	if d.Confidence == 0 {
		d.Confidence = 50 // benign default — UI lights it amber, prompting review
	}
	return &d, nil
}

func usagePromptTokens(u *aiUsage) int {
	if u == nil {
		return 0
	}
	return u.PromptTokens
}

// parseExpenseConsumeTime turns the LLM's free-form date string into a
// time.Time + has_detail flag. Accepts both "YYYY-MM-DD HH:mm" and
// date-only "YYYY-MM-DD". Falls back to time.Now when both fail (and
// flips status to draft so the user can fix it).
func parseExpenseConsumeTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Now().UTC(), false
	}
	formats := []struct {
		layout    string
		hasDetail bool
	}{
		{"2006-01-02 15:04:05", true},
		{"2006-01-02 15:04", true},
		{"2006/01/02 15:04:05", true},
		{"2006/01/02 15:04", true},
		{"2006-01-02", false},
		{"2006/01/02", false},
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f.layout, s, time.Local); err == nil {
			return t, f.hasDetail
		}
	}
	return time.Now().UTC(), false
}

