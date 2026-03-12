package agent

import (
	"fmt"
	"strings"
	"time"

	"aiblog/internal/blog"
	"aiblog/internal/llm"
	"aiblog/internal/rag"
)

type Service struct {
	llm    *llm.Client
	blog   *blog.Repository
	ragSvc *rag.Builder
}

type ChatRequest struct {
	Mode string `json:"mode"`
	Text string `json:"text"`
	Slug string `json:"slug"`
}

type ChatResponse struct {
	Reply    string      `json:"reply"`
	Post     *blog.Post  `json:"post,omitempty"`
	RAG      *rag.Result `json:"rag,omitempty"`
	Fallback bool        `json:"fallback"`
}

func NewService(llmClient *llm.Client, blogRepo *blog.Repository, ragSvc *rag.Builder) *Service {
	return &Service{llm: llmClient, blog: blogRepo, ragSvc: ragSvc}
}

func (s *Service) Chat(req ChatRequest) (*ChatResponse, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "search", "rag":
		return s.handleSearch(req)
	case "edit":
		return s.handleEdit(req)
	case "write", "draft", "":
		return s.handleWrite(req)
	default:
		return s.handleWrite(req)
	}
}

func (s *Service) handleSearch(req ChatRequest) (*ChatResponse, error) {
	result := s.ragSvc.Query(req.Text)
	return &ChatResponse{
		Reply:    result.Answer,
		RAG:      &result,
		Fallback: true,
	}, nil
}

func (s *Service) handleWrite(req ChatRequest) (*ChatResponse, error) {
	systemPrompt := `你是专业中文技术博客写作助手。输出 Markdown，结构清晰，包含标题、导语、小节和结论。`
	userPrompt := strings.TrimSpace(req.Text)
	if userPrompt == "" {
		userPrompt = "写一篇关于AI工程实践的博客。"
	}
	content, fallback, err := s.generateWithFallback(systemPrompt, userPrompt, defaultDraft(userPrompt))
	if err != nil {
		return nil, err
	}

	slug := sanitizeForAgent(req.Slug)
	if slug == "" {
		slug = sanitizeForAgent(firstLineAsTitle(content))
	}
	if slug == "" {
		slug = fmt.Sprintf("draft-%d", time.Now().Unix())
	}

	post := &blog.Post{
		FrontMatter: blog.FrontMatter{
			Title:   firstLineAsTitle(content),
			Slug:    slug,
			Date:    time.Now(),
			Status:  "draft",
			Summary: summarizeText(content),
			Tags:    []string{"ai", "draft"},
		},
		Body: content,
	}

	saved, err := s.blog.Save(post, "")
	if err != nil {
		return nil, err
	}

	return &ChatResponse{
		Reply:    "已生成草稿并保存到 blog/drafts，可在编辑器继续修改。",
		Post:     saved,
		Fallback: fallback,
	}, nil
}

func (s *Service) handleEdit(req ChatRequest) (*ChatResponse, error) {
	slug := sanitizeForAgent(req.Slug)
	if slug == "" {
		return nil, fmt.Errorf("edit mode requires slug")
	}
	post, err := s.blog.GetBySlug(slug)
	if err != nil {
		return nil, err
	}

	systemPrompt := `你是专业中文编辑。请在不改变事实的前提下优化结构、语言流畅度和可读性，输出完整 Markdown。`
	context := fmt.Sprintf("原文：\n%s\n\n编辑要求：%s", post.Body, req.Text)
	content, fallback, err := s.generateWithFallback(systemPrompt, context, post.Body)
	if err != nil {
		return nil, err
	}

	post.Body = content
	post.Summary = summarizeText(content)
	updated, err := s.blog.Save(post, post.Version)
	if err != nil {
		return nil, err
	}
	return &ChatResponse{
		Reply:    "已根据你的要求完成改稿并保存。",
		Post:     updated,
		Fallback: fallback,
	}, nil
}

func (s *Service) generateWithFallback(systemPrompt string, userPrompt string, fallbackText string) (string, bool, error) {
	if s.llm != nil && s.llm.Enabled() {
		if result, err := s.llm.Generate(systemPrompt, userPrompt); err == nil {
			return result, false, nil
		}
	}
	return fallbackText, true, nil
}

func defaultDraft(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "AI工程实践"
	}
	return fmt.Sprintf("# %s\n\n## 导语\n这是一篇由本地 MVP 生成的草稿。你可以继续补充背景、案例和结论。\n\n## 核心观点\n1. 明确目标和读者。\n2. 使用可验证的例子。\n3. 通过迭代打磨结构。\n\n## 实践建议\n- 先列提纲再落正文。\n- 每段只表达一个核心信息。\n- 发布前做一次事实核验。\n\n## 结语\n把文章当作持续演进的知识资产。")
}

func summarizeText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	r := []rune(strings.ReplaceAll(text, "\n", " "))
	if len(r) <= 80 {
		return string(r)
	}
	return string(r[:80]) + "..."
}

func firstLineAsTitle(md string) string {
	lines := strings.Split(strings.TrimSpace(md), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimPrefix(line, "#")
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return "AI 草稿"
}

func sanitizeForAgent(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return ""
	}
	input = strings.ReplaceAll(input, " ", "-")
	b := strings.Builder{}
	for _, r := range input {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}
