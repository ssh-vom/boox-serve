package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

type LibGenResult struct {
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

type MangaSearchResult struct {
	ID       string
	Title    string
	CoverURL string
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

func SearchManga(ctx context.Context, httpClient *http.Client, query string) ([]MangaSearchResult, error) {
	baseURL := "https://api.mangadex.org"

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
	addMangaDexHeaders(request)

	response, err := httpClient.Do(request)
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

	results := make([]MangaSearchResult, 0, len(result.Data))
	for _, manga := range result.Data {
		title := pickTitle(manga.Attributes.Title)
		if title == "" {
			continue
		}

		coverFileName := pickCoverFileName(manga.Relationships)
		coverURL := buildCoverURL(manga.ID, coverFileName)
		results = append(results, MangaSearchResult{ID: manga.ID, Title: title, CoverURL: coverURL})
	}

	return results, nil
}

func buildCoverURL(mangaID, fileName string) string {
	if mangaID == "" || fileName == "" {
		return ""
	}

	return fmt.Sprintf("https://uploads.mangadex.org/covers/%s/%s.256.jpg", mangaID, fileName)
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

func SearchLibraryGenesis(ctx context.Context, httpClient *http.Client, source, query string) ([]LibGenResult, error) {
	urlString := fmt.Sprintf("https://%s/search.php?req=%s", source, url.QueryEscape(query))

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlString, nil)
	if err != nil {
		return nil, fmt.Errorf("error building search request: %w", err)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("error making search request: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search request failed: %s", response.Status)
	}

	doc, err := goquery.NewDocumentFromReader(response.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to parse search response: %w", err)
	}

	var results []LibGenResult
	doc.Find("table.c tr").Each(func(i int, selection *goquery.Selection) {
		if i == 0 {
			return
		}

		id := strings.TrimSpace(selection.Find("td:nth-child(1)").Text())
		title := strings.TrimSpace(selection.Find("td:nth-child(3) a").Text())
		author := strings.TrimSpace(selection.Find("td:nth-child(2)").Text())
		publisher := strings.TrimSpace(selection.Find("td:nth-child(4)").Text())
		edition := strings.TrimSpace(selection.Find("td:nth-child(3) a").Text())
		size := strings.TrimSpace(selection.Find("td:nth-child(8)").Text())
		extension := strings.TrimSpace(selection.Find("td:nth-child(9)").Text())
		isbn := strings.TrimSpace(selection.Find("td:nth-child(5)").Text())
		fullURL := selection.Find("#"+id).AttrOr("href", "")

		var md5Hash string
		if parts := strings.Split(fullURL, "?"); len(parts) > 1 {
			md5Hash = parts[1]
		}

		title = strings.Replace(title, isbn, "", 1)
		results = append(results, LibGenResult{
			ID:        id,
			Number:    i,
			Title:     strings.TrimSpace(title),
			Author:    author,
			Publisher: publisher,
			Edition:   edition,
			ISBN:      isbn,
			URL:       fullURL,
			Size:      size,
			Extension: extension,
			Hash:      md5Hash,
		})
	})

	return results, nil
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
