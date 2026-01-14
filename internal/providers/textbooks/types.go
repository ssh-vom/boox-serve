package textbooks

import "context"

type Result struct {
	ID        string
	Number    int
	Title     string
	Author    string
	Publisher string
	Edition   string
	ISBN      string
	URL       string
	Size      string
	Extension string
	Hash      string
}

type Provider interface {
	Search(ctx context.Context, query string) ([]Result, error)
}
