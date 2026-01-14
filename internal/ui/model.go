package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ssh-vom/boox-serve/internal/app"
	"github.com/ssh-vom/boox-serve/internal/boox"
	"github.com/ssh-vom/boox-serve/internal/config"
	"github.com/ssh-vom/boox-serve/internal/cover"
	"github.com/ssh-vom/boox-serve/internal/providers/manga"
)

type appState int

const (
	stateChecking appState = iota
	stateCheckFailed
	stateMenu
	stateMangaQuery
	stateMangaSearching
	stateMangaResults
	stateMangaLoadingChapters
	stateMangaChapters
	stateDownloading
	stateDownloadDone
	stateSettings
	stateAbout
	stateTextbooks
	stateLibrary
)

type menuItem struct {
	title       string
	description string
	action      appState
}

func (item menuItem) Title() string       { return item.title }
func (item menuItem) Description() string { return item.description }
func (item menuItem) FilterValue() string { return item.title }

type mangaResultItem struct {
	result manga.SearchResult
}

func (item mangaResultItem) Title() string { return item.result.Title }
func (item mangaResultItem) Description() string {
	if item.result.CoverURL != "" {
		return "Cover available"
	}
	return item.result.ID
}
func (item mangaResultItem) FilterValue() string { return item.result.Title }

type chapterItem struct {
	chapter manga.Chapter
}

func (item chapterItem) Title() string       { return manga.FormatChapterLabel(item.chapter) }
func (item chapterItem) Description() string { return "" }
func (item chapterItem) FilterValue() string { return manga.FormatChapterLabel(item.chapter) }

type connectionResultMsg struct {
	device *boox.DeviceDetails
	err    error
}

type mangaSearchMsg struct {
	results []manga.SearchResult
	err     error
}

type chaptersMsg struct {
	chapters []manga.Chapter
	err      error
}

type downloadStartMsg struct {
	updates <-chan app.ProgressUpdate
}

type coverLoadedMsg struct {
	url   string
	image cover.Image
	err   error
}

type coverTransitionMsg struct{}

type logMsg string

type model struct {
	state appState

	config        config.Config
	booxClient    *boox.Client
	mangaProvider manga.Provider
	buildDeps     BuildDependencies

	menu         list.Model
	textInput    textinput.Model
	resultsList  list.Model
	chapterList  list.Model
	chapterMarks map[int]bool

	selectedManga manga.SearchResult
	chapters      []manga.Chapter

	coverCache           map[string]cover.Image
	coverErrors          map[string]string
	coverLoadingURL      string
	coverSelectedURL     string
	coverTransitionURL   string
	coverTransitionStep  int
	coverTransitionTotal int
	supportsGraphics     bool

	progress        progress.Model
	progressCurrent int
	progressTotal   int
	progressMessage string
	downloadErr     error
	downloadUpdates <-chan app.ProgressUpdate

	spinner spinner.Model

	settings     settingsModel
	returnState  appState
	errorMessage string
	infoMessage  string

	width  int
	height int

	logChannel chan logMsg
	logLines   []string
	verbose    bool
}

type settingsModel struct {
	inputs    []textinput.Model
	focus     int
	errorText string
	infoText  string
}

type Dependencies struct {
	BooxClient    *boox.Client
	MangaProvider manga.Provider
}

type BuildDependencies func(cfg config.Config) (Dependencies, error)

func NewModel(cfg config.Config, deps Dependencies, buildDeps BuildDependencies, startupErr error) model {
	menu := newMenuList(0, 0)
	textInput := newQueryInput()
	resultsList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	chapterList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)

	spinnerModel := spinner.New()
	spinnerModel.Spinner = spinner.Dot

	progressModel := progress.New(progress.WithDefaultGradient())

	model := model{
		state:                stateChecking,
		config:               cfg,
		booxClient:           deps.BooxClient,
		mangaProvider:        deps.MangaProvider,
		buildDeps:            buildDeps,
		menu:                 menu,
		textInput:            textInput,
		resultsList:          resultsList,
		chapterList:          chapterList,
		chapterMarks:         map[int]bool{},
		coverCache:           map[string]cover.Image{},
		coverErrors:          map[string]string{},
		coverLoadingURL:      "",
		coverSelectedURL:     "",
		coverTransitionURL:   "",
		coverTransitionStep:  0,
		coverTransitionTotal: 0,
		supportsGraphics:     supportsKittyGraphics(),
		spinner:              spinnerModel,
		progress:             progressModel,
		verbose:              cfg.Verbose,
	}

	if startupErr != nil {
		model.state = stateCheckFailed
		model.errorMessage = startupErr.Error()
	}

	if model.verbose {
		model.logChannel = make(chan logMsg, 200)
		log.SetFlags(log.LstdFlags)
		log.SetOutput(logWriter{channel: model.logChannel})
	}

	return model
}

func (model model) Init() tea.Cmd {
	commands := []tea.Cmd{}
	if model.state == stateChecking {
		commands = append(commands, checkConnectionCmd(model.booxClient))
	}
	if model.verbose {
		commands = append(commands, listenLogCmd(model.logChannel))
	}
	return tea.Batch(commands...)
}

func (model model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		model.width = msg.Width
		model.height = msg.Height
		model.menu.SetSize(msg.Width-4, listHeight(msg.Height))
		model.chapterList.SetSize(msg.Width-4, listHeight(msg.Height))
		if model.state == stateMangaResults {
			model.resultsList.SetSize(resultsListWidth(msg.Width), listHeight(msg.Height))
		} else {
			model.resultsList.SetSize(msg.Width-4, listHeight(msg.Height))
		}
		progressWidth := msg.Width - 10
		if progressWidth < 10 {
			progressWidth = 10
		}
		model.progress.Width = progressWidth
		if model.state == stateMangaResults {
			model.coverCache = map[string]cover.Image{}
			model.coverErrors = map[string]string{}
			model.coverLoadingURL = ""
			model.coverSelectedURL = ""
			model.coverTransitionURL = ""
			model.coverTransitionStep = 0
			model.coverTransitionTotal = 0
			return model, model.requestCoverCmd()
		}
		return model, nil
	case connectionResultMsg:
		if msg.err != nil {
			model.state = stateCheckFailed
			model.errorMessage = msg.err.Error()
			return model, nil
		}
		model.state = stateMenu
		model.infoMessage = fmt.Sprintf("Connected to %s", msg.device.Model)
		return model, nil
	case mangaSearchMsg:
		if msg.err != nil {
			model.state = stateMangaQuery
			model.errorMessage = msg.err.Error()
			return model, nil
		}
		model.resultsList = newMangaResultsList(msg.results, resultsListWidth(model.width), listHeight(model.height))
		model.coverCache = map[string]cover.Image{}
		model.coverErrors = map[string]string{}
		model.coverLoadingURL = ""
		model.coverSelectedURL = ""
		model.coverTransitionURL = ""
		model.coverTransitionStep = 0
		model.coverTransitionTotal = 0
		model.state = stateMangaResults
		return model, model.requestCoverCmd()
	case chaptersMsg:
		if msg.err != nil {
			model.state = stateMangaResults
			model.errorMessage = msg.err.Error()
			return model, nil
		}
		model.chapters = msg.chapters
		model.chapterList, model.chapterMarks = newChapterList(msg.chapters, model.width, model.height)
		model.state = stateMangaChapters
		return model, nil
	case downloadStartMsg:
		model.state = stateDownloading
		model.downloadErr = nil
		model.downloadUpdates = msg.updates
		model.progressCurrent = 0
		model.progressTotal = 0
		model.progressMessage = "Starting download"
		return model, listenProgressCmd(model.downloadUpdates)
	case app.ProgressUpdate:
		if msg.Err != nil {
			model.downloadErr = msg.Err
		}
		if msg.Done {
			model.state = stateDownloadDone
			return model, nil
		}
		model.progressCurrent = msg.Current
		model.progressTotal = msg.Total
		model.progressMessage = msg.Message
		var progressCmd tea.Cmd
		if msg.Total > 0 {
			progressCmd = model.progress.SetPercent(float64(msg.Current) / float64(msg.Total))
		}
		return model, tea.Batch(progressCmd, listenProgressCmd(model.downloadUpdates))
	case progress.FrameMsg:
		updatedModel, cmd := model.progress.Update(msg)
		if progressModel, ok := updatedModel.(progress.Model); ok {
			model.progress = progressModel
		}
		return model, cmd
	case coverLoadedMsg:
		if msg.err != nil {
			model.coverErrors[msg.url] = msg.err.Error()
		} else if msg.image.FilePath != "" {
			model.coverCache[msg.url] = msg.image
		}
		if model.coverLoadingURL == msg.url {
			model.coverLoadingURL = ""
		}
		if msg.url != "" && msg.url == model.selectedCoverURL() {
			return model, model.startCoverTransition(msg.url)
		}
		return model, nil
	case coverTransitionMsg:
		if model.coverTransitionURL == "" {
			return model, nil
		}
		if model.coverTransitionURL != model.selectedCoverURL() {
			model.coverTransitionURL = ""
			return model, nil
		}
		model.coverTransitionStep++
		if model.coverTransitionStep >= model.coverTransitionTotal {
			model.coverTransitionURL = ""
			return model, nil
		}
		return model, coverTransitionCmd()
	case logMsg:
		if model.verbose {
			model.logLines = append(model.logLines, string(msg))
			if len(model.logLines) > 6 {
				model.logLines = model.logLines[len(model.logLines)-6:]
			}
			return model, listenLogCmd(model.logChannel)
		}
		return model, nil
	}

	return model.handleStateUpdate(msg)
}

func (model *model) handleStateUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch model.state {
	case stateChecking:
		spinnerCmd := model.spinner.Tick
		model.spinner, spinnerCmd = model.spinner.Update(msg)
		return *model, spinnerCmd
	case stateCheckFailed:
		return *model, model.updateCheckFailed(msg)
	case stateMenu:
		return *model, model.updateMenu(msg)
	case stateMangaQuery:
		return *model, model.updateMangaQuery(msg)
	case stateMangaSearching:
		spinnerCmd := model.spinner.Tick
		model.spinner, spinnerCmd = model.spinner.Update(msg)
		return *model, spinnerCmd
	case stateMangaResults:
		return *model, model.updateMangaResults(msg)
	case stateMangaLoadingChapters:
		spinnerCmd := model.spinner.Tick
		model.spinner, spinnerCmd = model.spinner.Update(msg)
		return *model, spinnerCmd
	case stateMangaChapters:
		return *model, model.updateMangaChapters(msg)
	case stateDownloading:
		spinnerCmd := model.spinner.Tick
		model.spinner, spinnerCmd = model.spinner.Update(msg)
		return *model, spinnerCmd
	case stateDownloadDone:
		return *model, model.updateDownloadDone(msg)
	case stateSettings:
		return *model, model.updateSettings(msg)
	case stateAbout, stateTextbooks, stateLibrary:
		return *model, model.updateInfoScreens(msg)
	default:
		return *model, nil
	}
}

func (model model) View() string {
	view := ""

	switch model.state {
	case stateChecking:
		view = fmt.Sprintf("%s Checking Boox connection...", model.spinner.View())
	case stateCheckFailed:
		view = fmt.Sprintf("Boox not reachable.\nError: %s\n\nPress r to retry, s to edit settings, q to quit.", model.errorMessage)
	case stateMenu:
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("Boox Uploader"),
			model.menu.View(),
			secondaryStyle.Render("Enter to select · s settings · q quit"),
		)
	case stateMangaQuery:
		lines := []string{
			titleStyle.Render("Search Manga"),
			model.textInput.View(),
		}
		if model.errorMessage != "" {
			lines = append(lines, warningStyle.Render(model.errorMessage))
		}
		lines = append(lines, secondaryStyle.Render("Enter to search · esc to cancel"))
		view = lipgloss.JoinVertical(lipgloss.Left, lines...)
	case stateMangaSearching:
		view = fmt.Sprintf("%s Searching MangaDex...", model.spinner.View())
	case stateMangaResults:
		view = model.mangaResultsView()
	case stateMangaLoadingChapters:
		view = fmt.Sprintf("%s Fetching chapters...", model.spinner.View())
	case stateMangaChapters:
		lines := []string{
			titleStyle.Render("Select Chapters"),
			model.chapterList.View(),
		}
		if model.errorMessage != "" {
			lines = append(lines, warningStyle.Render(model.errorMessage))
		}
		lines = append(lines, secondaryStyle.Render("Space to toggle · Enter to download · esc to back"))
		view = lipgloss.JoinVertical(lipgloss.Left, lines...)
	case stateDownloading:
		progressLine := model.progress.View()
		if model.progressTotal > 0 {
			progressLine = fmt.Sprintf("%s %d/%d", progressLine, model.progressCurrent, model.progressTotal)
		}
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("Downloading"),
			model.spinner.View()+" "+model.progressMessage,
			progressLine,
		)
	case stateDownloadDone:
		message := "Download complete."
		if model.downloadErr != nil {
			message = "Download completed with errors:\n" + model.downloadErr.Error()
		}
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("Done"),
			message,
			secondaryStyle.Render("Press enter to return"),
		)
	case stateSettings:
		view = model.settingsView()
	case stateAbout:
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("About"),
			"Boox Uploader TUI for manga and textbooks.",
			secondaryStyle.Render("Press esc to go back"),
		)
	case stateTextbooks:
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("Textbook Search"),
			"Textbook sources (LibGen, Anna's Archive) are coming soon.",
			secondaryStyle.Render("Press esc to go back"),
		)
	case stateLibrary:
		view = lipgloss.JoinVertical(lipgloss.Left,
			titleStyle.Render("Boox Library"),
			"Library browsing will be available in a follow-up.",
			secondaryStyle.Render("Press esc to go back"),
		)
	}

	if model.verbose {
		view = lipgloss.JoinVertical(lipgloss.Left, view, model.logView())
	}

	return view
}

func (model *model) updateCheckFailed(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	switch key.String() {
	case "r":
		model.state = stateChecking
		return checkConnectionCmd(model.booxClient)
	case "s":
		model.settings = newSettingsModel(model.config)
		model.returnState = stateCheckFailed
		model.state = stateSettings
		return nil
	case "q", "ctrl+c":
		return tea.Quit
	}

	return nil
}

func (model *model) updateMenu(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	model.menu, cmd = model.menu.Update(msg)
	model.errorMessage = ""

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return cmd
	}

	switch key.String() {
	case "enter":
		if selected, ok := model.menu.SelectedItem().(menuItem); ok {
			if selected.action == stateSettings {
				model.settings = newSettingsModel(model.config)
				model.returnState = stateMenu
				model.state = stateSettings
				return nil
			}
			model.state = selected.action
			if selected.action == stateMangaQuery {
				model.textInput = newQueryInput()
				model.textInput.Focus()
			}
			return nil
		}
	case "s":
		model.settings = newSettingsModel(model.config)
		model.returnState = stateMenu
		model.state = stateSettings
		return nil
	case "q", "ctrl+c":
		return tea.Quit
	}

	return cmd
}

func (model *model) updateMangaQuery(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok && key.String() == "esc" {
		model.state = stateMenu
		return nil
	}
	if ok && key.String() != "enter" {
		model.errorMessage = ""
	}

	var cmd tea.Cmd
	model.textInput, cmd = model.textInput.Update(msg)
	if ok && key.String() == "enter" {
		query := strings.TrimSpace(model.textInput.Value())
		if query == "" {
			model.errorMessage = "Search query cannot be empty"
			return nil
		}
		model.state = stateMangaSearching
		model.errorMessage = ""
		return searchMangaCmd(model.mangaProvider, query)
	}

	return cmd
}

func (model *model) updateMangaResults(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	model.resultsList, cmd = model.resultsList.Update(msg)
	model.errorMessage = ""

	transitionCmd := tea.Cmd(nil)
	selectedURL := model.selectedCoverURL()
	if selectedURL != model.coverSelectedURL {
		model.coverSelectedURL = selectedURL
		if selectedURL != "" {
			if _, ok := model.coverCache[selectedURL]; ok {
				transitionCmd = model.startCoverTransition(selectedURL)
			}
		}
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return tea.Batch(cmd, model.requestCoverCmd(), transitionCmd)
	}

	switch key.String() {
	case "esc":
		model.state = stateMenu
		return nil
	case "enter":
		if selected, ok := model.resultsList.SelectedItem().(mangaResultItem); ok {
			model.selectedManga = selected.result
			model.state = stateMangaLoadingChapters
			return fetchChaptersCmd(model.mangaProvider, selected.result.ID)
		}
	}

	return tea.Batch(cmd, model.requestCoverCmd(), transitionCmd)
}

func (model *model) updateMangaChapters(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		switch key.String() {
		case "esc":
			model.state = stateMangaResults
			return nil
		case " ":
			model.errorMessage = ""
			index := model.chapterList.Index()
			model.chapterMarks[index] = !model.chapterMarks[index]
			return nil
		case "enter":
			selected := selectedChapters(model.chapterList.Items(), model.chapterMarks)
			if len(selected) == 0 {
				model.errorMessage = "Select at least one chapter"
				return nil
			}
			model.errorMessage = ""
			model.state = stateDownloading
			return startDownloadCmd(model.booxClient, model.mangaProvider, model.selectedManga.Title, selected)
		}
	}

	var cmd tea.Cmd
	model.chapterList, cmd = model.chapterList.Update(msg)
	return cmd
}

func (model *model) updateDownloadDone(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok && (key.String() == "enter" || key.String() == "esc") {
		model.state = stateMenu
		return nil
	}
	return nil
}

func (model *model) updateSettings(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok {
		if key.String() != "c" {
			model.settings.infoText = ""
		}
		switch key.String() {
		case "esc":
			model.state = model.returnState
			return nil
		case "tab", "shift+tab":
			model.settings.focus = updateSettingsFocus(key.String(), model.settings.focus, len(model.settings.inputs))
			model.settings = applySettingsFocus(model.settings)
			return nil
		case "enter":
			return model.saveSettings()
		case "c":
			if err := cover.ClearCache(); err != nil {
				model.settings.errorText = err.Error()
				return nil
			}
			model.coverCache = map[string]cover.Image{}
			model.coverErrors = map[string]string{}
			model.coverLoadingURL = ""
			model.coverSelectedURL = ""
			model.coverTransitionURL = ""
			model.coverTransitionStep = 0
			model.coverTransitionTotal = 0
			model.settings.errorText = ""
			model.settings.infoText = "Cover cache cleared."
			return nil
		}
	}

	var cmd tea.Cmd
	current := &model.settings.inputs[model.settings.focus]
	*current, cmd = current.Update(msg)
	return cmd
}

func (model *model) saveSettings() tea.Cmd {
	updated, err := buildConfigFromSettings(model.config, model.settings.inputs)
	if err != nil {
		model.settings.errorText = err.Error()
		return nil
	}

	if err := config.SaveConfig(updated); err != nil {
		model.settings.errorText = err.Error()
		return nil
	}

	model.config = updated
	if model.buildDeps != nil {
		deps, err := model.buildDeps(updated)
		if err != nil {
			model.settings.errorText = err.Error()
			return nil
		}
		model.booxClient = deps.BooxClient
		model.mangaProvider = deps.MangaProvider
	}

	model.settings.errorText = ""
	if model.returnState == stateCheckFailed {
		model.state = stateChecking
		return checkConnectionCmd(model.booxClient)
	}
	model.state = stateMenu
	return nil
}

func (model *model) updateInfoScreens(msg tea.Msg) tea.Cmd {
	key, ok := msg.(tea.KeyMsg)
	if ok && (key.String() == "esc" || key.String() == "q") {
		model.state = stateMenu
		return nil
	}
	return nil
}

func (model model) settingsView() string {
	lines := []string{
		titleStyle.Render("Settings"),
		"Edit Boox connection settings.",
	}

	for _, input := range model.settings.inputs {
		lines = append(lines, input.View())
	}
	if model.settings.errorText != "" {
		lines = append(lines, warningStyle.Render(model.settings.errorText))
	}
	if model.settings.infoText != "" {
		lines = append(lines, secondaryStyle.Render(model.settings.infoText))
	}
	lines = append(lines, secondaryStyle.Render("Press c to clear cover cache"))
	lines = append(lines, secondaryStyle.Render("Enter to save · Esc to cancel"))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (model model) mangaResultsView() string {
	listSection := []string{
		titleStyle.Render("Select Manga"),
		model.resultsList.View(),
	}
	if model.errorMessage != "" {
		listSection = append(listSection, warningStyle.Render(model.errorMessage))
	}
	listSection = append(listSection, secondaryStyle.Render("Enter to select · esc to cancel"))

	selected := manga.SearchResult{}
	if item, ok := model.resultsList.SelectedItem().(mangaResultItem); ok {
		selected = item.result
	}

	panelWidth := coverPanelWidth(model.width)
	panel := model.mangaCoverPanel(selected, panelWidth)
	listView := lipgloss.NewStyle().Width(resultsListWidth(model.width)).Render(lipgloss.JoinVertical(lipgloss.Left, listSection...))

	if model.width < 80 {
		return lipgloss.JoinVertical(lipgloss.Left, listView, panel)
	}

	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		listView,
		panel,
	)
}

func (model model) logView() string {
	if len(model.logLines) == 0 {
		return secondaryStyle.Render("Logs: (no entries)")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		secondaryStyle.Render("Logs:"),
		strings.Join(model.logLines, "\n"),
	)
}

func listHeight(height int) int {
	if height <= 10 {
		return height
	}

	return height - 8
}

const coverCellAspectRatio = 0.5

func coverRenderSize(panelWidth int, imageWidth, imageHeight int) (int, int) {
	cols := panelWidth - 2
	if cols < 12 {
		cols = 12
	}

	rows := 12
	if imageWidth > 0 && imageHeight > 0 {
		ratio := float64(imageHeight) / float64(imageWidth)
		rows = int(math.Round(float64(cols) * ratio * coverCellAspectRatio))
	}

	if rows < 6 {
		rows = 6
	}
	if rows > 24 {
		rows = 24
	}

	return cols, rows
}

func coverPlaceholder(rows, cols int) string {
	if rows <= 0 || cols <= 0 {
		return ""
	}

	line := strings.Repeat(" ", cols)
	lines := make([]string, rows)
	for i := 0; i < rows; i++ {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func coverPanelWidth(totalWidth int) int {
	if totalWidth <= 40 {
		return totalWidth
	}

	panelWidth := totalWidth / 3
	if panelWidth < 28 {
		panelWidth = 28
	}
	if panelWidth > totalWidth-20 {
		panelWidth = totalWidth - 20
	}
	return panelWidth
}

func resultsListWidth(totalWidth int) int {
	if totalWidth < 80 {
		if totalWidth-4 < 20 {
			return 20
		}
		return totalWidth - 4
	}
	listWidth := totalWidth - coverPanelWidth(totalWidth) - 2
	if listWidth < 20 {
		listWidth = 20
	}
	return listWidth
}

func supportsKittyGraphics() bool {
	term := strings.ToLower(os.Getenv("TERM"))
	return strings.Contains(term, "ghostty") || strings.Contains(term, "kitty")
}

func (model model) mangaCoverPanel(result manga.SearchResult, width int) string {
	if width < 20 {
		width = 20
	}

	lines := []string{}

	if !model.supportsGraphics {
		lines = append(lines, secondaryStyle.Render("Terminal image rendering unavailable."))
		content := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return panelStyle.Width(width).Render(content)
	}

	if result.Title == "" {
		lines = append(lines, secondaryStyle.Render("Select a manga to preview."))
		content := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return panelStyle.Width(width).Render(content)
	}

	lines = append(lines, panelTitleStyle.Render(result.Title))
	lines = append(lines, "")
	if result.CoverURL == "" {
		lines = append(lines, secondaryStyle.Render("No cover art available."))
		content := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return panelStyle.Width(width).Render(content)
	}

	image, ok := model.coverCache[result.CoverURL]
	if !ok {
		if model.coverLoadingURL == result.CoverURL {
			cols, rows := coverRenderSize(width, 0, 0)
			lines = append(lines, secondaryStyle.Render("Loading cover..."))
			lines = append(lines, coverPlaceholder(rows, cols))
			content := lipgloss.JoinVertical(lipgloss.Left, lines...)
			return panelStyle.Width(width).Render(content)
		}
		if errText, ok := model.coverErrors[result.CoverURL]; ok {
			lines = append(lines, warningStyle.Render(errText))
		} else {
			lines = append(lines, secondaryStyle.Render("Cover available."))
		}
		content := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return panelStyle.Width(width).Render(content)
	}

	cols, rows := coverRenderSize(width, image.Width, image.Height)
	placeholder := coverPlaceholder(rows, cols)
	framePath := image.FilePath
	if model.coverTransitionURL == result.CoverURL && len(image.Frames) > 0 && model.coverTransitionStep < len(image.Frames) {
		framePath = image.Frames[model.coverTransitionStep]
	}

	render, err := cover.RenderKittyImageFromFile(framePath, cols, rows, 0, 0)

	if err != nil {
		lines = append(lines, warningStyle.Render(err.Error()))
		content := lipgloss.JoinVertical(lipgloss.Left, lines...)
		return panelStyle.Width(width).Render(content)
	}

	lines = append(lines, render+"\n"+placeholder)

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return panelStyle.Width(width).Render(content)
}

func newMenuList(width, height int) list.Model {
	items := []list.Item{
		menuItem{title: "Search Manga", description: "Find manga and upload chapters", action: stateMangaQuery},
		menuItem{title: "Search Textbooks", description: "LibGen and Anna's Archive", action: stateTextbooks},
		menuItem{title: "Boox Library", description: "Browse titles on device", action: stateLibrary},
		menuItem{title: "Settings", description: "Edit Boox connection", action: stateSettings},
		menuItem{title: "About/Help", description: "Usage and shortcuts", action: stateAbout},
	}

	menu := list.New(items, list.NewDefaultDelegate(), width, height)
	menu.Title = "Home"
	menu.SetShowStatusBar(false)
	menu.SetFilteringEnabled(false)
	menu.SetShowHelp(false)

	return menu
}

func newQueryInput() textinput.Model {
	input := textinput.New()
	input.Placeholder = "e.g. One Piece"
	input.Focus()
	input.Prompt = "> "
	return input
}

func newMangaResultsList(results []manga.SearchResult, width, height int) list.Model {
	items := make([]list.Item, 0, len(results))
	for _, result := range results {
		items = append(items, mangaResultItem{result: result})
	}

	resultList := list.New(items, list.NewDefaultDelegate(), width, height)
	resultList.Title = "Results"
	resultList.SetShowStatusBar(false)
	resultList.SetFilteringEnabled(true)
	resultList.SetShowHelp(false)

	return resultList
}

func newChapterList(chapters []manga.Chapter, width, height int) (list.Model, map[int]bool) {
	items := make([]list.Item, 0, len(chapters))
	for _, chapter := range chapters {
		items = append(items, chapterItem{chapter: chapter})
	}

	selected := make(map[int]bool)
	delegate := multiSelectDelegate{selected: selected}
	chapterList := list.New(items, delegate, width, height)
	chapterList.Title = "Chapters"
	chapterList.SetShowStatusBar(false)
	chapterList.SetFilteringEnabled(true)
	chapterList.SetShowHelp(false)

	return chapterList, selected
}

func selectedChapters(items []list.Item, selected map[int]bool) []manga.Chapter {
	chapters := []manga.Chapter{}
	for index, item := range items {
		if selected[index] {
			chapterItem, ok := item.(chapterItem)
			if !ok {
				continue
			}
			chapters = append(chapters, chapterItem.chapter)
		}
	}
	return chapters
}

func checkConnectionCmd(client *boox.Client) tea.Cmd {
	return func() tea.Msg {
		if client == nil {
			return connectionResultMsg{err: errors.New("boox device not configured")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		device, err := client.CheckConnection(ctx)
		return connectionResultMsg{device: device, err: err}
	}
}

func searchMangaCmd(provider manga.Provider, query string) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return mangaSearchMsg{err: errors.New("manga provider unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		results, err := provider.Search(ctx, query)
		return mangaSearchMsg{results: results, err: err}
	}
}

func fetchChaptersCmd(provider manga.Provider, mangaID string) tea.Cmd {
	return func() tea.Msg {
		if provider == nil {
			return chaptersMsg{err: errors.New("manga provider unavailable")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		chapters, err := provider.FetchChapters(ctx, mangaID)
		return chaptersMsg{chapters: chapters, err: err}
	}
}

func fetchCoverCmd(provider manga.Provider, coverURL string) tea.Cmd {
	return func() tea.Msg {
		if coverURL == "" {
			return coverLoadedMsg{url: coverURL, err: errors.New("cover url missing")}
		}
		if provider == nil {
			return coverLoadedMsg{url: coverURL, err: errors.New("manga provider unavailable")}
		}

		cached, ok, err := cover.LoadCachedCover(coverURL)
		if err != nil {
			return coverLoadedMsg{url: coverURL, err: err}
		}
		if ok {
			return coverLoadedMsg{url: coverURL, image: cached}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		body, err := provider.FetchCover(ctx, coverURL)
		if err != nil {
			return coverLoadedMsg{url: coverURL, err: err}
		}

		image, err := cover.SaveCoverImage(coverURL, body)
		if err != nil {
			return coverLoadedMsg{url: coverURL, err: err}
		}

		return coverLoadedMsg{url: coverURL, image: image}
	}
}

func startDownloadCmd(booxClient *boox.Client, provider manga.Provider, mangaTitle string, chapters []manga.Chapter) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan app.ProgressUpdate, len(chapters)+2)
		go func() {
			if booxClient == nil {
				updates <- app.ProgressUpdate{Done: true, Err: errors.New("boox connection unavailable")}
				close(updates)
				return
			}
			if provider == nil {
				updates <- app.ProgressUpdate{Done: true, Err: errors.New("manga provider unavailable")}
				close(updates)
				return
			}
			ctx := context.Background()
			err := app.DownloadAndUploadMangaChapters(ctx, booxClient, provider, mangaTitle, chapters, updates)
			updates <- app.ProgressUpdate{Done: true, Err: err}
			close(updates)
		}()
		return downloadStartMsg{updates: updates}
	}
}

func listenProgressCmd(updates <-chan app.ProgressUpdate) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-updates
		if !ok {
			return app.ProgressUpdate{Done: true}
		}
		return msg
	}
}

func listenLogCmd(ch <-chan logMsg) tea.Cmd {
	return func() tea.Msg {
		return <-ch
	}
}

func (model *model) requestCoverCmd() tea.Cmd {
	if !model.supportsGraphics {
		return nil
	}
	if model.mangaProvider == nil {
		return nil
	}

	coverURL := model.selectedCoverURL()
	if coverURL == "" {
		return nil
	}

	if _, ok := model.coverCache[coverURL]; ok {
		return nil
	}

	if model.coverLoadingURL == coverURL {
		return nil
	}

	if _, ok := model.coverErrors[coverURL]; ok {
		return nil
	}

	model.coverLoadingURL = coverURL
	return fetchCoverCmd(model.mangaProvider, coverURL)
}

func (model *model) selectedCoverURL() string {
	if item, ok := model.resultsList.SelectedItem().(mangaResultItem); ok {
		return item.result.CoverURL
	}
	return ""
}

func coverTransitionCmd() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
		return coverTransitionMsg{}
	})
}

func (model *model) startCoverTransition(url string) tea.Cmd {
	if url == "" || !model.supportsGraphics {
		return nil
	}
	model.coverTransitionURL = url
	model.coverTransitionStep = 0
	transitionTotal := cover.FadeFrames
	if image, ok := model.coverCache[url]; ok && len(image.Frames) > 0 {
		transitionTotal = len(image.Frames)
	}
	model.coverTransitionTotal = transitionTotal
	return coverTransitionCmd()
}

func updateSettingsFocus(direction string, focus, total int) int {
	if direction == "tab" {
		focus++
	} else {
		focus--
	}
	if focus >= total {
		focus = 0
	} else if focus < 0 {
		focus = total - 1
	}
	return focus
}

func applySettingsFocus(settings settingsModel) settingsModel {
	for i := range settings.inputs {
		if i == settings.focus {
			settings.inputs[i].Focus()
			settings.inputs[i].PromptStyle = focusedStyle
			settings.inputs[i].TextStyle = focusedStyle
		} else {
			settings.inputs[i].Blur()
			settings.inputs[i].PromptStyle = blurStyle
			settings.inputs[i].TextStyle = blurStyle
		}
	}
	return settings
}

func newSettingsModel(cfg config.Config) settingsModel {
	inputs := make([]textinput.Model, 3)

	urlInput := textinput.New()
	urlInput.Prompt = "Boox URL: "
	urlInput.SetValue(cfg.BooxURL)
	urlInput.CharLimit = 200

	ipInput := textinput.New()
	ipInput.Prompt = "Boox IP: "
	ipInput.SetValue(cfg.BooxIP)
	ipInput.CharLimit = 60

	portInput := textinput.New()
	portInput.Prompt = "Boox Port: "
	if cfg.BooxPort > 0 {
		portInput.SetValue(strconv.Itoa(cfg.BooxPort))
	}
	portInput.CharLimit = 6

	inputs[0] = urlInput
	inputs[1] = ipInput
	inputs[2] = portInput

	settings := settingsModel{inputs: inputs, focus: 0}
	return applySettingsFocus(settings)
}

func buildConfigFromSettings(cfg config.Config, inputs []textinput.Model) (config.Config, error) {
	urlValue := strings.TrimSpace(inputs[0].Value())
	ipValue := strings.TrimSpace(inputs[1].Value())
	portValue := strings.TrimSpace(inputs[2].Value())

	if urlValue == "" && ipValue == "" {
		return cfg, errors.New("boox url or ip is required")
	}

	cfg.BooxURL = urlValue
	cfg.BooxIP = ipValue

	if portValue != "" {
		port, err := strconv.Atoi(portValue)
		if err != nil {
			return cfg, errors.New("boox port must be a number")
		}
		cfg.BooxPort = port
	}

	return cfg, nil
}

type multiSelectDelegate struct {
	selected map[int]bool
}

func (delegate multiSelectDelegate) Height() int                                   { return 1 }
func (delegate multiSelectDelegate) Spacing() int                                  { return 0 }
func (delegate multiSelectDelegate) Update(msg tea.Msg, model *list.Model) tea.Cmd { return nil }

func (delegate multiSelectDelegate) Render(writer io.Writer, model list.Model, index int, item list.Item) {
	checkbox := " "
	if delegate.selected[index] {
		checkbox = "x"
	}
	cursor := " "
	if index == model.Index() {
		cursor = ">"
	}
	title := item.FilterValue()
	if titled, ok := item.(interface{ Title() string }); ok {
		title = titled.Title()
	}
	fmt.Fprintf(writer, "%s [%s] %s", cursor, checkbox, title)
}

type logWriter struct {
	channel chan<- logMsg
}

func (writer logWriter) Write(data []byte) (int, error) {
	message := strings.TrimSpace(string(data))
	if message == "" {
		return len(data), nil
	}

	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		select {
		case writer.channel <- logMsg(line):
		default:
		}
	}

	return len(data), nil
}

var (
	titleStyle      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	secondaryStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	warningStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	focusedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	blurStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	panelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	panelStyle      = lipgloss.NewStyle().Padding(0, 1)
)
