package blog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrVersionConflict = errors.New("version conflict")

type Repository struct {
	root string
}

func NewRepository(root string) *Repository {
	return &Repository{root: filepath.Clean(root)}
}

func (r *Repository) EnsureStructure() error {
	dirs := []string{
		filepath.Join(r.root, "posts"),
		filepath.Join(r.root, "drafts"),
		filepath.Join(r.root, "assets"),
		filepath.Join(r.root, ".history"),
		filepath.Join(r.root, ".locks"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) List(includeDrafts bool) ([]ListItem, error) {
	paths := []string{filepath.Join(r.root, "posts")}
	if includeDrafts {
		paths = append(paths, filepath.Join(r.root, "drafts"))
	}

	items := make([]ListItem, 0)
	for _, dir := range paths {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".md") {
				return nil
			}
			post, err := r.readByPath(path)
			if err != nil {
				return nil
			}
			if post.Status == "draft" && !includeDrafts {
				return nil
			}
			items = append(items, ListItem{
				Title:     post.Title,
				Slug:      post.Slug,
				Summary:   post.Summary,
				Date:      post.Date,
				Tags:      post.Tags,
				Status:    post.Status,
				UpdatedAt: post.UpdatedAt,
			})
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Date.IsZero() && items[j].Date.IsZero() {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].Date.After(items[j].Date)
	})
	return items, nil
}

func (r *Repository) Search(query string, includeDrafts bool) ([]ListItem, error) {
	items, err := r.List(includeDrafts)
	if err != nil {
		return nil, err
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return items, nil
	}
	terms := strings.Fields(q)
	if len(terms) == 0 {
		return items, nil
	}

	filtered := make([]ListItem, 0, len(items))
	for _, item := range items {
		haystack := strings.ToLower(strings.Join([]string{
			item.Title,
			item.Slug,
			item.Summary,
			strings.Join(item.Tags, " "),
		}, " "))
		if matchesAnyTerm(haystack, terms) {
			filtered = append(filtered, item)
		}
	}
	return filtered, nil
}

func (r *Repository) GetBySlug(slug string) (*Post, error) {
	slug = sanitizeSlug(slug)
	candidates := []string{
		filepath.Join(r.root, "posts", slug+".md"),
		filepath.Join(r.root, "drafts", slug+".md"),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			post, err := r.readByPath(path)
			if err != nil {
				return nil, err
			}
			return post, nil
		}
	}
	return nil, os.ErrNotExist
}

func (r *Repository) Save(input *Post, expectedVersion string) (*Post, error) {
	if err := r.EnsureStructure(); err != nil {
		return nil, err
	}

	input.Slug = sanitizeSlug(input.Slug)
	if input.Slug == "untitled" && input.Title != "" {
		input.Slug = sanitizeSlug(input.Title)
	}
	if input.Slug == "" {
		input.Slug = fmt.Sprintf("post-%d", time.Now().Unix())
	}
	if input.Title == "" {
		input.Title = "Untitled"
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.Date.IsZero() {
		input.Date = time.Now()
	}

	targetDir := "drafts"
	if input.Status == "published" {
		targetDir = "posts"
	}
	targetPath := filepath.Join(r.root, targetDir, input.Slug+".md")

	if existing, err := r.readIfExists(targetPath); err == nil {
		if expectedVersion != "" && existing.Version != expectedVersion {
			return nil, ErrVersionConflict
		}
		if err := r.backup(targetPath); err != nil {
			return nil, err
		}
	}

	// Ensure old file in the opposite folder is moved out of the way.
	otherDir := "posts"
	if targetDir == "posts" {
		otherDir = "drafts"
	}
	otherPath := filepath.Join(r.root, otherDir, input.Slug+".md")
	if _, err := os.Stat(otherPath); err == nil {
		if err := os.Remove(otherPath); err != nil {
			return nil, err
		}
	}

	content := formatMarkdown(input.FrontMatter, input.Body)
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return r.readByPath(targetPath)
}

func (r *Repository) Publish(slug string, expectedVersion string) (*Post, error) {
	post, err := r.GetBySlug(slug)
	if err != nil {
		return nil, err
	}
	post.Status = "published"
	return r.Save(post, expectedVersion)
}

func (r *Repository) Delete(slug string, expectedVersion string) error {
	slug = sanitizeSlug(slug)
	paths := []string{
		filepath.Join(r.root, "posts", slug+".md"),
		filepath.Join(r.root, "drafts", slug+".md"),
	}

	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return err
		}

		existing, err := r.readByPath(path)
		if err != nil {
			return err
		}
		if expectedVersion != "" && existing.Version != expectedVersion {
			return ErrVersionConflict
		}
		if err := r.backup(path); err != nil {
			return err
		}
		return os.Remove(path)
	}
	return os.ErrNotExist
}

func (r *Repository) readIfExists(path string) (*Post, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return r.readByPath(path)
}

func (r *Repository) readByPath(path string) (*Post, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, err := parseMarkdown(string(raw))
	if err != nil {
		return nil, err
	}
	if fm.Slug == "" {
		fm.Slug = sanitizeSlug(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}
	if fm.Title == "" {
		fm.Title = fm.Slug
	}
	html, err := renderMarkdown(body)
	if err != nil {
		return nil, err
	}
	stat, _ := os.Stat(path)
	updatedAt := time.Now()
	if stat != nil {
		updatedAt = stat.ModTime()
	}
	version := hash(string(raw))
	return &Post{
		FrontMatter: fm,
		Path:        relativeToRoot(r.root, path),
		Body:        body,
		HTML:        html,
		UpdatedAt:   updatedAt,
		Version:     version,
	}, nil
}

func (r *Repository) backup(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	base := filepath.Base(path)
	historyName := fmt.Sprintf("%s.%s.bak", base, stamp)
	historyPath := filepath.Join(r.root, ".history", historyName)
	return os.WriteFile(historyPath, raw, 0o644)
}

func hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func relativeToRoot(root string, full string) string {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return full
	}
	return filepath.ToSlash(rel)
}

func matchesAnyTerm(haystack string, terms []string) bool {
	for _, term := range terms {
		if term == "" {
			continue
		}
		if strings.Contains(haystack, term) {
			return true
		}
	}
	return false
}
