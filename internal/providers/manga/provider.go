package manga

import "context"

type Provider interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
	FetchChapters(ctx context.Context, mangaID string) ([]Chapter, error)
	DownloadChapterImages(ctx context.Context, chapter Chapter) ([][]byte, error)
	FetchCover(ctx context.Context, coverURL string) ([]byte, error)
}
