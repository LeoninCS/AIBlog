package blog

import "time"

type FrontMatter struct {
	Title   string    `json:"title"`
	Slug    string    `json:"slug"`
	Date    time.Time `json:"date"`
	Tags    []string  `json:"tags"`
	Status  string    `json:"status"`
	Summary string    `json:"summary"`
}

type Post struct {
	FrontMatter
	Path      string    `json:"path"`
	Body      string    `json:"body"`
	HTML      string    `json:"html"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   string    `json:"version"`
}

type ListItem struct {
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Summary   string    `json:"summary"`
	Date      time.Time `json:"date"`
	Tags      []string  `json:"tags"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}
