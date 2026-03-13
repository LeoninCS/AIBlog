package rag

import (
	"sort"
	"strings"
)

type Chunk struct {
	ID      string  `json:"id"`
	Slug    string  `json:"slug"`
	Title   string  `json:"title"`
	Path    string  `json:"path"`
	Text    string  `json:"text"`
	Index   int     `json:"index"`
	Score   float64 `json:"score"`
	Updated int64   `json:"updated"`
}

type Result struct {
	Answer  string  `json:"answer"`
	Chunks  []Chunk `json:"chunks"`
	Query   string  `json:"query"`
	Model   string  `json:"model"`
	Summary string  `json:"summary"`
	Intent  string  `json:"intent,omitempty"`
}

type Index struct {
	chunks []Chunk
}

func NewIndex() *Index {
	return &Index{chunks: make([]Chunk, 0)}
}

func (idx *Index) Replace(chunks []Chunk) {
	idx.chunks = chunks
}

func (idx *Index) Search(query string, topK int) []Chunk {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" || len(idx.chunks) == 0 {
		return nil
	}
	terms := tokenize(query)
	if len(terms) == 0 {
		return nil
	}

	scored := make([]Chunk, 0, len(idx.chunks))
	for _, c := range idx.chunks {
		text := strings.ToLower(c.Text + " " + c.Title + " " + c.Slug)
		score := scoreText(text, terms)
		if score <= 0 {
			continue
		}
		clone := c
		clone.Score = score
		scored = append(scored, clone)
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].Updated > scored[j].Updated
		}
		return scored[i].Score > scored[j].Score
	})

	if topK <= 0 || topK > len(scored) {
		topK = len(scored)
	}
	return scored[:topK]
}

func tokenize(text string) []string {
	replacer := strings.NewReplacer(
		"，", " ", "。", " ", "、", " ", ",", " ", ".", " ",
		"?", " ", "？", " ", "!", " ", "！", " ", "\n", " ", "\t", " ",
	)
	text = replacer.Replace(text)
	parts := strings.Fields(text)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) < 2 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func scoreText(text string, terms []string) float64 {
	if text == "" {
		return 0
	}
	score := 0.0
	for _, term := range terms {
		count := strings.Count(text, term)
		if count == 0 {
			continue
		}
		score += float64(count)
		if strings.Contains(text, "# "+term) {
			score += 1.5
		}
	}
	return score
}
