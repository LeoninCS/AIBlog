package rag

import (
	"fmt"
	"strings"

	"aiblog/internal/blog"
)

type Builder struct {
	chunkSize int
	overlap   int
	topK      int
	index     *Index
}

func NewBuilder(chunkSize, overlap, topK int) *Builder {
	if topK <= 0 {
		topK = 5
	}
	return &Builder{
		chunkSize: chunkSize,
		overlap:   overlap,
		topK:      topK,
		index:     NewIndex(),
	}
}

func (b *Builder) Rebuild(items []blog.Post) int {
	chunks := make([]Chunk, 0)
	for _, p := range items {
		parts := ChunkMarkdown(p.Slug, p.Title, p.Path, p.Body, b.chunkSize, b.overlap, p.UpdatedAt.Unix())
		chunks = append(chunks, parts...)
	}
	b.index.Replace(chunks)
	return len(chunks)
}

func (b *Builder) Query(question string) Result {
	chunks := b.index.Search(question, b.topK)
	summary := summarize(question, chunks)
	return Result{
		Answer:  summary,
		Chunks:  chunks,
		Query:   question,
		Model:   "lexical-rag-mvp",
		Summary: summary,
	}
}

func summarize(question string, chunks []Chunk) string {
	if len(chunks) == 0 {
		return "没有在当前博客知识库中找到高相关内容。你可以先创建或发布相关文章后再检索。"
	}
	lines := make([]string, 0, len(chunks)+2)
	lines = append(lines, fmt.Sprintf("问题：%s", strings.TrimSpace(question)))
	lines = append(lines, "基于博客内容的要点：")
	for i, c := range chunks {
		excerpt := strings.TrimSpace(c.Text)
		if len([]rune(excerpt)) > 120 {
			r := []rune(excerpt)
			excerpt = string(r[:120]) + "..."
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, c.Title, excerpt))
	}
	return strings.Join(lines, "\n")
}
