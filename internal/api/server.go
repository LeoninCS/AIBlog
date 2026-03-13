package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aiblog/internal/agent"
	"aiblog/internal/blog"
	"aiblog/internal/rag"
)

type Server struct {
	blog    *blog.Repository
	agent   *agent.Service
	rag     *rag.Builder
	webRoot string
	logger  *log.Logger
}

func NewServer(blogRepo *blog.Repository, agentSvc *agent.Service, ragSvc *rag.Builder, webRoot string, logger *log.Logger) *Server {
	return &Server{blog: blogRepo, agent: agentSvc, rag: ragSvc, webRoot: webRoot, logger: logger}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/posts", s.handlePosts)
	mux.HandleFunc("/api/posts/", s.handlePostBySlug)
	mux.HandleFunc("/api/agent/chat", s.handleAgentChat)
	mux.HandleFunc("/api/rag/query", s.handleRAGQuery)
	mux.HandleFunc("/api/rag/reindex", s.handleRAGReindex)

	fs := http.FileServer(http.Dir(s.webRoot))
	mux.Handle("/", withSPA(fs, s.webRoot))

	return withCORS(mux)
}

func (s *Server) InitRAG() error {
	_, err := s.reindex()
	return err
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	jsonResponse(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handlePosts(w http.ResponseWriter, r *http.Request) {
	s.logf("api posts method=%s path=%s", r.Method, r.URL.Path)
	switch r.Method {
	case http.MethodGet:
		includeDrafts := r.URL.Query().Get("includeDrafts") == "true"
		query := r.URL.Query().Get("q")
		items, err := s.blog.Search(query, includeDrafts)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, err)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var input blog.Post
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			errorResponse(w, http.StatusBadRequest, err)
			return
		}
		saved, err := s.blog.Save(&input, r.Header.Get("If-Match"))
		if err != nil {
			handleBlogErr(w, err)
			return
		}
		_, _ = s.reindex()
		jsonResponse(w, http.StatusCreated, saved)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handlePostBySlug(w http.ResponseWriter, r *http.Request) {
	s.logf("api post_by_slug method=%s path=%s", r.Method, r.URL.Path)
	path := strings.TrimPrefix(r.URL.Path, "/api/posts/")
	path = strings.Trim(path, "/")
	if path == "" {
		errorResponse(w, http.StatusBadRequest, errors.New("missing slug"))
		return
	}

	if strings.HasSuffix(path, "/publish") {
		slug := strings.TrimSuffix(path, "/publish")
		slug = strings.Trim(slug, "/")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		post, err := s.blog.Publish(slug, r.Header.Get("If-Match"))
		if err != nil {
			handleBlogErr(w, err)
			return
		}
		_, _ = s.reindex()
		jsonResponse(w, http.StatusOK, post)
		return
	}

	slug := path
	switch r.Method {
	case http.MethodGet:
		post, err := s.blog.GetBySlug(slug)
		if err != nil {
			handleBlogErr(w, err)
			return
		}
		jsonResponse(w, http.StatusOK, post)
	case http.MethodPut:
		var input blog.Post
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			errorResponse(w, http.StatusBadRequest, err)
			return
		}
		input.Slug = slug
		saved, err := s.blog.Save(&input, r.Header.Get("If-Match"))
		if err != nil {
			handleBlogErr(w, err)
			return
		}
		_, _ = s.reindex()
		jsonResponse(w, http.StatusOK, saved)
	case http.MethodDelete:
		err := s.blog.Delete(slug, r.Header.Get("If-Match"))
		if err != nil {
			handleBlogErr(w, err)
			return
		}
		_, _ = s.reindex()
		jsonResponse(w, http.StatusOK, map[string]any{
			"deleted": true,
			"slug":    slug,
		})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleAgentChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req agent.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, err)
		return
	}
	s.logf("api agent_chat mode=%s slug=%s text_len=%d", req.Mode, req.Slug, len([]rune(req.Text)))

	resp, err := s.agent.Chat(req)
	if err != nil {
		s.logf("api agent_chat error mode=%s slug=%s error=%v", req.Mode, req.Slug, err)
		errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	chunks, reindexErr := s.reindex()
	if reindexErr != nil {
		s.logf("api agent_chat reindex_error error=%v", reindexErr)
	} else {
		s.logf("api agent_chat reindex_complete chunks=%d", chunks)
	}
	postSlug := ""
	if resp.Post != nil {
		postSlug = resp.Post.Slug
	}
	s.logf("api agent_chat success mode=%s slug=%s fallback=%t fallback_reason=%q", req.Mode, postSlug, resp.Fallback, resp.FallbackReason)
	jsonResponse(w, http.StatusOK, resp)
}

func (s *Server) handleRAGQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		errorResponse(w, http.StatusBadRequest, err)
		return
	}
	s.logf("api rag_query query_len=%d", len([]rune(req.Query)))
	result := s.rag.Query(req.Query)
	s.logf("api rag_query success chunks=%d", len(result.Chunks))
	jsonResponse(w, http.StatusOK, result)
}

func (s *Server) handleRAGReindex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	count, err := s.reindex()
	if err != nil {
		s.logf("api rag_reindex error=%v", err)
		errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.logf("api rag_reindex success chunks=%d", count)
	jsonResponse(w, http.StatusOK, map[string]any{
		"message": "reindex completed",
		"chunks":  count,
	})
}

func (s *Server) reindex() (int, error) {
	s.logf("rag rebuild start")
	items, err := s.blog.List(true)
	if err != nil {
		s.logf("rag rebuild list_error error=%v", err)
		return 0, err
	}
	posts := make([]blog.Post, 0, len(items))
	for _, item := range items {
		post, err := s.blog.GetBySlug(item.Slug)
		if err != nil {
			s.logf("rag rebuild skip slug=%s error=%v", item.Slug, err)
			continue
		}
		posts = append(posts, *post)
	}
	count := s.rag.Rebuild(posts)
	s.logf("rag rebuild complete posts=%d chunks=%d", len(posts), count)
	return count, nil
}

func handleBlogErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrNotExist):
		errorResponse(w, http.StatusNotFound, err)
	case errors.Is(err, blog.ErrVersionConflict):
		errorResponse(w, http.StatusConflict, err)
	default:
		errorResponse(w, http.StatusInternalServerError, err)
	}
}

func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func errorResponse(w http.ResponseWriter, status int, err error) {
	jsonResponse(w, status, map[string]string{"error": err.Error()})
}

func (s *Server) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withSPA(fs http.Handler, webRoot string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		clean := filepath.Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if clean != "." && strings.Contains(clean, ".") {
			full := filepath.Join(webRoot, clean)
			if info, err := os.Stat(full); err == nil && !info.IsDir() {
				fs.ServeHTTP(w, r)
				return
			}
		}

		http.ServeFile(w, r, filepath.Join(webRoot, "index.html"))
	})
}
