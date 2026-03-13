package main

import (
	"log"
	"net/http"
	"time"

	cfgpkg "aiblog/config"
	"aiblog/internal/agent"
	"aiblog/internal/api"
	"aiblog/internal/blog"
	"aiblog/internal/llm"
	"aiblog/internal/observability"
	"aiblog/internal/rag"
)

func main() {
	cfg, err := cfgpkg.Load("config/config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	blogRepo := blog.NewRepository(cfg.Blog.Root)
	if err := blogRepo.EnsureStructure(); err != nil {
		log.Fatalf("init blog dirs: %v", err)
	}
	logger, closer, err := observability.NewFileLogger(".")
	if err != nil {
		log.Fatalf("init file logger: %v", err)
	}
	defer closer.Close()
	logger.Printf("startup begin model_provider=%s model=%s blog_root=%s", cfg.ModelProvider, cfg.Model, cfg.Blog.Root)

	r := rag.NewBuilder(cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap, cfg.RAG.TopK)
	provider, err := cfg.ActiveProvider()
	if err != nil {
		log.Fatalf("active provider: %v", err)
	}
	llmClient := llm.NewClient(
		provider.BaseURL,
		provider.WireAPI,
		cfg.APIKey(),
		cfg.Model,
		time.Duration(provider.TimeoutSeconds)*time.Second,
		provider.MaxRetries,
	)
	agentSvc := agent.NewService(llmClient, blogRepo, r, logger)

	server := api.NewServer(blogRepo, agentSvc, r, "web", logger)
	if err := server.InitRAG(); err != nil {
		log.Printf("warn: initial rag indexing failed: %v", err)
		logger.Printf("startup rag_init error=%v", err)
	}
	logger.Printf("startup complete address=%s provider=%s base_url=%s wire_api=%s timeout_seconds=%d max_retries=%d", cfg.Server.Address, provider.Name, provider.BaseURL, provider.WireAPI, provider.TimeoutSeconds, provider.MaxRetries)

	log.Printf("AIBlog running on %s", cfg.Server.Address)
	if err := http.ListenAndServe(cfg.Server.Address, server.Routes()); err != nil {
		logger.Printf("server fatal error=%v", err)
		log.Fatalf("server failed: %v", err)
	}
}
