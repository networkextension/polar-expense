package expense

// stubs.go — placeholder shims for dock-internal helpers that the
// extracted handler code references. The extraction PR keeps these
// as SDK-wired (where possible) + log-only TODOs (where the SDK
// doesn't have a path yet) so the package compiles + boots.
//
// Each block names which dock function/type it stubs and what the
// real wiring will look like.

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/networkextension/polar-sdk"
)

// ---- LLM dispatch types --------------------------------------------
//
// Originally lived in dock's ai_agent.go. We keep the shape so the
// extracted code compiles; requestChatCompletion is stubbed (see
// aiAgentStub below).

type aiRuntimeConfig struct {
	APIKey       string
	BaseURL      string
	Model        string
	SystemPrompt string
	ProxyURL     string
	Endpoint     string
	WorkspaceID  string
}

type aiChatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatCompletionRequest struct {
	Model     string                    `json:"model"`
	Messages  []aiChatCompletionMessage `json:"messages"`
	MaxTokens int                       `json:"max_tokens,omitempty"`
}

type aiChatCompletionChoice struct {
	Message aiChatCompletionMessage `json:"message"`
}

type aiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type aiChatCompletionResponse struct {
	Choices []aiChatCompletionChoice `json:"choices"`
	Usage   *aiUsage                 `json:"usage,omitempty"`
}

// aiAgentStub satisfies the surface expense_extract*.go poke at:
//   - requestChatCompletion (sync chat completion — TODO; SDK doesn't
//     expose a sync wrapper yet)
//   - httpClientFor (per-proxy HTTP client; defaults to a shared
//     90s-timeout client today since the multimodal path manages
//     its own context deadline)
type aiAgentStub struct {
	dock      *sdk.Client
	defaultHC *http.Client
}

func (a *aiAgentStub) requestChatCompletion(rc aiRuntimeConfig, payload aiChatCompletionRequest) (*aiChatCompletionResponse, error) {
	// TODO(extract): SDK has no synchronous chat-completion path
	// today. Dock's requestChatCompletion talked to the upstream
	// LLM directly with cost accounting via recordAgentLLMCall.
	// Two viable wirings:
	//   (a) call dock's /api/proxy/v1/chat/completions through
	//       p.Dock.Do with the end-user's access_token (multi-hop)
	//   (b) add a new SDK surface ChatCompletion(workspaceID, cfgID,
	//       msgs) that resolves api_key + records cost in one call
	// Until then return a typed error so expense_extract.go fails
	// clean (the multimodal path still works because it talks to
	// the LLM directly).
	log.Printf("expense: TODO(extract) requestChatCompletion endpoint=%s model=%s ws=%s — needs SDK ChatCompletion or direct /api/proxy/v1/chat/completions call",
		rc.Endpoint, rc.Model, rc.WorkspaceID)
	return nil, errChatCompletionNotWired
}

// httpClientFor — packtunnel/projects don't need this; expense does
// because the multimodal path picks an HTTP client per-proxy URL.
// Returns a shared 90s-timeout client; proxyURL is recorded in the
// log line so future per-proxy routing can be added without changing
// the call sites.
func (a *aiAgentStub) httpClientFor(proxyURL string) *http.Client {
	if strings.TrimSpace(proxyURL) != "" {
		log.Printf("expense: TODO(extract) httpClientFor proxy=%s — returning default client (per-proxy routing not yet wired)", proxyURL)
	}
	if a == nil || a.defaultHC == nil {
		return &http.Client{Timeout: 120 * time.Second}
	}
	return a.defaultHC
}

// ---- Plugin glue ---------------------------------------------------

func (p *Plugin) attachAIAgent() {
	if p.aiAgent == nil {
		p.aiAgent = &aiAgentStub{
			dock:      p.Dock,
			defaultHC: &http.Client{Timeout: 120 * time.Second},
		}
	}
}

// ---- Cross-domain LLM helpers --------------------------------------

// resolveSystemAgentLLM — was dock's resolver picking the workspace's
// system-agent default LLM (the same one powering thread_summary /
// task_sharpen). Stub: returns errNotWired so the OCR-then-LLM
// path fails clean. Multimodal path doesn't use this — it goes
// directly through getAvailableLLMConfigWithAPIKey.
//
// Real wiring: needs a new SDK surface
// p.Dock.SystemAgentLLMResolve(workspaceID) → (cfgID, model).
func (p *Plugin) resolveSystemAgentLLM(workspaceID string) (aiRuntimeConfig, int64, error) {
	log.Printf("expense: TODO(extract) resolveSystemAgentLLM workspace=%s — no SDK surface yet, returning ErrNotWired", workspaceID)
	return aiRuntimeConfig{}, 0, errSystemAgentLLMNotWired
}

// getAvailableLLMConfigWithAPIKey — was dock's resolve-cfg-and-decrypt
// path. SDK's LLMConfigGet returns plaintext APIKey, so we can wire
// it directly. The workspace check is delegated to dock.
func (p *Plugin) getAvailableLLMConfigWithAPIKey(workspaceID string, configID int64) (*expenseLLMConfig, string, error) {
	cfg, err := p.Dock.LLMConfigGet(configID, workspaceID)
	if err != nil {
		return nil, "", err
	}
	if cfg == nil {
		return nil, "", nil
	}
	out := &expenseLLMConfig{
		ID:       cfg.ID,
		Name:     cfg.Name,
		BaseURL:  cfg.BaseURL,
		Model:    cfg.Model,
		ProxyURL: cfg.ProxyURL,
	}
	return out, cfg.APIKey, nil
}

// expenseLLMConfig is the projection of sdk.LLMConfig that
// expense_extract_multimodal.go consumes. Kept separate so a future
// schema change in the SDK doesn't ripple through the multimodal
// path unnecessarily.
type expenseLLMConfig struct {
	ID       int64
	Name     string
	BaseURL  string
	Model    string
	ProxyURL string
}

// ensureLLMConfigTeamQuota — was dock's marketplace-quota seed. The
// extracted svc doesn't own the llm_billing_* tables, so this is a
// no-op + log. checkChatBillingGate below also no-ops, so the
// multimodal path runs without quota checks until either:
//   (a) dock exposes the gate via SDK, or
//   (b) we migrate the billing tables into polar_expense
func (p *Plugin) ensureLLMConfigTeamQuota(workspaceID string, cfgID int64) error {
	log.Printf("expense: TODO(extract) ensureLLMConfigTeamQuota ws=%s cfg=%d — no-op (billing tables stay in dock)", workspaceID, cfgID)
	return nil
}

// checkChatBillingGate — was dock's per-workspace + per-cfg budget
// gate. Stub: always allow (returns blocked=false). The cost still
// gets recorded via recordAgentLLMCall so the dock-side ledger stays
// honest; the gate just doesn't trip from here.
func (p *Plugin) checkChatBillingGate(workspaceID string, cfgID int64) (bool, string) {
	// no log spam on the hot path — every from-image upload would
	// emit one. The TODO is captured in ensureLLMConfigTeamQuota.
	return false, ""
}

// recordAgentLLMCall — was dock's cost-ledger helper. SDK exposes
// AgentLLMCallRecord with the same shape; wire straight through so
// the multimodal vision calls show up in /llm-billing.html.
func (p *Plugin) recordAgentLLMCall(workspaceID string, cfgID int64, modelRequested, modelResolved string,
	promptTokens, completionTokens, totalTokens, latencyMS, statusCode int,
	errorText string, costOverrideUSD *float64) error {
	return p.Dock.AgentLLMCallRecord(sdk.AgentLLMCallRecord{
		WorkspaceID:      workspaceID,
		LLMConfigID:      cfgID,
		ModelRequested:   modelRequested,
		ModelResolved:    modelResolved,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		LatencyMS:        latencyMS,
		StatusCode:       statusCode,
		ErrorText:        errorText,
		CostOverrideUSD:  costOverrideUSD,
	})
}

// ---- Sentinel errors ----------------------------------------------

type stubErr string

func (e stubErr) Error() string { return string(e) }

const (
	errChatCompletionNotWired = stubErr("expense: chat completion not yet wired in extracted svc (TODO: SDK ChatCompletion or direct /api/proxy/v1/chat/completions)")
	errSystemAgentLLMNotWired = stubErr("expense: system-agent LLM resolver not yet wired in extracted svc (TODO: SDK SystemAgentLLMResolve)")
)
