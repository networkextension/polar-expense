package expense

// Multimodal LLM extraction for the expense module (L2b).
//
// Skips OCR entirely — sends the receipt image straight to a vision-
// capable LLM and asks for the same structured JSON the OCR path
// produces. The vision model sees layout, font weight, colour, icon
// position — all of which disambiguate "266 (amount)" vs "9142 (card
// tail)" vs "15 (积分)" vs "17-digit 订单号" much better than text-only
// OCR can.
//
// Activated when EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID points at a
// vision-capable llm_config (e.g. qwen-vl-max, gemini-pro-vision,
// gpt-4o). When unset, expense_extract.go falls back to OCR→LLM.
//
// Doesn't use aiAgent.requestChatCompletion because aiChatCompletionMessage.Content
// is just a string — the OpenAI multimodal contract needs an array of
// content blocks. Direct HTTP keeps the typed pipeline untouched for the
// other 95% of callers.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const expenseMultimodalDefaultModel = "" // empty = use llm_config.model verbatim

func expenseMultimodalLLMConfigID() int64 {
	return int64(envIntDefault("EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID", 0))
}

// extractExpenseFromImageMultimodal POSTs the image + prompt to the
// configured vision LLM and parses the JSON response into the same
// ExpenseExtractDraft shape as the OCR path. workspaceID is used to
// resolve the LLM config + apply the B5 budget gate via
// recordProxyCallFailure-style — but we DON'T go through the proxy
// pipeline, so the B5 gate is applied manually here.
func (p *Plugin) extractExpenseFromImageMultimodal(ctx context.Context, workspaceID, imagePath string) (*ExpenseExtractDraft, error) {
	cfgID := expenseMultimodalLLMConfigID()
	if cfgID <= 0 {
		return nil, errors.New("multimodal not configured")
	}
	cfg, apiKey, err := p.getAvailableLLMConfigWithAPIKey(workspaceID, cfgID)
	if err != nil || cfg == nil {
		return nil, fmt.Errorf("multimodal llm config %d not accessible to workspace %s: %w", cfgID, workspaceID, err)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("multimodal llm config %d has no api_key set", cfgID)
	}

	// Marketplace flow: the workspace's first extract auto-creates a
	// quota row at the platform default budget. Subsequent calls are
	// gated by checkChatBillingGate against that budget. Idempotent —
	// existing budgets are preserved.
	if err := p.ensureLLMConfigTeamQuota(workspaceID, cfg.ID); err != nil {
		// Non-fatal: log and continue; checkChatBillingGate below will
		// run with whatever state exists. Worst case: no gate trips.
		log.Printf("expense extract: ensureLLMConfigTeamQuota workspace=%s cfg=%d: %v", workspaceID, cfg.ID, err)
	}

	// B5 budget gate — same shape as ai_agent.handleTask's gate. Refuse
	// extraction when the workspace has either exhausted its monthly
	// budget or drained its prepaid balance for this config.
	if blocked, msg := p.checkChatBillingGate(workspaceID, cfg.ID); blocked {
		return nil, errors.New("multimodal: " + msg)
	}

	// Read + base64 the image. data:image/...;base64,... data URL is
	// what every OpenAI-compat vision model accepts.
	imgBytes, err := os.ReadFile(imagePath)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	mime := mimeTypeForExpenseImage(imagePath)
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(imgBytes)

	// Load workspace categories — same prompt context as OCR path.
	cats, err := p.listExpenseCategories(workspaceID, false)
	if err != nil {
		return nil, fmt.Errorf("load categories: %w", err)
	}
	catLines := make([]string, 0, len(cats))
	for _, c := range cats {
		catLines = append(catLines, fmt.Sprintf("  %d  %s %s", c.ID, c.Icon, c.Name))
	}
	userText := fmt.Sprintf(`这是一张消费凭证截图。请仔细看图（金额位置/字号、商户名icon、时间格式），按 schema 输出 JSON。

可选分类（找最匹配的 id，找不到给 null）：
%s

只输出 JSON，不要 markdown 代码块、不要解释。`, strings.Join(catLines, "\n"))

	payload := openAIMultimodalRequest{
		Model: cfg.Model,
		Messages: []openAIMultimodalMessage{
			{Role: "system", Content: []openAIMultimodalPart{{Type: "text", Text: expenseExtractSystemPrompt}}},
			{Role: "user", Content: []openAIMultimodalPart{
				{Type: "image_url", ImageURL: &openAIImageURL{URL: dataURL}},
				{Type: "text", Text: userText},
			}},
		},
		MaxTokens: 512,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// 90s timeout — vision models are slower than text. Caller may have
	// its own deadline; both apply.
	cmdCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cmdCtx, http.MethodPost, cfg.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))

	client := p.aiAgent.httpClientFor(strings.TrimSpace(cfg.ProxyURL))
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("multimodal http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("multimodal read: %w", err)
	}
	elapsed := time.Since(start)
	log.Printf("expense extract: multimodal cfg=%d model=%s status=%d elapsed=%s body_len=%d",
		cfg.ID, cfg.Model, resp.StatusCode, elapsed, len(respBody))

	if resp.StatusCode >= 400 {
		// Record the failure so the team's audit shows the attempt
		// (zero tokens / zero cost since the call never produced output).
		_ = p.recordAgentLLMCall(workspaceID, cfg.ID, cfg.Model, cfg.Model,
			0, 0, 0, int(elapsed.Milliseconds()), resp.StatusCode,
			truncateForLog(string(respBody), 200), nil)
		return nil, fmt.Errorf("multimodal status %d: %s", resp.StatusCode, truncateForLog(string(respBody), 300))
	}

	var apiResp openAIMultimodalResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("multimodal decode: %w (body %s)", err, truncateForLog(string(respBody), 200))
	}
	if len(apiResp.Choices) == 0 {
		return nil, errors.New("multimodal: empty choices")
	}

	// Record the successful call into the agent-path cost ledger so the
	// team's monthly spend updates and the B5 gate fires next time.
	prompt, completion, total := 0, 0, 0
	if apiResp.Usage != nil {
		prompt = apiResp.Usage.PromptTokens
		completion = apiResp.Usage.CompletionTokens
		total = apiResp.Usage.TotalTokens
	}
	if recErr := p.recordAgentLLMCall(workspaceID, cfg.ID, cfg.Model, cfg.Model,
		prompt, completion, total, int(elapsed.Milliseconds()), http.StatusOK, "", nil); recErr != nil {
		log.Printf("expense extract: ledger record failed workspace=%s cfg=%d: %v", workspaceID, cfg.ID, recErr)
	}

	out := strings.TrimSpace(apiResp.Choices[0].Message.Content)
	draft, err := parseExpenseDraftJSON(out)
	if err != nil {
		return nil, fmt.Errorf("multimodal parse JSON: %w (raw: %s)", err, truncateForLog(out, 200))
	}
	if draft.Amount <= 0 {
		return nil, fmt.Errorf("multimodal: amount missing or <= 0 (raw: %s)", truncateForLog(out, 200))
	}
	return draft, nil
}

func mimeTypeForExpenseImage(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

// OpenAI-compat multimodal request/response shapes. Kept local so we
// don't pollute aiAgent's typed structs.
type openAIMultimodalRequest struct {
	Model     string                    `json:"model"`
	Messages  []openAIMultimodalMessage `json:"messages"`
	MaxTokens int                       `json:"max_tokens,omitempty"`
}
type openAIMultimodalMessage struct {
	Role    string                 `json:"role"`
	Content []openAIMultimodalPart `json:"content"`
}
type openAIMultimodalPart struct {
	Type     string           `json:"type"`
	Text     string           `json:"text,omitempty"`
	ImageURL *openAIImageURL  `json:"image_url,omitempty"`
}
type openAIImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "low" | "high" | "auto" — leave empty for provider default
}
type openAIMultimodalResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	// Usage block is OpenAI-compat and what every vision model emits.
	// We thread it into recordAgentLLMCall so cost_usd is populated.
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}
