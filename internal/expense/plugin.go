// Package expense is the household-ledger plugin (家庭账本).
// L1 = CRUD over expense_categories + expenses. L2 adds OCR (Apple
// Vision) + LLM extraction + an optional multimodal vision-LLM path
// when EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID is set.
//
// The plugin owns its own DB (polar_expense) + on-disk blob store
// (BlobDir/expense-images/<sha[0:2]>/<sha><ext>). All cross-domain
// resolves (workspace, system user id, llm config + api key, agent
// cost ledger) go through polar-sdk → dock /internal/v1/*.
package expense

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/networkextension/polar-sdk"
)

type Plugin struct {
	DB         *sql.DB
	Dock       *sdk.Client
	Name       string
	Listen     string
	Ver        string
	BlobDir    string // $POLAR_EXPENSE_BLOB_DIR — expense-images/ subdir
	MetricsTok string

	// SystemWorkspaceID is dock's "system" user's personal team. Most
	// expense routes are workspace-scoped via the end user's session
	// (requireWorkspaceID), but cron/maintenance paths in the future
	// will want a stable workspace handle. Pre-fetched once at New()
	// to avoid hitting dock on every request.
	SystemWorkspaceID string

	// aiAgent — stubbed LLM dispatch (see stubs.go). expense_extract.go
	// calls aiAgent.requestChatCompletion which is TODO-wired today;
	// expense_extract_multimodal.go talks HTTP directly + uses the SDK
	// to record cost.
	aiAgent *aiAgentStub

	metrics   *expenseMetrics
	startedAt time.Time
}

type Config struct {
	DBDSN        string
	DockBase     string
	PluginName   string
	PluginToken  string
	Listen       string
	BuildVersion string
	BlobDir      string
	MetricsToken string
}

func New(ctx context.Context, cfg Config) (*Plugin, error) {
	cfg.PluginName = strings.TrimSpace(cfg.PluginName)
	if cfg.PluginName == "" {
		cfg.PluginName = "expense"
	}
	if strings.TrimSpace(cfg.DBDSN) == "" {
		return nil, errors.New("expense.New: DBDSN required")
	}
	if strings.TrimSpace(cfg.DockBase) == "" {
		return nil, errors.New("expense.New: DockBase required")
	}
	if strings.TrimSpace(cfg.PluginToken) == "" {
		return nil, errors.New("expense.New: PluginToken required")
	}
	if strings.TrimSpace(cfg.BlobDir) == "" {
		return nil, errors.New("expense.New: BlobDir required")
	}

	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("open polar_expense: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping polar_expense: %w", err)
	}

	dock := sdk.NewClient(cfg.DockBase, cfg.PluginName, sdk.DeriveHMACKey(cfg.PluginToken))
	resp, err := dock.Do(http.MethodGet, "/internal/v1/ping", nil)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("dock ping: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_ = db.Close()
		return nil, fmt.Errorf("dock /ping rejected: HTTP %d", resp.StatusCode)
	}

	// Pre-fetch the system workspace id so future cron paths don't have
	// to round-trip dock per call. Warn + continue on miss; the current
	// HTTP handlers all derive workspace from the end user's session so
	// a missing system workspace is non-fatal at boot.
	sysWS, err := dock.Do(http.MethodGet, "/internal/v1/users/system/workspace", nil)
	systemWorkspaceID := ""
	if err == nil {
		var body struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := readJSON(sysWS, &body); err == nil {
			systemWorkspaceID = body.WorkspaceID
		}
	}
	if systemWorkspaceID == "" {
		log.Printf("expense: WARN system workspace lookup failed (err=%v) — cron paths will be no-ops until dock bootstraps system user", err)
	}

	p := &Plugin{
		DB:                db,
		Dock:              dock,
		Name:              cfg.PluginName,
		Listen:            cfg.Listen,
		Ver:               cfg.BuildVersion,
		BlobDir:           cfg.BlobDir,
		MetricsTok:        cfg.MetricsToken,
		SystemWorkspaceID: systemWorkspaceID,
		metrics:           newExpenseMetrics(),
		startedAt:         time.Now(),
	}
	p.attachAIAgent()
	return p, nil
}

// readJSON — minimal JSON unmarshaller; SDK has the same helper but
// it's unexported. Local copy avoids the import-cycle gymnastics.
func readJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	return dec.Decode(out)
}

func (p *Plugin) RegisterRoutes(r gin.IRouter) {
	r.GET("/healthz", p.handleHealthz)
	r.GET("/metrics", p.handleMetricsExposition)

	// /api/expense-categories/* + /api/expenses/* — mirrors dock's
	// /api/expense* prefixes so nginx can flip with a single
	// proxy_pass (see scripts/nginx/expense-svc-snippet.conf). All
	// routes require an authed session; auth + workspace resolution
	// happens via requireAuthViaDock (same pattern as packtunnel auth).
	api := r.Group("/api", p.requireAuthViaDock())
	{
		api.GET("/expense-categories", p.handleExpenseCategoryList)
		api.POST("/expense-categories", p.handleExpenseCategoryCreate)
		api.PUT("/expense-categories/:id", p.handleExpenseCategoryUpdate)
		api.DELETE("/expense-categories/:id", p.handleExpenseCategoryDelete)

		api.GET("/expenses", p.handleExpenseList)
		api.POST("/expenses", p.handleExpenseCreate)
		api.PUT("/expenses/:id", p.handleExpenseUpdate)
		api.DELETE("/expenses/:id", p.handleExpenseDelete)

		api.POST("/expenses/from-image", p.handleExpenseFromImage)
		api.GET("/expenses/:id/image", p.handleExpenseImageDownload)
	}
}

func (p *Plugin) Start(ctx context.Context) {
	go p.heartbeatLoop(ctx)
}

func (p *Plugin) Close() error {
	if p.DB != nil {
		return p.DB.Close()
	}
	return nil
}

func (p *Plugin) handleHealthz(c *gin.Context) {
	dbOK := true
	if err := p.DB.PingContext(c.Request.Context()); err != nil {
		dbOK = false
	}
	status := http.StatusOK
	if !dbOK {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"plugin":         p.Name,
		"version":        p.Ver,
		"uptime_seconds": int64(time.Since(p.startedAt).Seconds()),
		"db_ok":          dbOK,
		"blob_dir":       p.BlobDir,
		"go":             runtime.Version(),
	})
}

func (p *Plugin) handleMetricsExposition(c *gin.Context) {
	if p.MetricsTok == "" {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if c.GetHeader("Authorization") != "Bearer "+p.MetricsTok {
		c.Header("WWW-Authenticate", `Bearer realm="metrics"`)
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	promhttp.HandlerFor(p.metrics.registry, promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
}

func (p *Plugin) heartbeatLoop(ctx context.Context) {
	p.beat(ctx)
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.beat(ctx)
		}
	}
}

func (p *Plugin) beat(_ context.Context) {
	err := p.Dock.Heartbeat(sdk.HeartbeatOpts{
		Version:       p.Ver,
		Endpoint:      p.Listen,
		UptimeSeconds: int64(time.Since(p.startedAt).Seconds()),
	})
	if err != nil {
		log.Printf("expense: heartbeat failed: %v", err)
	}
}

type expenseMetrics struct {
	registry *prometheus.Registry
	upGauge  prometheus.Gauge
}

func newExpenseMetrics() *expenseMetrics {
	m := &expenseMetrics{registry: prometheus.NewRegistry()}
	m.upGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "polar_expense_up",
		Help: "Always 1 while expense-svc is serving.",
	})
	m.registry.MustRegister(m.upGauge)
	m.upGauge.Set(1)
	return m
}
