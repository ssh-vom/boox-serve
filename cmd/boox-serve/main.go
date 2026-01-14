package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ssh-vom/boox-serve/internal/boox"
	"github.com/ssh-vom/boox-serve/internal/config"
	"github.com/ssh-vom/boox-serve/internal/providers/manga/mangadex"
	"github.com/ssh-vom/boox-serve/internal/ui"
)

func main() {
	verboseFlag := flag.Bool("verbose", false, "show verbose logs")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if *verboseFlag {
		cfg.Verbose = true
	}

	httpClient := newHTTPClient()
	deps, startupErr := buildDependencies(cfg, httpClient)

	program := tea.NewProgram(ui.NewModel(cfg, deps, func(cfg config.Config) (ui.Dependencies, error) {
		return buildDependencies(cfg, httpClient)
	}, startupErr), tea.WithAltScreen())

	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}

func buildDependencies(cfg config.Config, httpClient *http.Client) (ui.Dependencies, error) {
	deps := ui.Dependencies{
		MangaProvider: mangadex.New(httpClient, cfg.Providers.MangaDexAPIKey),
	}

	baseURL, err := cfg.BaseURL()
	if err != nil {
		return deps, err
	}
	deps.BooxClient = boox.NewClient(baseURL, httpClient)
	return deps, nil
}

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
