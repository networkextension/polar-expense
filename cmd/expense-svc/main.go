// Command expense-svc is the household-ledger plugin binary.
//
// Env vars:
//
//	POLAR_EXPENSE_DB_DSN       postgres://ideamesh:test123456@127.0.0.1:5432/polar_expense?sslmode=disable
//	POLAR_DOCK_BASE            http://127.0.0.1:8080
//	POLAR_PLUGIN_NAME          expense
//	POLAR_PLUGIN_TOKEN         polar_plugin_…   (plaintext from /admin-plugins.html)
//	POLAR_EXPENSE_LISTEN       127.0.0.1:8097
//	POLAR_EXPENSE_VERSION      git-sha or "0.0.1"
//	POLAR_EXPENSE_BLOB_DIR     /Users/local/expense-svc-data   (holds expense-images/)
//	POLAR_EXPENSE_METRICS_TOKEN  bearer for /metrics; unset = 404
//
// OCR + LLM env (consumed inside the plugin, not Config-bound):
//
//	EXPENSE_OCR_BIN                      /Users/local/.local/bin/vision-ocr   (Apple Vision CLI)
//	EXPENSE_OCR_TIMEOUT_SEC              60
//	EXPENSE_EXTRACT_MULTIMODAL_LLM_CONFIG_ID  int (set → multimodal vision-LLM path)
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"

	"github.com/networkextension/polar-expense/internal/expense"
)

func main() {
	cfg := expense.Config{
		DBDSN:        envOrDefault("POLAR_EXPENSE_DB_DSN", "postgres://ideamesh:test123456@127.0.0.1:5432/polar_expense?sslmode=disable"),
		DockBase:     envOrDefault("POLAR_DOCK_BASE", "http://127.0.0.1:8080"),
		PluginName:   envOrDefault("POLAR_PLUGIN_NAME", "expense"),
		PluginToken:  os.Getenv("POLAR_PLUGIN_TOKEN"),
		Listen:       envOrDefault("POLAR_EXPENSE_LISTEN", "127.0.0.1:8097"),
		BuildVersion: envOrDefault("POLAR_EXPENSE_VERSION", "0.0.1"),
		BlobDir:      envOrDefault("POLAR_EXPENSE_BLOB_DIR", "/Users/local/expense-svc-data"),
		MetricsToken: os.Getenv("POLAR_EXPENSE_METRICS_TOKEN"),
	}
	if strings.TrimSpace(cfg.PluginToken) == "" {
		log.Fatal("POLAR_PLUGIN_TOKEN unset — get plaintext from /admin-plugins.html")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	plugin, err := expense.New(ctx, cfg)
	if err != nil {
		log.Fatalf("expense.New: %v", err)
	}
	defer plugin.Close()

	gin.SetMode(envOrDefault("GIN_MODE", gin.ReleaseMode))
	r := gin.New()
	r.Use(gin.Recovery())
	plugin.RegisterRoutes(r)
	plugin.Start(ctx)

	srv := &http.Server{Addr: cfg.Listen, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("expense-svc listening on %s (dock=%s, name=%s, ver=%s, blob=%s)",
			cfg.Listen, cfg.DockBase, cfg.PluginName, cfg.BuildVersion, cfg.BlobDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe: %v", err)
		}
	}()

	<-ctx.Done()
	log.Print("expense-svc: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("expense-svc: shutdown: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
