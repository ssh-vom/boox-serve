package mangadex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ssh-vom/boox-serve/internal/providers/manga"
)

const (
	baseURL           = "https://api.mangadex.org"
	coverBaseURL      = "https://uploads.mangadex.org"
	mangaDexUserAgent = "boox-serve/0.1"
)

type Provider struct {
	httpClient *http.Client
	apiKey     string
}

func New(httpClient *http.Client, apiKey string) *Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &Provider{httpClient: httpClient, apiKey: strings.TrimSpace(apiKey)}
}

func (provider *Provider) Search(ctx context.Context, query string) ([]manga.SearchResult, error) {
	searchURL, err := url.Parse(baseURL + "/manga")
	if err != nil {
		return nil, fmt.Errorf("error parsing search URL: %w", err)
	}

	q := searchURL.Query()
	q.Set("title", query)
	q.Set("limit", "20")
	q.Add("includes[]", "cover_art")
	searchURL.RawQuery = q.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("error building search request: %w", err)
	}
	provider.addHeaders(request)

	response, err := provider.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("error making search request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search request failed: %s", response.Status)
	}

	var result mangaSearchResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("error parsing search response: %w", err)
	}

	results := make([]manga.SearchResult, 0, len(result.Data))
	for _, entry := range result.Data {
		title := pickTitle(entry.Attributes.Title)
		if title == "" {
			continue
		}

		coverFileName := pickCoverFileName(entry.Relationships)
		coverURL := buildCoverURL(entry.ID, coverFileName)
		results = append(results, manga.SearchResult{ID: entry.ID, Title: title, CoverURL: coverURL})
	}

	return results, nil
}

func (provider *Provider) FetchChapters(ctx context.Context, mangaID string) ([]manga.Chapter, error) {
	var allChapters []manga.Chapter
	seen := make(map[string]bool)
	limit := 100
	offset := 0

	for {
		endpoint := fmt.Sprintf("%s/chapter?limit=%d&offset=%d&manga=%s&contentRating[]=safe&contentRating[]=suggestive&contentRating[]=erotica&includeFutureUpdates=1&order[volume]=asc&order[chapter]=asc&translatedLanguage[]=en", baseURL, limit, offset, mangaID)

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building image request: %w", err)
		}
		provider.addHeaders(request)

		response, err := provider.httpClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("error fetching chapters: %w", err)
		}

		var chapterResponse chapterResponse
		if err := json.NewDecoder(response.Body).Decode(&chapterResponse); err != nil {
			response.Body.Close()
			return nil, fmt.Errorf("error parsing chapter response: %w", err)
		}
		response.Body.Close()

		for _, chapterData := range chapterResponse.Data {
			if seen[chapterData.ID] {
				continue
			}
			if chapterData.Attributes.ExternalURL != "" || chapterData.Attributes.Pages == 0 {
				continue
			}
			seen[chapterData.ID] = true

			chapterNumber, _ := strconv.ParseFloat(chapterData.Attributes.Chapter, 64)
			chapter := manga.Chapter{
				ID:             chapterData.ID,
				Number:         chapterData.Attributes.Chapter,
				Title:          chapterData.Attributes.Title,
				Volume:         chapterData.Attributes.Volume,
				NumericChapter: chapterNumber,
			}
			allChapters = append(allChapters, chapter)
		}

		if len(chapterResponse.Data) < limit {
			break
		}
		offset += limit
	}

	sort.Slice(allChapters, func(i, j int) bool {
		if allChapters[i].NumericChapter == allChapters[j].NumericChapter {
			return allChapters[i].Volume < allChapters[j].Volume
		}
		return allChapters[i].NumericChapter < allChapters[j].NumericChapter
	})

	return allChapters, nil
}

func (provider *Provider) DownloadChapterImages(ctx context.Context, chapter manga.Chapter) ([][]byte, error) {
	chapterDetails, err := provider.fetchChapterDetails(ctx, chapter.ID)
	if err != nil {
		return nil, err
	}

	return provider.downloadChapterImages(ctx, chapterDetails)
}

func (provider *Provider) FetchCover(ctx context.Context, coverURL string) ([]byte, error) {
	if coverURL == "" {
		return nil, errors.New("cover url missing")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error building cover request: %w", err)
	}
	provider.addHeaders(request)

	response, err := provider.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("error fetching cover: %w", err)
	}

	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		return nil, fmt.Errorf("error reading cover: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover request failed: %s", response.Status)
	}

	return body, nil
}

func (provider *Provider) addHeaders(request *http.Request) {
	request.Header.Set("User-Agent", mangaDexUserAgent)
	if provider.apiKey == "" {
		return
	}
	request.Header.Set("Authorization", "Bearer "+provider.apiKey)
	request.Header.Set("X-Api-Key", provider.apiKey)
}

type mangaRelationship struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Attributes struct {
		FileName string `json:"fileName"`
	} `json:"attributes"`
}

type mangaSearchResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Title map[string]string `json:"title"`
		} `json:"attributes"`
		Relationships []mangaRelationship `json:"relationships"`
	} `json:"data"`
}

type chapterResponse struct {
	Data []struct {
		ID         string `json:"id"`
		Attributes struct {
			Volume      string `json:"volume"`
			Chapter     string `json:"chapter"`
			Title       string `json:"title"`
			Pages       int    `json:"pages"`
			ExternalURL string `json:"externalUrl"`
		} `json:"attributes"`
	} `json:"data"`
}

type chapterDetails struct {
	Result  string `json:"result"`
	BaseURL string `json:"baseUrl"`
	Chapter struct {
		Hash      string   `json:"hash"`
		Data      []string `json:"data"`
		DataSaver []string `json:"dataSaver"`
	} `json:"chapter"`
}

func buildCoverURL(mangaID, fileName string) string {
	if mangaID == "" || fileName == "" {
		return ""
	}

	return fmt.Sprintf("%s/covers/%s/%s.256.jpg", coverBaseURL, mangaID, fileName)
}

func pickCoverFileName(relationships []mangaRelationship) string {
	for _, relation := range relationships {
		if relation.Type != "cover_art" {
			continue
		}
		if relation.Attributes.FileName != "" {
			return relation.Attributes.FileName
		}
	}

	return ""
}

func pickTitle(titles map[string]string) string {
	if titles == nil {
		return ""
	}

	if value, ok := titles["en"]; ok {
		return value
	}

	for _, value := range titles {
		return value
	}

	return ""
}

func (provider *Provider) fetchChapterDetails(ctx context.Context, chapterID string) (*chapterDetails, error) {
	endpoint := fmt.Sprintf("%s/at-home/server/%s", baseURL, chapterID)

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building chapter request: %w", err)
		}
		provider.addHeaders(request)

		response, err := provider.httpClient.Do(request)
		if err != nil {
			lastErr = fmt.Errorf("error fetching chapter details: %w", err)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("error reading chapter details response: %w", err)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("chapter details request failed: %s", strings.TrimSpace(string(body)))
			if shouldRetry(response.StatusCode) && attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		var details chapterDetails
		if err := json.Unmarshal(body, &details); err != nil {
			lastErr = fmt.Errorf("error parsing chapter details: %w", err)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		if details.Result != "ok" {
			lastErr = fmt.Errorf("chapter details request returned %q", details.Result)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		if details.BaseURL == "" || details.Chapter.Hash == "" {
			lastErr = fmt.Errorf(
				"%w: chapter details missing baseUrl/hash for %s\nresult=%q baseUrl=%q hash=%q data=%d dataSaver=%d",
				manga.ErrChapterMetadataMissing,
				chapterID,
				details.Result,
				details.BaseURL,
				details.Chapter.Hash,
				len(details.Chapter.Data),
				len(details.Chapter.DataSaver),
			)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		return &details, nil
	}

	if lastErr == nil {
		lastErr = errors.New("unable to fetch chapter details")
	}
	return nil, lastErr
}

func (provider *Provider) downloadChapterImages(ctx context.Context, chapter *chapterDetails) ([][]byte, error) {
	if chapter.BaseURL == "" || chapter.Chapter.Hash == "" {
		return nil, fmt.Errorf("%w: invalid chapter details for download (baseUrl=%q hash=%q)", manga.ErrChapterMetadataMissing, chapter.BaseURL, chapter.Chapter.Hash)
	}

	baseURL := chapter.BaseURL
	hash := chapter.Chapter.Hash
	fileNames := chapter.Chapter.Data
	pathSegment := "data"

	if len(fileNames) == 0 && len(chapter.Chapter.DataSaver) > 0 {
		fileNames = chapter.Chapter.DataSaver
		pathSegment = "data-saver"
	}

	if len(fileNames) == 0 {
		return nil, fmt.Errorf("%w: no pages returned for chapter %s (data=%d dataSaver=%d)", manga.ErrChapterNoPages, hash, len(chapter.Chapter.Data), len(chapter.Chapter.DataSaver))
	}

	images := make([][]byte, len(fileNames))

	for i, fileName := range fileNames {
		endpoint := fmt.Sprintf("%s/%s/%s/%s", baseURL, pathSegment, hash, fileName)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building chapter request: %w", err)
		}
		provider.addHeaders(request)

		response, err := provider.httpClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("error downloading %s: %w", fileName, err)
		}

		imgData, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("error reading %s: %w", fileName, err)
		}

		if len(imgData) == 0 {
			return nil, fmt.Errorf("downloaded image %s is empty", fileName)
		}

		images[i] = imgData
	}

	return images, nil
}

func shouldRetry(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}

func waitWithBackoff(ctx context.Context, attempt int) {
	wait := time.Duration(attempt*attempt) * 250 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < wait {
			return
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
