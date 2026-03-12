package blog

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var fmLine = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.*)$`)

func parseMarkdown(raw string) (FrontMatter, string, error) {
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return FrontMatter{}, strings.TrimSpace(normalized), nil
	}

	idx := strings.Index(normalized[4:], "\n---\n")
	if idx < 0 {
		return FrontMatter{}, "", errors.New("invalid front matter: missing closing ---")
	}

	metaText := normalized[4 : 4+idx]
	body := strings.TrimSpace(normalized[4+idx+5:])
	fm, err := parseFrontMatter(metaText)
	if err != nil {
		return FrontMatter{}, "", err
	}
	return fm, body, nil
}

func parseFrontMatter(text string) (FrontMatter, error) {
	fm := FrontMatter{}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := fmLine.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		value := strings.TrimSpace(match[2])
		value = strings.Trim(value, `"`)
		switch key {
		case "title":
			fm.Title = value
		case "slug":
			fm.Slug = sanitizeSlug(value)
		case "status":
			fm.Status = strings.ToLower(value)
		case "summary":
			fm.Summary = value
		case "tags":
			fm.Tags = parseTags(value)
		case "date":
			if parsed, err := parseDate(value); err == nil {
				fm.Date = parsed
			}
		}
	}
	if fm.Status == "" {
		fm.Status = "draft"
	}
	return fm, nil
}

func formatMarkdown(fm FrontMatter, body string) string {
	if fm.Date.IsZero() {
		fm.Date = time.Now()
	}
	if fm.Status == "" {
		fm.Status = "draft"
	}
	tags := ""
	if len(fm.Tags) > 0 {
		tags = strings.Join(fm.Tags, ",")
	}
	return fmt.Sprintf("---\ntitle: %s\nslug: %s\ndate: %s\ntags: %s\nstatus: %s\nsummary: %s\n---\n\n%s\n",
		escapeFM(fm.Title),
		escapeFM(fm.Slug),
		fm.Date.Format(time.RFC3339),
		escapeFM(tags),
		escapeFM(fm.Status),
		escapeFM(fm.Summary),
		strings.TrimSpace(body),
	)
}

func parseTags(value string) []string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(strings.Trim(p, `"'`))
		if t == "" {
			continue
		}
		tags = append(tags, t)
	}
	return tags
}

func parseDate(value string) (time.Time, error) {
	formats := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	for _, f := range formats {
		if t, err := time.Parse(f, value); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date: %s", value)
}

func escapeFM(value string) string {
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

func sanitizeSlug(slug string) string {
	slug = strings.TrimSpace(strings.ToLower(slug))
	slug = strings.ReplaceAll(slug, " ", "-")
	builder := strings.Builder{}
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			builder.WriteRune(r)
		}
	}
	clean := strings.Trim(builder.String(), "-")
	if clean == "" {
		return "untitled"
	}
	return clean
}
