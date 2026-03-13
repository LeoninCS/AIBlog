package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"aiblog/internal/blog"
	"aiblog/internal/llm"
	"aiblog/internal/rag"
)

type Service struct {
	llm    *llm.Client
	blog   *blog.Repository
	ragSvc *rag.Builder
	root   string
	logger *log.Logger
}

const (
	projectTopK                = 6
	projectContextMaxRunes     = 3200
	projectBriefMaxRunes       = 900
	projectFileChunkSize       = 560
	projectFileChunkOverlap    = 80
	projectSelectedFileCount   = 6
	articleSectionContextRunes = 720
)

type projectFileSpec struct {
	path      string
	priority  float64
	maxChunks int
}

type projectSectionSummary struct {
	path  string
	text  string
	score float64
}

type scoredProjectChunk struct {
	index int
	text  string
	score float64
}

type articleSectionPlan struct {
	Title string
	Goal  string
	Seed  string
}

type ChatRequest struct {
	Mode     string `json:"mode"`
	Text     string `json:"text"`
	Slug     string `json:"slug"`
	Selected string `json:"selected"`
	Context  string `json:"context"`
}

type ChatResponse struct {
	Reply          string      `json:"reply"`
	Post           *blog.Post  `json:"post,omitempty"`
	RAG            *rag.Result `json:"rag,omitempty"`
	Analysis       string      `json:"analysis,omitempty"`
	Generation     string      `json:"generation,omitempty"`
	Fallback       bool        `json:"fallback"`
	FallbackReason string      `json:"fallback_reason,omitempty"`
}

func NewService(llmClient *llm.Client, blogRepo *blog.Repository, ragSvc *rag.Builder, logger *log.Logger) *Service {
	return &Service{
		llm:    llmClient,
		blog:   blogRepo,
		ragSvc: ragSvc,
		root:   ".",
		logger: logger,
	}
}

func (s *Service) Chat(req ChatRequest) (*ChatResponse, error) {
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case "search", "rag":
		return s.handleSearch(req)
	case "rag-create", "search-create", "research-write":
		return s.handleRAGCreate(req)
	case "inline-edit", "selection-edit":
		return s.handleInlineEdit(req)
	case "edit":
		return s.handleEdit(req)
	case "write", "draft", "":
		return s.handleWrite(req)
	default:
		return s.handleWrite(req)
	}
}

func (s *Service) handleSearch(req ChatRequest) (*ChatResponse, error) {
	s.logf("agent search start query=%q", truncateForLog(req.Text, 120))
	result := s.ragSvc.Query(req.Text)
	answer, fallback, fallbackReason, err := s.answerFromRAG(req.Text, result.Chunks)
	if err != nil {
		s.logf("agent search error query=%q error=%v", truncateForLog(req.Text, 120), err)
		return nil, err
	}
	result.Answer = answer
	result.Summary = answer
	result.Model = ragModelName(fallback)
	result.Intent = inferRAGIntent(req.Text)
	return &ChatResponse{
		Reply:          answer,
		RAG:            &result,
		Fallback:       fallback,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *Service) handleRAGCreate(req ChatRequest) (*ChatResponse, error) {
	query := strings.TrimSpace(req.Text)
	if query == "" {
		return nil, fmt.Errorf("rag-create mode requires text")
	}
	s.logf("agent rag_create start query=%q", truncateForLog(query, 120))

	result := s.ragSvc.Query(query)
	content, fallback, fallbackReason, err := s.createFromRAG(query, result.Chunks)
	if err != nil {
		s.logf("agent rag_create generation_error query=%q error=%v", truncateForLog(query, 120), err)
		return nil, err
	}

	slug := sanitizeForAgent(req.Slug)
	if slug == "" {
		slug = sanitizeForAgent(firstLineAsTitle(content))
	}
	if slug == "" {
		slug = fmt.Sprintf("rag-draft-%d", time.Now().Unix())
	}
	originalSlug := slug
	slug, err = s.blog.NextAvailableSlug(slug)
	if err != nil {
		s.logf("agent rag_create slug_resolve_error base_slug=%s error=%v", originalSlug, err)
		return nil, err
	}
	if slug != originalSlug {
		s.logf("agent rag_create slug_collision base_slug=%s resolved_slug=%s", originalSlug, slug)
	}

	post := &blog.Post{
		FrontMatter: blog.FrontMatter{
			Title:   firstLineAsTitle(content),
			Slug:    slug,
			Date:    time.Now(),
			Status:  "draft",
			Summary: summarizeText(content),
			Tags:    ragTags(result.Chunks),
		},
		Body: content,
	}

	saved, err := s.blog.Save(post, "")
	if err != nil {
		s.logf("agent rag_create save_error slug=%s error=%v", slug, err)
		return nil, err
	}

	answer, answerFallback, answerFallbackReason, err := s.answerFromRAG(query, result.Chunks)
	if err != nil {
		return nil, err
	}
	if answerFallback && fallbackReason == "" {
		fallbackReason = answerFallbackReason
	}
	fallback = fallback || answerFallback
	result.Answer = answer
	result.Summary = answer
	result.Model = ragModelName(fallback)
	result.Intent = "create"

	return &ChatResponse{
		Reply:          "已基于检索结果生成新草稿并保存到 blog/drafts。",
		Post:           saved,
		RAG:            &result,
		Fallback:       fallback,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *Service) handleWrite(req ChatRequest) (*ChatResponse, error) {
	userPrompt := strings.TrimSpace(req.Text)
	if userPrompt == "" {
		userPrompt = "写一篇关于AI工程实践的博客。"
	}
	s.logf("agent write start prompt=%q slug=%q", truncateForLog(userPrompt, 160), req.Slug)
	projectAware := referencesCurrentProject(userPrompt)
	s.logf("agent write topic_routing project_aware=%t", projectAware)

	projectContext := ""
	if projectAware {
		projectContext = s.projectContext(userPrompt)
		s.logf("agent write context_ready project_context_len=%d", len([]rune(projectContext)))
	} else {
		s.logf("agent write context_skipped reason=%q", "prompt does not reference current project")
	}

	analysis, analysisFallback, analysisFallbackReason, err := s.analyzeWritingIntent(userPrompt, projectContext, projectAware)
	if err != nil {
		s.logf("agent write analysis_error prompt=%q error=%v", truncateForLog(userPrompt, 160), err)
		return nil, err
	}
	compressedAnalysis := compressWritingBrief(analysis)
	s.logf("agent write analysis_done fallback=%t fallback_reason=%q", analysisFallback, analysisFallbackReason)
	s.logf(
		"agent write generation_start analysis_len=%d compressed_analysis_len=%d project_context_len=%d",
		len([]rune(analysis)),
		len([]rune(compressedAnalysis)),
		len([]rune(projectContext)),
	)

	content, generatedTitle, generationFallback, generationFallbackReason, err := s.generateArticleFromBrief(compressedAnalysis, projectContext, userPrompt, projectAware)
	if err != nil {
		s.logf("agent write generation_error prompt=%q error=%v", truncateForLog(userPrompt, 160), err)
		return nil, err
	}
	fallback := analysisFallback || generationFallback
	fallbackReason := mergeFallbackReasons(analysisFallbackReason, generationFallbackReason)
	title := resolvedArticleTitle(generatedTitle, compressedAnalysis, userPrompt)
	content = prependMarkdownTitle(title, content)
	s.logf("agent write generation_done fallback=%t fallback_reason=%q title=%q", fallback, fallbackReason, truncateForLog(title, 120))

	slug := sanitizeForAgent(req.Slug)
	if slug == "" {
		slug = sanitizeForAgent(title)
	}
	if slug == "" {
		slug = fmt.Sprintf("draft-%d", time.Now().Unix())
	}
	originalSlug := slug
	slug, err = s.blog.NextAvailableSlug(slug)
	if err != nil {
		s.logf("agent write slug_resolve_error base_slug=%s error=%v", originalSlug, err)
		return nil, err
	}
	if slug != originalSlug {
		s.logf("agent write slug_collision base_slug=%s resolved_slug=%s", originalSlug, slug)
	}

	post := &blog.Post{
		FrontMatter: blog.FrontMatter{
			Title:   title,
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
		s.logf("agent write save_error slug=%s error=%v", slug, err)
		return nil, err
	}
	s.logf("agent write success slug=%s path=%s fallback=%t fallback_reason=%q", saved.Slug, saved.Path, fallback, fallbackReason)

	return &ChatResponse{
		Reply:          "已生成草稿并保存到 blog/drafts，可在编辑器继续修改。",
		Post:           saved,
		Analysis:       compressedAnalysis,
		Generation:     content,
		Fallback:       fallback,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *Service) handleEdit(req ChatRequest) (*ChatResponse, error) {
	slug := sanitizeForAgent(req.Slug)
	if slug == "" {
		return nil, fmt.Errorf("edit mode requires slug")
	}
	s.logf("agent edit start slug=%s", slug)
	post, err := s.blog.GetBySlug(slug)
	if err != nil {
		s.logf("agent edit load_error slug=%s error=%v", slug, err)
		return nil, err
	}

	systemPrompt := `你是专业中文编辑。请在不改变事实的前提下优化结构、语言流畅度和可读性，输出完整 Markdown。`
	context := fmt.Sprintf("原文：\n%s\n\n编辑要求：%s", post.Body, req.Text)
	content, fallback, fallbackReason, err := s.generateWithFallback(systemPrompt, context, post.Body)
	if err != nil {
		s.logf("agent edit generation_error slug=%s error=%v", slug, err)
		return nil, err
	}

	post.Body = content
	post.Summary = summarizeText(content)
	updated, err := s.blog.Save(post, post.Version)
	if err != nil {
		s.logf("agent edit save_error slug=%s error=%v", slug, err)
		return nil, err
	}
	s.logf("agent edit success slug=%s fallback=%t fallback_reason=%q", slug, fallback, fallbackReason)
	return &ChatResponse{
		Reply:          "已根据你的要求完成改稿并保存。",
		Post:           updated,
		Fallback:       fallback,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *Service) handleInlineEdit(req ChatRequest) (*ChatResponse, error) {
	selected := strings.TrimSpace(req.Selected)
	instruction := strings.TrimSpace(req.Text)
	if selected == "" {
		return nil, fmt.Errorf("inline-edit mode requires selected text")
	}
	if instruction == "" {
		return nil, fmt.Errorf("inline-edit mode requires edit instruction")
	}
	s.logf("agent inline_edit start selected_len=%d instruction=%q", len([]rune(selected)), truncateForLog(instruction, 120))

	systemPrompt := `你是嵌入在编辑器中的中文 AI 写作助手。用户会提供一段选中文本和修改要求。
请只输出修改后的文本本身，不要解释，不要加标题，不要加代码围栏。
保持原意与上下文一致，除非用户明确要求重写。`
	userPrompt := fmt.Sprintf("编辑要求：%s\n\n上下文（帮助理解语气，可选）：\n%s\n\n待修改文本：\n%s", instruction, strings.TrimSpace(req.Context), selected)
	fallbackText := fallbackInlineEdit(selected, instruction)
	content, fallback, fallbackReason, err := s.generateWithFallback(systemPrompt, userPrompt, fallbackText)
	if err != nil {
		s.logf("agent inline_edit error error=%v", err)
		return nil, err
	}
	s.logf("agent inline_edit success fallback=%t fallback_reason=%q", fallback, fallbackReason)

	return &ChatResponse{
		Reply:          strings.TrimSpace(content),
		Fallback:       fallback,
		FallbackReason: fallbackReason,
	}, nil
}

func (s *Service) generateWithFallback(systemPrompt string, userPrompt string, fallbackText string) (string, bool, string, error) {
	if s.llm == nil {
		s.logf("llm fallback reason=%q", "llm client is nil")
		return fallbackText, true, "llm client is nil", nil
	}
	if !s.llm.Enabled() {
		s.logf("llm fallback reason=%q", "llm client is not configured")
		return fallbackText, true, "llm client is not configured", nil
	}
	start := time.Now()
	s.logf("llm generate start user_prompt_len=%d system_prompt_len=%d", len([]rune(userPrompt)), len([]rune(systemPrompt)))
	result, err := s.llm.Generate(systemPrompt, userPrompt)
	if err == nil {
		s.logf("llm generate success duration_ms=%d user_prompt_len=%d", time.Since(start).Milliseconds(), len([]rune(userPrompt)))
		return result, false, "", nil
	}
	s.logf("llm generate failed duration_ms=%d error=%v user_prompt_len=%d", time.Since(start).Milliseconds(), err, len([]rune(userPrompt)))
	return fallbackText, true, err.Error(), nil
}

func defaultDraft(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "AI工程实践"
	}
	return fmt.Sprintf("# %s\n\n## 导语\n这是一篇由本地 MVP 生成的草稿。你可以继续补充背景、案例和结论。\n\n## 核心观点\n1. 明确目标和读者。\n2. 使用可验证的例子。\n3. 通过迭代打磨结构。\n\n## 实践建议\n- 先列提纲再落正文。\n- 每段只表达一个核心信息。\n- 发布前做一次事实核验。\n\n## 结语\n把文章当作持续演进的知识资产。", prompt)
}

func (s *Service) analyzeWritingIntent(prompt string, projectContext string, projectAware bool) (string, bool, string, error) {
	systemPrompt := `你是中文技术博客策划助手。请先分析并优化用户的原始写作需求，把它整理成一份更适合写作的 brief。
输出要求：
1. 保留用户真实意图，不要改题。
2. 输出结构化内容，包含：文章目标、目标读者、建议标题、建议结构、应该覆盖的关键点、写作语气。
3. 只有在用户明确提到“这个项目”“当前项目”“AIBlog”“我的项目”等指代时，才结合提供的项目上下文。
4. 如果用户主题是 Kubernetes、数据库、前端、AI、编程语言等通用技术主题，不要擅自改写成对当前项目的介绍。
4. 使用简洁清晰的中文。`
	fallbackText := fallbackWritingBrief(prompt)
	userInput := fmt.Sprintf(
		"用户写作需求：\n%s\n\n是否明确指向当前项目：%t\n\n项目上下文（仅在明确指向当前项目时可用，否则忽略）：\n%s",
		strings.TrimSpace(prompt),
		projectAware,
		projectContext,
	)
	return s.generateWithFallback(systemPrompt, userInput, fallbackText)
}

func (s *Service) generateArticleFromBrief(brief string, projectContext string, userPrompt string, projectAware bool) (string, string, bool, string, error) {
	fallbackTitle := fallbackArticleTitle(brief, userPrompt)
	fallbackText := fallbackDraftFromBrief(brief, projectContext, userPrompt, projectAware)
	plans := articleSectionsFromBrief(brief, projectContext, userPrompt, projectAware)
	if len(plans) == 0 {
		return prependMarkdownTitle(fallbackTitle, fallbackText), fallbackTitle, true, "article section plan is empty", nil
	}
	lines := make([]string, 0, len(plans)*3)

	sectionFallback := false
	fallbackReasons := make([]string, 0, len(plans))
	for idx, plan := range plans {
		s.logf("agent write section_start index=%d title=%q", idx+1, plan.Title)
		content, fallback, reason, err := s.generateArticleSection(plan, brief, projectContext)
		if err != nil {
			return "", "", false, "", err
		}
		if fallback {
			sectionFallback = true
			if reason != "" {
				fallbackReasons = append(fallbackReasons, fmt.Sprintf("%s: %s", plan.Title, reason))
			}
		}
		lines = append(lines, "## "+plan.Title)
		lines = append(lines, content)
		lines = append(lines, "")
		s.logf("agent write section_done index=%d title=%q fallback=%t len=%d", idx+1, plan.Title, fallback, len([]rune(content)))
	}

	body := strings.TrimSpace(strings.Join(lines, "\n"))
	body = normalizeGeneratedMarkdown(body)
	if strings.TrimSpace(body) == "" {
		return prependMarkdownTitle(fallbackTitle, fallbackText), fallbackTitle, true, mergeFallbackReasons("generated article is empty", strings.Join(fallbackReasons, " | ")), nil
	}

	title, titleFallback, titleFallbackReason, err := s.generateArticleTitle(brief, userPrompt, body, fallbackTitle)
	if err != nil {
		return "", "", false, "", err
	}
	if titleFallback {
		sectionFallback = true
		if titleFallbackReason != "" {
			fallbackReasons = append(fallbackReasons, "title: "+titleFallbackReason)
		}
	}

	content := prependMarkdownTitle(title, body)
	return content, title, sectionFallback, mergeFallbackReasons(fallbackReasons...), nil
}

func (s *Service) answerFromRAG(question string, chunks []rag.Chunk) (string, bool, string, error) {
	if len(chunks) == 0 {
		result := s.ragSvc.Query(question)
		return result.Answer, true, "rag query returned no chunks", nil
	}

	systemPrompt := `你是中文技术博客知识库问答助手。请严格基于给定资料回答，输出要求：
1. 先给结论，再给2到4条要点。
2. 明确指出信息来自哪些文章或片段。
3. 不要编造资料中不存在的事实。
4. 使用自然、专业、克制的中文。`
	userPrompt := fmt.Sprintf("用户问题：%s\n\n知识库片段：\n%s\n\n请基于以上资料回答，并在结尾补充“参考来源”列表。", strings.TrimSpace(question), buildChunkContext(chunks, 5, 520))
	fallbackText := fallbackRAGAnswer(question, chunks)
	return s.generateWithFallback(systemPrompt, userPrompt, fallbackText)
}

func (s *Service) createFromRAG(query string, chunks []rag.Chunk) (string, bool, string, error) {
	if len(chunks) == 0 {
		return fallbackRAGDraft(query, chunks), true, "rag query returned no chunks", nil
	}

	systemPrompt := `你是专业中文技术作者。请根据提供的知识库资料写一篇新的 Markdown 草稿，要求：
1. 标题明确，结构完整，包含导语、小节和结语。
2. 内容必须基于提供资料进行整合，不要虚构事实。
3. 如果资料不足，就明确指出局限，并给出保守表达。
4. 文风专业、清晰、适合技术博客发布。`
	userPrompt := fmt.Sprintf("写作任务：%s\n\n参考资料：\n%s\n\n请输出完整 Markdown 草稿。", strings.TrimSpace(query), buildChunkContext(chunks, 6, 680))
	fallbackText := fallbackRAGDraft(query, chunks)
	return s.generateWithFallback(systemPrompt, userPrompt, fallbackText)
}

func (s *Service) generateArticleTitle(brief string, userPrompt string, body string, fallbackTitle string) (string, bool, string, error) {
	systemPrompt := `你是中文技术博客标题助手。请根据用户需求和文章正文，总结一个适合作为博客标题的中文标题。
要求：
1. 只输出标题本身，不要解释，不要加引号，不要 Markdown 符号。
2. 标题要准确概括文章主题，不要空泛。
3. 长度尽量控制在 8 到 24 个中文字符之间。
4. 如果主题是技术说明文，优先突出核心对象、机制或场景。`
	userPromptText := fmt.Sprintf(
		"用户原始需求：%s\n\n文章 brief：\n%s\n\n文章正文：\n%s",
		strings.TrimSpace(userPrompt),
		strings.TrimSpace(brief),
		shortenRunes(strings.TrimSpace(body), 1800),
	)
	title, fallback, reason, err := s.generateWithFallback(systemPrompt, userPromptText, fallbackTitle)
	if err != nil {
		return "", false, "", err
	}
	title = normalizeTitle(title)
	if title == "" {
		return normalizeTitle(fallbackTitle), true, mergeFallbackReasons(reason, "empty generated title"), nil
	}
	return title, fallback, reason, nil
}

func mergeFallbackReasons(reasons ...string) string {
	parts := make([]string, 0, len(reasons))
	seen := make(map[string]struct{}, len(reasons))
	for _, reason := range reasons {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		if _, ok := seen[reason]; ok {
			continue
		}
		seen[reason] = struct{}{}
		parts = append(parts, reason)
	}
	return strings.Join(parts, " | ")
}

func (s *Service) projectContext(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		query = "项目 功能 架构 模块"
	}

	readmeSummary := s.summarizeReadme()
	summaries := s.projectSectionSummaries(query)
	retrieval := s.compactProjectKnowledge(query)
	sections := make([]string, 0, 4)

	if readmeSummary != "" {
		sections = append(sections, readmeSummary)
	}
	if len(summaries) > 0 {
		lines := make([]string, 0, len(summaries))
		for _, summary := range summaries {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", summary.path, summary.text))
		}
		sections = append(sections, "项目实现摘要：\n"+strings.Join(lines, "\n"))
	}
	if retrieval != "" {
		sections = append(sections, retrieval)
	}

	context := strings.Join(sections, "\n\n")
	context = shortenRunes(strings.TrimSpace(context), projectContextMaxRunes)
	s.logf(
		"agent project_context_ready query=%q sections=%d len=%d",
		truncateForLog(query, 120),
		len(sections),
		len([]rune(context)),
	)
	return context
}

func (s *Service) projectKnowledgeChunks(query string) []rag.Chunk {
	specs := projectFileSpecs()
	chunks := make([]rag.Chunk, 0, len(specs)*2)
	for _, spec := range specs {
		rel := spec.path
		path := filepath.Join(s.root, rel)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		title := rel
		slug := sanitizeForAgent(strings.ReplaceAll(rel, string(filepath.Separator), "-"))
		parts := rag.ChunkMarkdown(slug, title, rel, string(data), 900, 120, 0)
		chunks = append(chunks, parts...)
	}
	if len(chunks) == 0 {
		return nil
	}

	query = strings.TrimSpace(query)
	if query == "" {
		query = "项目 功能 架构 模块"
	}
	idx := rag.NewIndex()
	idx.Replace(chunks)
	return idx.Search(query, projectTopK)
}

func buildChunkContext(chunks []rag.Chunk, maxChunks int, maxLen int) string {
	if maxChunks <= 0 || maxChunks > len(chunks) {
		maxChunks = len(chunks)
	}
	lines := make([]string, 0, maxChunks)
	for i := 0; i < maxChunks; i++ {
		text := strings.TrimSpace(chunks[i].Text)
		runes := []rune(text)
		if maxLen > 0 && len(runes) > maxLen {
			text = string(runes[:maxLen]) + "..."
		}
		lines = append(lines, fmt.Sprintf("[%d] 标题: %s\nslug: %s\npath: %s\n内容: %s", i+1, chunks[i].Title, chunks[i].Slug, chunks[i].Path, text))
	}
	return strings.Join(lines, "\n\n")
}

func projectFileSpecs() []projectFileSpec {
	return []projectFileSpec{
		{path: "README.md", priority: 3.4, maxChunks: 2},
		{path: filepath.Join("config", "config.yaml"), priority: 2.4, maxChunks: 1},
		{path: filepath.Join("cmd", "server", "main.go"), priority: 2.8, maxChunks: 1},
		{path: filepath.Join("internal", "api", "server.go"), priority: 3.1, maxChunks: 2},
		{path: filepath.Join("internal", "blog", "repository.go"), priority: 2.8, maxChunks: 2},
		{path: filepath.Join("internal", "blog", "renderer.go"), priority: 1.6, maxChunks: 1},
		{path: filepath.Join("internal", "agent", "service.go"), priority: 3.2, maxChunks: 2},
		{path: filepath.Join("internal", "rag", "service.go"), priority: 2.6, maxChunks: 1},
		{path: filepath.Join("internal", "rag", "index.go"), priority: 2.2, maxChunks: 1},
		{path: filepath.Join("web", "index.html"), priority: 1.4, maxChunks: 1},
		{path: filepath.Join("web", "app.js"), priority: 2.3, maxChunks: 1},
		{path: filepath.Join("web", "styles.css"), priority: 1.1, maxChunks: 1},
	}
}

func (s *Service) summarizeReadme() string {
	path := filepath.Join(s.root, "README.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}

	title := firstLineAsTitle(text)
	intro := firstNonEmptyParagraph(removeMarkdownHeadings(text))
	features := normalizeBulletLines(markdownSection(text, "Features"), 4)
	structure := normalizeBulletLines(markdownSection(text, "Project Structure"), 5)
	runbook := normalizeBulletLines(markdownSection(text, "API Quick Reference"), 5)
	run := markdownSection(text, "Run")
	parts := []string{
		fmt.Sprintf("[README.md]\n项目标题：%s", title),
		"项目简介：" + shortenRunes(strings.TrimSpace(intro), 160),
	}
	if features != "" {
		parts = append(parts, "README 功能摘要：\n"+features)
	}
	if structure != "" {
		parts = append(parts, "README 结构摘要：\n"+structure)
	}
	if strings.TrimSpace(run) != "" {
		parts = append(parts, "README 运行方式：\n- "+shortenRunes(flattenWhitespace(run), 120))
	}
	if runbook != "" {
		parts = append(parts, "README 接口摘要：\n"+runbook)
	}
	return strings.Join(parts, "\n\n")
}

func (s *Service) projectSectionSummaries(query string) []projectSectionSummary {
	specs := projectFileSpecs()
	summaries := make([]projectSectionSummary, 0, len(specs))
	for _, spec := range specs {
		summary, ok := s.summarizeProjectFile(spec, query)
		if !ok {
			continue
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].score == summaries[j].score {
			return summaries[i].path < summaries[j].path
		}
		return summaries[i].score > summaries[j].score
	})
	if len(summaries) > projectSelectedFileCount {
		summaries = summaries[:projectSelectedFileCount]
	}
	return summaries
}

func (s *Service) summarizeProjectFile(spec projectFileSpec, query string) (projectSectionSummary, bool) {
	path := filepath.Join(s.root, spec.path)
	data, err := os.ReadFile(path)
	if err != nil {
		return projectSectionSummary{}, false
	}
	raw := strings.TrimSpace(string(data))
	if raw == "" {
		return projectSectionSummary{}, false
	}

	chunks := rag.ChunkMarkdown(
		sanitizeForAgent(strings.ReplaceAll(spec.path, string(filepath.Separator), "-")),
		spec.path,
		spec.path,
		raw,
		projectFileChunkSize,
		projectFileChunkOverlap,
		0,
	)
	selected := selectRelevantProjectChunks(chunks, query, spec.priority, spec.maxChunks)
	if len(selected) == 0 {
		return projectSectionSummary{}, false
	}

	lines := make([]string, 0, len(selected))
	score := spec.priority
	for i, item := range selected {
		compact := compactChunkText(item.text, 140)
		if compact == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, compact))
		score += item.score
	}
	if len(lines) == 0 {
		return projectSectionSummary{}, false
	}

	text := strings.Join(lines, "\n")
	text = shortenRunes(text, 340)
	return projectSectionSummary{
		path:  spec.path,
		text:  text,
		score: score,
	}, true
}

func selectRelevantProjectChunks(chunks []rag.Chunk, query string, baseScore float64, maxChunks int) []scoredProjectChunk {
	if len(chunks) == 0 {
		return nil
	}
	if maxChunks <= 0 {
		maxChunks = 1
	}

	terms := queryTerms(query)
	scored := make([]scoredProjectChunk, 0, len(chunks))
	for _, chunk := range chunks {
		normalized := flattenWhitespace(chunk.Text)
		if normalized == "" {
			continue
		}
		score := baseScore + heuristicChunkScore(chunk.Path, normalized, terms)
		scored = append(scored, scoredProjectChunk{
			index: chunk.Index,
			text:  normalized,
			score: score,
		})
	}
	if len(scored) == 0 {
		return nil
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].index < scored[j].index
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > maxChunks {
		scored = scored[:maxChunks]
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].index < scored[j].index
	})
	return scored
}

func (s *Service) compactProjectKnowledge(query string) string {
	chunks := s.projectKnowledgeChunks(query)
	if len(chunks) == 0 {
		return ""
	}

	lines := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		if i >= 4 {
			break
		}
		lines = append(lines, fmt.Sprintf("- `%s`: %s", chunk.Path, compactChunkText(chunk.Text, 130)))
	}
	if len(lines) == 0 {
		return ""
	}
	return "项目检索补充：\n" + strings.Join(lines, "\n")
}

func compressWritingBrief(brief string) string {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return brief
	}

	priorityFields := []string{
		"文章目标",
		"目标读者",
		"建议标题",
		"建议结构",
		"应该覆盖的关键点",
		"写作语气",
	}
	selected := make([]string, 0, len(priorityFields))
	for _, field := range priorityFields {
		value := extractBriefFieldValue(brief, field)
		if value == "" {
			continue
		}
		selected = append(selected, shortenRunes(field+"："+value, 180))
	}
	if len(selected) == 0 {
		return shortenRunes(flattenWhitespace(brief), projectBriefMaxRunes)
	}
	return shortenRunes(strings.Join(selected, "\n"), projectBriefMaxRunes)
}

func extractBriefFieldValue(brief string, labels ...string) string {
	lines := strings.Split(strings.TrimSpace(brief), "\n")
	for idx, raw := range lines {
		line := normalizeBriefLine(raw)
		if line == "" {
			continue
		}
		for _, label := range labels {
			value, found, needsNext := extractLabeledValue(line, label)
			if found {
				return value
			}
			if !needsNext {
				continue
			}
			for nextIdx := idx + 1; nextIdx < len(lines); nextIdx++ {
				next := normalizeBriefLine(lines[nextIdx])
				if next == "" {
					continue
				}
				if looksLikeStructuredBriefField(next) {
					break
				}
				return cleanLabeledValue(next)
			}
		}
	}
	return ""
}

func normalizeBriefLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	line = strings.ReplaceAll(line, "**", "")
	line = strings.ReplaceAll(line, "__", "")
	line = strings.ReplaceAll(line, "`", "")
	for {
		switch {
		case strings.HasPrefix(line, ">"):
			line = strings.TrimSpace(strings.TrimPrefix(line, ">"))
		case strings.HasPrefix(line, "#"):
			line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		case strings.HasPrefix(line, "- "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
		case strings.HasPrefix(line, "* "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "* "))
		case strings.HasPrefix(line, "+ "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "+ "))
		case strings.HasPrefix(line, "• "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "• "))
		default:
			goto ordered
		}
	}

ordered:
	line = trimOrderedListPrefix(line)
	return strings.TrimSpace(line)
}

func trimOrderedListPrefix(line string) string {
	runes := []rune(strings.TrimSpace(line))
	if len(runes) == 0 {
		return ""
	}

	if runes[0] == '(' || runes[0] == '（' {
		idx := 1
		for idx < len(runes) && unicode.IsDigit(runes[idx]) {
			idx++
		}
		if idx > 1 && idx < len(runes) && (runes[idx] == ')' || runes[idx] == '）') {
			idx++
			for idx < len(runes) && unicode.IsSpace(runes[idx]) {
				idx++
			}
			return strings.TrimSpace(string(runes[idx:]))
		}
	}

	idx := 0
	for idx < len(runes) && unicode.IsDigit(runes[idx]) {
		idx++
	}
	if idx > 0 && idx < len(runes) {
		switch runes[idx] {
		case '.', '、', ')', '）':
			idx++
			for idx < len(runes) && unicode.IsSpace(runes[idx]) {
				idx++
			}
			return strings.TrimSpace(string(runes[idx:]))
		}
	}
	return strings.TrimSpace(string(runes))
}

func extractLabeledValue(line string, label string) (string, bool, bool) {
	line = strings.TrimSpace(line)
	label = strings.TrimSpace(label)
	if line == "" || label == "" || !strings.HasPrefix(line, label) {
		return "", false, false
	}

	rest := strings.TrimSpace(strings.TrimPrefix(line, label))
	if rest == "" {
		return "", false, true
	}
	separator := ""
	switch {
	case strings.HasPrefix(rest, "："):
		separator = "："
	case strings.HasPrefix(rest, ":"):
		separator = ":"
	default:
		return "", false, false
	}

	value := cleanLabeledValue(strings.TrimSpace(strings.TrimPrefix(rest, separator)))
	if value == "" {
		return "", false, true
	}
	return value, true, false
}

func cleanLabeledValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "\"'`“”‘’")
	return strings.Join(strings.Fields(value), " ")
}

func looksLikeStructuredBriefField(line string) bool {
	fields := []string{
		"文章目标",
		"目标读者",
		"建议标题",
		"建议结构",
		"应该覆盖的关键点",
		"写作语气",
	}
	line = normalizeBriefLine(line)
	for _, field := range fields {
		if _, found, needsNext := extractLabeledValue(line, field); found || needsNext {
			return true
		}
	}
	return false
}

func articleSectionsFromBrief(brief string, projectContext string, userPrompt string, projectAware bool) []articleSectionPlan {
	if projectAware && briefLooksProjectAware(brief, userPrompt) {
		return []articleSectionPlan{
			{Title: "导语", Goal: "简要说明这篇文章要解决什么问题，以及为什么要介绍这个项目。", Seed: projectIntro(projectContext)},
			{Title: "项目概览", Goal: "概括项目定位、目标与整体工作流。", Seed: projectOverview(projectContext)},
			{Title: "核心能力", Goal: "提炼项目面向用户和系统层面的核心能力。", Seed: projectFeatures(projectContext)},
			{Title: "架构与模块", Goal: "说明服务端、存储、RAG 和前端之间的职责划分。", Seed: projectStructure(projectContext)},
			{Title: "运行方式", Goal: "说明文章生成、保存、渲染与重建索引的基本使用方式。", Seed: projectRunbook(projectContext)},
			{Title: "总结", Goal: "收束全文，强调项目价值和可继续扩展的方向。", Seed: "强调它已经形成从提示词输入到 Markdown 保存、前端展示、RAG 检索的一体化闭环。"},
		}
	}

	return []articleSectionPlan{
		{Title: "导语", Goal: "先解释主题与写作背景，让读者快速进入问题。"},
		{Title: "核心概念", Goal: "用清晰、易读的语言解释核心概念和范围。"},
		{Title: "实践要点", Goal: "给出 3 到 5 个关键实践点或判断标准。"},
		{Title: "常见问题", Goal: "总结常见误区、局限或注意事项。"},
		{Title: "总结", Goal: "用简短结论收尾，并指出下一步可继续深入的方向。"},
	}
}

func (s *Service) generateArticleSection(plan articleSectionPlan, brief string, projectContext string) (string, bool, string, error) {
	systemPrompt := `你是专业中文技术博客写作助手。请只生成当前小节的 Markdown 正文段落，不要输出标题，不要解释，不要代码围栏。
要求：
1. 内容紧扣当前小节目标。
2. 语言自然、专业、克制。
3. 如果主题涉及当前项目，优先依据提供的项目上下文，不要泛泛而谈。
4. 控制篇幅在 1 到 3 段，必要时可带少量项目符号列表。`
	sectionContext := shortenRunes(strings.TrimSpace(plan.Seed), articleSectionContextRunes)
	userPrompt := fmt.Sprintf(
		"文章 brief：\n%s\n\n当前小节：%s\n小节目标：%s\n小节可用信息：\n%s\n\n项目上下文：\n%s",
		strings.TrimSpace(brief),
		plan.Title,
		plan.Goal,
		sectionContext,
		projectContext,
	)
	fallbackText := fallbackArticleSection(plan, brief, projectContext)
	content, fallback, reason, err := s.generateWithFallback(systemPrompt, userPrompt, fallbackText)
	if err != nil {
		return "", false, "", err
	}
	return normalizeSectionBody(content), fallback, reason, nil
}

func fallbackArticleSection(plan articleSectionPlan, brief string, projectContext string) string {
	switch plan.Title {
	case "导语":
		return projectIntro(projectContext)
	case "项目概览":
		return projectOverview(projectContext)
	case "核心能力":
		return projectFeatures(projectContext)
	case "架构与模块":
		return projectStructure(projectContext)
	case "运行方式":
		return projectRunbook(projectContext)
	case "总结":
		if briefLooksProjectAware(brief, "") {
			return "这个项目已经把提示词输入、AI 生成、Markdown 保存、前端渲染和 RAG 检索串成了一条完整链路，适合作为一个可继续演进的 AI 博客工作台。"
		}
		return "围绕这个主题，最重要的是先把核心概念讲清楚，再结合实践场景和注意事项组织内容，这样文章会更有可读性和落地价值。"
	case "核心概念":
		return "这一部分应该先定义主题边界，再解释关键术语、基本原理和适用场景，帮助读者建立统一认知。"
	case "实践要点":
		return "- 先明确目标与使用场景。\n- 再结合实现步骤或判断标准展开说明。\n- 最后补充容易忽视的细节与经验。"
	case "常见问题":
		return "- 不要只讲概念，不讲适用条件。\n- 不要忽略边界条件和实际限制。\n- 不要缺少结论与行动建议。"
	default:
		if strings.TrimSpace(plan.Seed) != "" {
			return shortenRunes(strings.TrimSpace(plan.Seed), 220)
		}
		return "这一部分建议围绕当前小节目标展开，保持内容具体、清晰并贴近实际使用场景。"
	}
}

func heuristicChunkScore(path string, text string, terms []string) float64 {
	lower := strings.ToLower(text)
	score := 0.0
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(lower, term) {
			score += 1.5
		}
	}

	switch {
	case strings.Contains(path, "README.md"):
		score += 2.2
	case strings.Contains(path, "internal/agent"):
		score += 1.8
	case strings.Contains(path, "internal/api"):
		score += 1.4
	case strings.Contains(path, "internal/blog"):
		score += 1.1
	case strings.Contains(path, "internal/rag"):
		score += 1.0
	}

	anchors := []string{
		"mode=write", "/api/agent/chat", "reindex", "blog/drafts",
		"markdown", "publish", "draft", "rag", "search", "save", "render",
	}
	for _, anchor := range anchors {
		if strings.Contains(lower, anchor) {
			score += 0.6
		}
	}
	if strings.Contains(lower, "func ") || strings.Contains(lower, "type ") {
		score += 0.2
	}
	return score
}

func compactChunkText(text string, maxRunes int) string {
	text = flattenWhitespace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	points := make([]string, 0, 3)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "* "):
			points = append(points, strings.TrimSpace(line))
		case strings.HasPrefix(line, "func "), strings.HasPrefix(line, "type "):
			points = append(points, line)
		case looksLikeConfigLine(line):
			points = append(points, line)
		case len(points) == 0:
			points = append(points, line)
		}
		if len(points) >= 3 {
			break
		}
	}
	if len(points) == 0 {
		return shortenRunes(text, maxRunes)
	}
	return shortenRunes(strings.Join(points, " | "), maxRunes)
}

func flattenWhitespace(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts = append(parts, line)
	}
	return strings.Join(parts, " ")
}

func looksLikeConfigLine(line string) bool {
	if strings.Contains(line, ":") && !strings.Contains(line, "://") {
		return true
	}
	return strings.HasPrefix(line, "`") || strings.HasPrefix(line, "POST ") || strings.HasPrefix(line, "GET ")
}

func queryTerms(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return []string{"项目", "功能", "架构", "博客", "rag", "ai"}
	}
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return unicode.IsSpace(r) || strings.ContainsRune("，。、“”‘’!！?？,.:：;；/\\|-_()[]{}<>", r)
	})
	terms := make([]string, 0, len(fields)+4)
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if len([]rune(field)) < 2 {
			continue
		}
		terms = append(terms, field)
	}
	if len(terms) == 0 {
		return []string{"项目", "功能", "架构", "博客", "rag", "ai"}
	}
	return terms
}

func removeMarkdownHeadings(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func fallbackRAGAnswer(question string, chunks []rag.Chunk) string {
	if len(chunks) == 0 {
		return "没有在当前博客知识库中找到高相关内容。你可以先创建或发布相关文章后再检索。"
	}

	lines := []string{
		fmt.Sprintf("关于“%s”，当前知识库里可归纳出以下要点：", strings.TrimSpace(question)),
	}
	for i, c := range chunks {
		if i >= 4 {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s：%s", i+1, c.Title, shortenRunes(strings.TrimSpace(c.Text), 90)))
	}
	lines = append(lines, "")
	lines = append(lines, "参考来源：")
	for i, c := range chunks {
		if i >= 4 {
			break
		}
		lines = append(lines, fmt.Sprintf("- %s (%s)", c.Title, c.Path))
	}
	return strings.Join(lines, "\n")
}

func fallbackRAGDraft(query string, chunks []rag.Chunk) string {
	title := firstUsefulTitle(query, chunks)
	overview := "当前资料较少，以下内容基于检索到的博客片段进行整理，建议补充更多案例与数据后再发布。"
	if len(chunks) > 0 {
		overview = fmt.Sprintf("本文围绕“%s”整理了博客知识库中的已有观点，并将相关内容重新组织成一篇可继续编辑的草稿。", strings.TrimSpace(query))
	}

	lines := []string{
		fmt.Sprintf("# %s", title),
		"",
		"## 导语",
		overview,
		"",
		"## 关键信息整理",
	}

	if len(chunks) == 0 {
		lines = append(lines, "- 当前没有检索到足够资料，建议先补充相关文章后再生成。")
	} else {
		for i, c := range chunks {
			if i >= 4 {
				break
			}
			lines = append(lines, fmt.Sprintf("### %s", c.Title))
			lines = append(lines, shortenRunes(strings.TrimSpace(c.Text), 240))
			lines = append(lines, "")
		}
	}

	lines = append(lines,
		"## 可继续完善的方向",
		"- 补充更具体的案例、指标或工程细节。",
		"- 将重复观点合并，增强章节之间的逻辑递进。",
		"- 在正式发布前增加一节“参考来源”或“延伸阅读”。",
		"",
		"## 结语",
		"这是一篇基于知识库内容自动整理的草稿，可以继续在编辑器中补充与打磨。",
	)
	return strings.Join(lines, "\n")
}

func ragTags(chunks []rag.Chunk) []string {
	seen := map[string]struct{}{
		"rag":   {},
		"agent": {},
		"draft": {},
	}
	tags := []string{"rag", "agent", "draft"}
	for _, chunk := range chunks {
		for _, raw := range strings.FieldsFunc(strings.ToLower(chunk.Slug+" "+chunk.Title), func(r rune) bool {
			return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
		}) {
			if len(raw) < 3 {
				continue
			}
			if _, ok := seen[raw]; ok {
				continue
			}
			seen[raw] = struct{}{}
			tags = append(tags, raw)
			if len(tags) >= 6 {
				return tags
			}
		}
	}
	return tags
}

func ragModelName(fallback bool) string {
	if fallback {
		return "rag-agent-fallback"
	}
	return "rag-agent-llm"
}

func inferRAGIntent(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(lower, "写"), strings.Contains(lower, "生成"), strings.Contains(lower, "create"), strings.Contains(lower, "draft"):
		return "create"
	case strings.Contains(lower, "总结"), strings.Contains(lower, "分析"), strings.Contains(lower, "explain"):
		return "answer"
	default:
		return "answer"
	}
}

func firstUsefulTitle(query string, chunks []rag.Chunk) string {
	query = strings.TrimSpace(query)
	if query != "" {
		return query
	}
	for _, c := range chunks {
		if strings.TrimSpace(c.Title) != "" {
			return c.Title
		}
	}
	return "基于知识库整理的草稿"
}

func shortenRunes(text string, n int) string {
	r := []rune(strings.TrimSpace(text))
	if len(r) <= n || n <= 0 {
		return string(r)
	}
	return string(r[:n]) + "..."
}

func fallbackInlineEdit(selected string, instruction string) string {
	selected = strings.TrimSpace(selected)
	instruction = strings.ToLower(strings.TrimSpace(instruction))
	switch {
	case strings.Contains(instruction, "精简"), strings.Contains(instruction, "简化"), strings.Contains(instruction, "压缩"):
		return shortenRunes(selected, 120)
	case strings.Contains(instruction, "润色"), strings.Contains(instruction, "优化"):
		return strings.ReplaceAll(selected, "。", "，进一步提升可读性。")
	case strings.Contains(instruction, "改标题"), strings.Contains(instruction, "标题"):
		return firstLineAsTitle("# " + selected)
	default:
		return selected
	}
}

func fallbackWritingBrief(prompt string) string {
	title := extractPromptTitle(prompt)
	projectAware := strings.Contains(strings.ToLower(prompt), "项目") || strings.Contains(strings.ToLower(prompt), "project")
	structure := "建议结构：导语 -> 背景与问题 -> 核心方法/实践 -> 常见坑点或经验 -> 总结。"
	keyPoints := "应该覆盖的关键点：概念解释、适用场景、实现步骤、案例或注意事项、结论建议。"
	if projectAware {
		structure = "建议结构：项目概览 -> 核心能力 -> 系统架构 -> 关键模块说明 -> 使用方式与总结。"
		keyPoints = "应该覆盖的关键点：项目目标、核心功能、目录结构、关键模块职责、运行方式、适用场景。"
	}
	return strings.Join([]string{
		"文章目标：围绕用户提出的主题，输出一篇结构清晰、可直接继续编辑的技术博客草稿。",
		"目标读者：对该主题感兴趣、希望快速理解关键概念与实践路径的技术读者。",
		fmt.Sprintf("建议标题：%s", title),
		structure,
		keyPoints,
		"写作语气：专业、清晰、克制，避免空泛表述。",
	}, "\n")
}

func extractPromptTitle(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return "AI 工程实践指南"
	}
	if len([]rune(prompt)) > 36 {
		return shortenRunes(prompt, 36)
	}
	return prompt
}

func extractSuggestedTitle(brief string) string {
	title := normalizeTitle(extractBriefFieldValue(brief, "建议标题", "标题建议", "标题"))
	if title != "" {
		return title
	}
	return "AI 博客草稿"
}

func fallbackArticleTitle(brief string, userPrompt string) string {
	title := normalizeTitle(extractSuggestedTitle(brief))
	if title != "" && !isPlaceholderTitle(title) {
		return title
	}
	return normalizeTitle(nonProjectTitle(title, userPrompt))
}

func nonProjectTitle(title string, userPrompt string) string {
	title = normalizeTitle(title)
	if title != "" && !isPlaceholderTitle(title) {
		return title
	}

	prompt := strings.TrimSpace(userPrompt)
	if prompt == "" {
		return "技术博客草稿"
	}

	prefixes := []string{
		"帮我写一篇关于",
		"帮我写一份关于",
		"帮我写个关于",
		"帮我写一篇",
		"帮我写一份",
		"帮我写个",
		"帮我写",
		"请写一篇关于",
		"请写一份关于",
		"请写个关于",
		"请写一篇",
		"请写一份",
		"请写个",
		"请写",
		"写一篇关于",
		"写一份关于",
		"写个关于",
		"写一篇",
		"写一份",
		"写个",
		"写",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(prompt, prefix) {
			prompt = strings.TrimSpace(strings.TrimPrefix(prompt, prefix))
			break
		}
	}

	suffixes := []string{
		"的详细博客介绍",
		"的详细介绍博客",
		"的详细介绍文章",
		"的详细介绍",
		"的博客介绍",
		"的文章介绍",
		"的博客",
		"的文章",
		"博客介绍",
		"文章介绍",
		"详细介绍",
		"介绍博客",
		"介绍文章",
		"介绍",
		"博客",
		"文章",
	}
	for _, suffix := range suffixes {
		prompt = strings.TrimSpace(strings.TrimSuffix(prompt, suffix))
	}

	candidate := strings.TrimPrefix(prompt, "关于")
	candidate = strings.TrimPrefix(candidate, "一下")
	candidate = strings.Trim(candidate, "：:，,。.!！？?")
	if candidate == "" {
		return "技术博客草稿"
	}
	if !strings.Contains(candidate, "指南") && !strings.Contains(candidate, "详解") && !strings.Contains(candidate, "介绍") {
		candidate += "详解"
	}
	return shortenRunes(candidate, 36)
}

func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}

	for _, line := range strings.Split(strings.ReplaceAll(title, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		title = line
		break
	}

	title = normalizeBriefLine(title)
	for _, label := range []string{"建议标题", "标题"} {
		if value, found, _ := extractLabeledValue(title, label); found {
			title = value
			break
		}
	}

	title = strings.TrimPrefix(title, "#")
	title = strings.TrimSpace(title)
	title = strings.Trim(title, "\"'`“”‘’《》()（）[]")
	title = strings.Join(strings.Fields(title), " ")
	return shortenRunes(title, 40)
}

func resolvedArticleTitle(generatedTitle string, brief string, userPrompt string) string {
	candidates := []string{
		normalizeTitle(generatedTitle),
		normalizeTitle(extractSuggestedTitle(brief)),
		normalizeTitle(fallbackArticleTitle(brief, userPrompt)),
	}

	fallback := ""
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if fallback == "" {
			fallback = candidate
		}
		if !isPlaceholderTitle(candidate) {
			return candidate
		}
	}
	if fallback != "" {
		return fallback
	}
	return "技术博客草稿"
}

func isPlaceholderTitle(title string) bool {
	normalized := strings.ToLower(strings.TrimSpace(title))
	normalized = strings.Trim(normalized, "\"'`“”‘’《》()（）[]")
	normalized = strings.Join(strings.Fields(normalized), "")
	if normalized == "" {
		return true
	}

	switch normalized {
	case "ai博客草稿", "博客草稿", "技术博客草稿", "博客标题", "文章标题", "草稿", "draft", "untitled", "无标题":
		return true
	}
	return false
}

func prependMarkdownTitle(title string, body string) string {
	title = normalizeTitle(title)
	body = strings.TrimSpace(body)
	if title == "" {
		return body
	}
	body = strings.TrimLeft(body, "\n")
	if strings.HasPrefix(body, "# ") {
		lines := strings.Split(body, "\n")
		if len(lines) > 0 {
			lines[0] = "# " + title
		}
		return normalizeGeneratedMarkdown(strings.Join(lines, "\n"))
	}
	if body == "" {
		return "# " + title
	}
	return normalizeGeneratedMarkdown(fmt.Sprintf("# %s\n\n%s", title, body))
}

func fallbackDraftFromBrief(brief string, projectContext string, userPrompt string, projectAware bool) string {
	title := extractSuggestedTitle(brief)
	if !projectAware || !briefLooksProjectAware(brief, userPrompt) {
		return defaultDraft(nonProjectTitle(title, userPrompt))
	}

	return strings.Join([]string{
		fmt.Sprintf("# %s", title),
		"",
		"## 导语",
		projectIntro(projectContext),
		"",
		"## 项目概览",
		projectOverview(projectContext),
		"",
		"## 核心能力",
		projectFeatures(projectContext),
		"",
		"## 架构与模块",
		projectStructure(projectContext),
		"",
		"## 运行方式",
		projectRunbook(projectContext),
		"",
		"## 结语",
		"这篇草稿基于当前项目的 README、配置和关键源码结构整理生成，可以继续在编辑器中补充实现细节与使用案例。",
	}, "\n")
}

func briefLooksProjectAware(brief string, userPrompt string) bool {
	if !referencesCurrentProject(userPrompt) {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(brief))
	return strings.Contains(lower, "这个项目") ||
		strings.Contains(lower, "当前项目") ||
		strings.Contains(lower, "aiblog") ||
		strings.Contains(lower, "我的项目")
}

func projectIntro(projectContext string) string {
	readme := extractSection(projectContext, "[README.md]")
	if readme == "" {
		return "本文介绍当前项目的核心目标、主要功能与整体结构，帮助读者快速理解它的工作方式。"
	}
	return shortenRunes(firstNonEmptyParagraph(readme), 150)
}

func projectOverview(projectContext string) string {
	readme := extractSection(projectContext, "[README.md]")
	if readme == "" {
		return "- 当前项目围绕内容生成、文章管理与检索能力构建。\n- 支持 Markdown 内容保存、编辑与展示。\n- 结合 AI 与 RAG 完成写作和知识检索。"
	}
	section := markdownSection(readme, "Features")
	if section == "" {
		return "- 项目聚焦于博客内容生产、编辑和检索。\n- 提供 AI 写作与 RAG 分析能力。\n- 提供从内容保存到前端渲染的一体化流程。"
	}
	return normalizeBulletLines(section, 4)
}

func projectFeatures(projectContext string) string {
	readme := extractSection(projectContext, "[README.md]")
	if readme == "" {
		return "- 支持博客生成、编辑、发布与检索。\n- 支持前端工作台展示。\n- 支持通过 API 驱动 AI 能力。"
	}
	section := markdownSection(readme, "Features")
	if section == "" {
		return "- 支持博客内容展示与管理。\n- 支持 AI 写作与改稿。\n- 支持 RAG 检索分析。"
	}
	return normalizeBulletLines(section, 6)
}

func projectStructure(projectContext string) string {
	readme := extractSection(projectContext, "[README.md]")
	if readme == "" {
		return "- `cmd/server/main.go` 负责应用启动。\n- `internal/api` 负责 HTTP 接口。\n- `internal/blog` 负责 Markdown 仓库与渲染。\n- `internal/agent` 负责 AI 写作编排。\n- `internal/rag` 负责检索索引与查询。"
	}
	section := markdownSection(readme, "Project Structure")
	if section == "" {
		return "- `cmd/server/main.go` 负责应用启动。\n- `internal/api` 提供 Web API。\n- `internal/blog` 管理文章内容。\n- `internal/agent` 负责编排 AI 生成与改写。\n- `internal/rag` 提供检索与知识库能力。"
	}
	return normalizeBulletLines(section, 8)
}

func projectRunbook(projectContext string) string {
	readme := extractSection(projectContext, "[README.md]")
	if readme == "" {
		return "- 启动服务后可通过浏览器访问前端页面。\n- 通过写作入口生成草稿，文章会保存为 Markdown。\n- 保存或发布后内容会自动进入检索知识库。"
	}
	run := markdownSection(readme, "Run")
	api := markdownSection(readme, "API Quick Reference")
	parts := make([]string, 0, 2)
	if run != "" {
		parts = append(parts, shortenRunes(strings.TrimSpace(run), 220))
	}
	if api != "" {
		parts = append(parts, "关键接口：\n"+normalizeBulletLines(api, 6))
	}
	if len(parts) == 0 {
		return "- 启动服务后即可在浏览器中访问工作台。\n- 生成文章后会保存为 Markdown 并渲染到前端。\n- 文章内容会被自动纳入 RAG 知识库。"
	}
	return strings.Join(parts, "\n\n")
}

func referencesCurrentProject(prompt string) bool {
	lower := strings.ToLower(strings.TrimSpace(prompt))
	if lower == "" {
		return false
	}

	keywords := []string{
		"这个项目",
		"当前项目",
		"我的项目",
		"本项目",
		"aiblog",
		"这个博客系统",
		"本站",
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func extractSection(text string, marker string) string {
	start := strings.Index(text, marker)
	if start < 0 {
		return ""
	}
	return strings.TrimSpace(text[start+len(marker):])
}

func firstNonEmptyParagraph(text string) string {
	for _, part := range strings.Split(text, "\n\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		return part
	}
	return strings.TrimSpace(text)
}

func markdownSection(text string, heading string) string {
	lines := strings.Split(text, "\n")
	target := "## " + heading
	var out []string
	inSection := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			if inSection {
				break
			}
			if trimmed == target {
				inSection = true
			}
			continue
		}
		if inSection {
			out = append(out, line)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func normalizeBulletLines(text string, maxLines int) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, maxLines)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			out = append(out, line)
		} else if strings.HasPrefix(line, "`") {
			out = append(out, "- "+line)
		}
		if len(out) >= maxLines {
			break
		}
	}
	if len(out) == 0 {
		return "- " + shortenRunes(strings.ReplaceAll(strings.TrimSpace(text), "\n", " "), 220)
	}
	return strings.Join(out, "\n")
}

func normalizeGeneratedMarkdown(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func normalizeSectionBody(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") || strings.HasPrefix(trimmed, "## ") {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return normalizeGeneratedMarkdown(strings.Join(cleaned, "\n"))
}

func (s *Service) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Printf(format, args...)
	}
}

func truncateForLog(text string, max int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if max <= 0 {
		return text
	}
	r := []rune(text)
	if len(r) <= max {
		return text
	}
	return string(r[:max]) + "..."
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
