package rag

import (
	"fmt"
	"strings"
)

func ChunkMarkdown(slug, title, path, text string, chunkSize, overlap int, updated int64) []Chunk {
	if chunkSize <= 0 {
		chunkSize = 900
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}

	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	paragraphs := strings.Split(normalized, "\n\n")
	windows := make([]string, 0)
	buf := strings.Builder{}

	for _, para := range paragraphs {
		p := strings.TrimSpace(para)
		if p == "" {
			continue
		}
		if buf.Len()+len(p)+2 <= chunkSize {
			if buf.Len() > 0 {
				buf.WriteString("\n\n")
			}
			buf.WriteString(p)
			continue
		}
		if buf.Len() > 0 {
			windows = append(windows, buf.String())
		}
		tail := tailWithOverlap(buf.String(), overlap)
		buf.Reset()
		if tail != "" {
			buf.WriteString(tail)
			if len(p) > 0 {
				buf.WriteString("\n\n")
			}
		}
		buf.WriteString(p)
	}
	if buf.Len() > 0 {
		windows = append(windows, buf.String())
	}

	chunks := make([]Chunk, 0, len(windows))
	for i, w := range windows {
		chunks = append(chunks, Chunk{
			ID:      fmt.Sprintf("%s#%d", slug, i),
			Slug:    slug,
			Title:   title,
			Path:    path,
			Text:    strings.TrimSpace(w),
			Index:   i,
			Updated: updated,
		})
	}
	return chunks
}

func tailWithOverlap(text string, overlap int) string {
	if overlap <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= overlap {
		return text
	}
	return string(runes[len(runes)-overlap:])
}
