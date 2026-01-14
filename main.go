package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/AlecAivazis/survey/v2"
	"github.com/spf13/cobra"
)

func performQuery(source string, query string) map[string]LibGenResult {
	fmt.Println("searching for:", query)

	query = strings.ReplaceAll(query, " ", "+")

	results, err := searchLibaryGenesis(source, query)

	if err != nil {
		log.Fatal(err)
	}

	return results
}

func getTitleFromPageResult(results map[string]LibGenResult) []string {
	var titles []string
	for title := range results {
		titles = append(titles, title)
	}
	return titles
}

func getIDFromPageResult(results map[string]LibGenResult) []string {
	var IDs []string
	for _, res := range results {
		IDs = append(IDs, res.ID)
	}
	return IDs
}
func main() {

	var booxSearchCmd = &cobra.Command{
		Use:   "booxdownloader",
		Short: "A powerful tool to get titles from libgen and other sources",
		Run:   booxSearch,
	}
	var rootCmd = &cobra.Command{Use: "root"}
	rootCmd.AddCommand(booxSearchCmd)
	rootCmd.Execute()
}

func booxSearch(cmd *cobra.Command, args []string) {
	options := []string{"Textbooks", "Manga"}

	var searchType string
	var source string

	queryBoox()
	searchTypePrompt := &survey.Select{
		Message: "Select a category to search from",
		Options: options,
	}

	survey.AskOne(searchTypePrompt, &searchType)
	fmt.Println(searchType)

	sourcePrompt := &survey.Select{
		Message: "Select a source to search from",
		Options: getSourcesFromSearchType(searchType),
	}
	survey.AskOne(sourcePrompt, &source)
	getSourcesFromSearchType(source)

	if searchType == "Textbooks" {
		var query string
		prompt := &survey.Input{
			Message: "Enter your search query",
		}
		survey.AskOne(prompt, &query)

		fmt.Println(source)

		queryResults := performQuery(source, query)

		titleSelect := &survey.MultiSelect{
			Message: "Select the title of the book",
			Options: getTitleFromPageResult(queryResults),
		}

		var titleSelection []string

		survey.AskOne(titleSelect, &titleSelection)

		var hash_list_to_download []title_and_hash

		for _, title := range titleSelection {
			val, ok := queryResults[title]
			if ok {
				hash_list_to_download = append(hash_list_to_download, title_and_hash{val.Title, val.Hash})
			}

		}

		LibGenDownload(hash_list_to_download)

		return
	}
}

func getSourcesFromSearchType(searchType string) []string {
	switch searchType {
	case "Textbooks":
		return []string{"libgen.is", "libgen.rs", "libgen.st"}
	case "Manga":
		return []string{"MangaDex", "MangaFox"}
	}
	return []string{""}
}
