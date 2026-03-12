package main

import (
	"log"
	"net/http"

	cfgpkg "aiblog/config"
	"aiblog/internal/agent"
	"aiblog/internal/api"
	"aiblog/internal/blog"
	"aiblog/internal/llm"
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

	r := rag.NewBuilder(cfg.RAG.ChunkSize, cfg.RAG.ChunkOverlap, cfg.RAG.TopK)
	provider, err := cfg.ActiveProvider()
	if err != nil {
		log.Fatalf("active provider: %v", err)
	}
	llmClient := llm.NewClient(provider.BaseURL, cfg.APIKey(), cfg.Model)
	agentSvc := agent.NewService(llmClient, blogRepo, r)

	server := api.NewServer(blogRepo, agentSvc, r, "web")
	if err := server.InitRAG(); err != nil {
		log.Printf("warn: initial rag indexing failed: %v", err)
	}

	log.Printf("AIBlog running on %s", cfg.Server.Address)
	if err := http.ListenAndServe(cfg.Server.Address, server.Routes()); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
