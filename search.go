package main

import (
	"fmt"
	"net/http"
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

func searchLibaryGenesis(source, query string) (map[string]LibGenResult, error) {
	//URL encode the query
	urlArray := []string{"https://", source, "/search.php?req=", query}
	urlString := strings.Join(urlArray, "")

	fmt.Println(urlString)

	resp, err := http.Get(urlString)

	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)

	if err != nil {
		return nil, err
	}

	var results []LibGenResult
	mapped_results := make(map[string]LibGenResult)
	doc.Find("table.c tr").Each(func(i int, s *goquery.Selection) {
		if i > 0 {

			id := s.Find("td:nth-child(1)").Text()

			title := s.Find("td:nth-child(3) a").Text()

			author := s.Find("td:nth-child(2)").Text()

			publisher := s.Find("td-nth-child(4)").Text()

			//publishing_number := s.Find("td-nth-child(1)").Text() // not sure what this should be called yet

			edition := s.Find("td:nth-child(3) a").Text()

			size := s.Find("td:nth-child(8)").Text()

			extension := s.Find("td:nth-child(9)").Text()
			//language := s.Find("td:nth-child(7)").Text()

			//year := s.Find("td:nth-child(5)").Text()

			isbn := s.Find("td:nth-child(5)").Text()

			fullURL := s.Find("#"+id).AttrOr("href", "")

			md5Hash := strings.Split(fullURL, "?")[1]

			title = strings.Replace(title, isbn, "", 1)

			results = append(results, LibGenResult{ID: id, Number: i, Author: author, ISBN: isbn, URL: fullURL, Edition: edition, Title: title, Size: size, Extension: extension, Publisher: publisher, Hash: md5Hash})

			for _, result := range results {
				mapped_results[result.Title] = result
			}

		}

	})

	return mapped_results, nil

}
