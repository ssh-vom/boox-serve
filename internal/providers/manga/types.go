package manga

import (
	"errors"
	"fmt"
)

type SearchResult struct {
	ID       string
	Title    string
	CoverURL string
}

type Chapter struct {
	ID             string
	Number         string
	Title          string
	Volume         string
	NumericChapter float64
}

var (
	ErrChapterMetadataMissing = errors.New("chapter metadata missing")
	ErrChapterNoPages         = errors.New("chapter has no pages")
)

func FormatChapterLabel(chapter Chapter) string {
	label := "Chapter"
	if chapter.Number != "" {
		label = fmt.Sprintf("Chapter %s", chapter.Number)
	}
	if chapter.Title != "" {
		label = fmt.Sprintf("%s - %s", label, chapter.Title)
	}
	if chapter.Volume != "" {
		label = fmt.Sprintf("Volume %s, %s", chapter.Volume, label)
	}
	return label
}
