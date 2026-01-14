package libgen

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ssh-vom/boox-serve/internal/providers/textbooks"
)

const defaultSource = "libgen.is"

type Provider struct {
	httpClient *http.Client
	source     string
}

func New(httpClient *http.Client, source string) *Provider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if strings.TrimSpace(source) == "" {
		source = defaultSource
	}

	return &Provider{httpClient: httpClient, source: source}
}

func (provider *Provider) Search(ctx context.Context, query string) ([]textbooks.Result, error) {
	urlString := fmt.Sprintf("https://%s/search.php?req=%s", provider.source, url.QueryEscape(query))

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlString, nil)
	if err != nil {
		return nil, fmt.Errorf("error building search request: %w", err)
	}

	response, err := provider.httpClient.Do(request)
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

	var results []textbooks.Result
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
		results = append(results, textbooks.Result{
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
