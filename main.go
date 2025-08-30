package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/adrianpk/ghp/internal/ghp"
)

//go:embed all:prompts
var embeddedFS embed.FS

func main() {
	cfgPath := flag.String("config", "./config.yml", "path to YAML config")
	user := flag.String("user", "", "GitHub username/handle")
	provider := flag.String("provider", "", "AI provider: openai or gemini")
	flag.Parse()
	if *user == "" {
		log.Fatal("missing --user")
	}

	cfg, err := ghp.LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	if *provider != "" {
		cfg.LLM.Provider = *provider
	}

	if err := os.MkdirAll(cfg.App.OutDir, 0o755); err != nil {
		log.Fatalf("mkdir out: %v", err)
	}

	llmClient, err := ghp.NewLLMClient(cfg)
	if err != nil {
		log.Fatalf("llm: %v", err)
	}

	svc, err := ghp.NewService(cfg, llmClient, embeddedFS)
	if err != nil {
		log.Fatalf("service: %v", err)
	}

	ctx := context.Background()
	html, err := svc.Submit(ctx, *user)
	if err != nil {
		log.Fatalf("submit: %v", err)
	}

	out := filepath.Join(cfg.App.OutDir, fmt.Sprintf("profile-%s.html", *user))
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}

	fmt.Println("Report:", out)
}
