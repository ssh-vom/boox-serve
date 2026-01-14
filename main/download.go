package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ChapterResponse struct {
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

type Chapter struct {
	ID             string
	Number         string
	Title          string
	Volume         string
	NumericChapter float64
}

type ChapterDetails struct {
	Result  string `json:"result"`
	BaseURL string `json:"baseUrl"`
	Chapter struct {
		Hash      string   `json:"hash"`
		Data      []string `json:"data"`
		DataSaver []string `json:"dataSaver"`
	} `json:"chapter"`
}

var (
	errChapterMetadataMissing = errors.New("chapter metadata missing")
	errChapterNoPages         = errors.New("chapter has no pages")
)

func FetchChapters(ctx context.Context, httpClient *http.Client, mangaID string) ([]Chapter, error) {
	var allChapters []Chapter
	seen := make(map[string]bool)
	limit := 100
	offset := 0

	for {
		endpoint := fmt.Sprintf("https://api.mangadex.org/chapter?limit=%d&offset=%d&manga=%s&contentRating[]=safe&contentRating[]=suggestive&contentRating[]=erotica&includeFutureUpdates=1&order[volume]=asc&order[chapter]=asc&translatedLanguage[]=en", limit, offset, mangaID)

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building image request: %w", err)
		}
		addMangaDexHeaders(request)

		response, err := httpClient.Do(request)

		if err != nil {
			return nil, fmt.Errorf("error fetching chapters: %w", err)
		}

		var chapterResponse ChapterResponse
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
			chapter := Chapter{
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

func FetchChapterDetails(ctx context.Context, httpClient *http.Client, chapterID string) (*ChapterDetails, error) {
	endpoint := fmt.Sprintf("https://api.mangadex.org/at-home/server/%s", chapterID)

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building chapter request: %w", err)
		}
		addMangaDexHeaders(request)

		response, err := httpClient.Do(request)
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

		var chapterDetails ChapterDetails
		if err := json.Unmarshal(body, &chapterDetails); err != nil {
			lastErr = fmt.Errorf("error parsing chapter details: %w", err)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		if chapterDetails.Result != "ok" {
			lastErr = fmt.Errorf("chapter details request returned %q", chapterDetails.Result)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		if chapterDetails.BaseURL == "" || chapterDetails.Chapter.Hash == "" {
			lastErr = fmt.Errorf(
				"%w: chapter details missing baseUrl/hash for %s\nresult=%q baseUrl=%q hash=%q data=%d dataSaver=%d",
				errChapterMetadataMissing,
				chapterID,
				chapterDetails.Result,
				chapterDetails.BaseURL,
				chapterDetails.Chapter.Hash,
				len(chapterDetails.Chapter.Data),
				len(chapterDetails.Chapter.DataSaver),
			)
			if attempt < 3 {
				waitWithBackoff(ctx, attempt)
				continue
			}
			return nil, lastErr
		}

		return &chapterDetails, nil
	}

	if lastErr == nil {
		lastErr = errors.New("unable to fetch chapter details")
	}
	return nil, lastErr
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

func DownloadChapterImages(ctx context.Context, httpClient *http.Client, chapter *ChapterDetails) ([][]byte, error) {
	if chapter.BaseURL == "" || chapter.Chapter.Hash == "" {
		return nil, fmt.Errorf("%w: invalid chapter details for download (baseUrl=%q hash=%q)", errChapterMetadataMissing, chapter.BaseURL, chapter.Chapter.Hash)
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
		return nil, fmt.Errorf("%w: no pages returned for chapter %s (data=%d dataSaver=%d)", errChapterNoPages, hash, len(chapter.Chapter.Data), len(chapter.Chapter.DataSaver))
	}

	images := make([][]byte, len(fileNames))

	for i, fileName := range fileNames {
		endpoint := fmt.Sprintf("%s/%s/%s/%s", baseURL, pathSegment, hash, fileName)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("error building chapter request: %w", err)
		}
		addMangaDexHeaders(request)

		response, err := httpClient.Do(request)

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

func CreateCBZ(chapterName string, images [][]byte) ([]byte, error) {
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	for i, imgData := range images {
		fileName := fmt.Sprintf("%s_page_%03d.jpg", chapterName, i+1)
		writer, err := zipWriter.Create(fileName)
		if err != nil {
			return nil, fmt.Errorf("error creating zip entry %s: %w", fileName, err)
		}

		n, err := writer.Write(imgData)
		if err != nil {
			return nil, fmt.Errorf("error writing image data for %s: %w", fileName, err)
		}
		if n != len(imgData) {
			return nil, fmt.Errorf("incomplete write for %s: wrote %d of %d bytes", fileName, n, len(imgData))
		}
	}

	if err := zipWriter.Close(); err != nil {
		return nil, fmt.Errorf("error closing zip writer: %w", err)
	}

	cbzData := buf.Bytes()
	if len(cbzData) == 0 {
		return nil, fmt.Errorf("created CBZ file is empty")
	}

	return cbzData, nil
}
