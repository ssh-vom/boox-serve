package app

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ssh-vom/boox-serve/internal/boox"
	"github.com/ssh-vom/boox-serve/internal/providers/manga"
)

type TitleAndHash struct {
	Title string
	Hash  string
}

type ProgressUpdate struct {
	Current int
	Total   int
	Message string
	Done    bool
	Err     error
}

type progressTracker struct {
	updates chan<- ProgressUpdate
	total   int
	current int
}

func newProgressTracker(updates chan<- ProgressUpdate, total int) *progressTracker {
	return &progressTracker{updates: updates, total: total}
}

func (tracker *progressTracker) message(message string) {
	if tracker == nil || tracker.updates == nil {
		return
	}
	tracker.updates <- ProgressUpdate{Current: tracker.current, Total: tracker.total, Message: message}
}

func (tracker *progressTracker) advance(message string) {
	if tracker == nil {
		return
	}
	if tracker.current < tracker.total {
		tracker.current++
	}
	if tracker.updates != nil {
		tracker.updates <- ProgressUpdate{Current: tracker.current, Total: tracker.total, Message: message}
	}
}

func (tracker *progressTracker) skip(steps int, message string) {
	if tracker == nil {
		return
	}
	tracker.current += steps
	if tracker.current > tracker.total {
		tracker.current = tracker.total
	}
	if tracker.updates != nil {
		tracker.updates <- ProgressUpdate{Current: tracker.current, Total: tracker.total, Message: message}
	}
}

func DownloadAndUploadLibGen(ctx context.Context, booxClient *boox.Client, httpClient *http.Client, items []TitleAndHash) error {
	for _, item := range items {
		getURL := "https://cdn3.booksdl.org/get.php?" + item.Hash

		request, err := http.NewRequestWithContext(ctx, http.MethodGet, getURL, nil)
		if err != nil {
			return fmt.Errorf("error building download request: %w", err)
		}

		response, err := httpClient.Do(request)
		if err != nil {
			return fmt.Errorf("error downloading file: %w", err)
		}

		buffer := &bytes.Buffer{}
		if _, err := io.Copy(buffer, response.Body); err != nil {
			response.Body.Close()
			return fmt.Errorf("error reading file: %w", err)
		}
		response.Body.Close()

		fileName := fmt.Sprintf("%s.pdf", sanitizeFileName(item.Title))
		if err := booxClient.UploadFile(ctx, "", fileName, buffer.Bytes()); err != nil {
			return err
		}
	}

	return nil
}

func DownloadAndUploadMangaChapters(ctx context.Context, booxClient *boox.Client, provider manga.Provider, mangaTitle string, chapters []manga.Chapter, updates chan<- ProgressUpdate) error {
	if len(chapters) == 0 {
		return fmt.Errorf("no chapters selected")
	}

	folderName := sanitizeFileName(mangaTitle)
	folderID, err := booxClient.CreateFolder(ctx, nil, folderName)
	if err != nil {
		if updates != nil {
			updates <- ProgressUpdate{Message: "Unable to create folder, uploading to root"}
		}
		folderID = ""
	}

	const stepsPerChapter = 3
	tracker := newProgressTracker(updates, len(chapters)*stepsPerChapter)

	var chapterErrors []error

	for index, chapter := range chapters {
		label := manga.FormatChapterLabel(chapter)
		prefix := fmt.Sprintf("Chapter %d/%d: ", index+1, len(chapters))

		tracker.message(prefix + "Downloading pages for " + label)
		images, err := provider.DownloadChapterImages(ctx, chapter)
		if err != nil {
			if shouldSkipChapter(err) {
				chapterErrors = append(chapterErrors, err)
				tracker.skip(stepsPerChapter, prefix+"Skipped "+label)
				continue
			}
			return fmt.Errorf("error downloading chapter images: %w", err)
		}
		tracker.advance(prefix + "Downloaded pages for " + label)

		tracker.message(prefix + "Creating CBZ for " + label)
		chapterName := sanitizeFileName(label)
		cbzData, err := createCBZ(chapterName, images)
		if err != nil {
			return fmt.Errorf("error creating CBZ file: %w", err)
		}
		tracker.advance(prefix + "Created CBZ for " + label)

		tracker.message(prefix + "Uploading " + label)
		fileName := fmt.Sprintf("%s.cbz", chapterName)
		if err := booxClient.UploadFile(ctx, folderID, fileName, cbzData); err != nil {
			return fmt.Errorf("error uploading CBZ file: %w", err)
		}
		tracker.advance(prefix + "Uploaded " + label)
	}

	if len(chapterErrors) > 0 {
		return fmt.Errorf("skipped %d chapter(s): %w", len(chapterErrors), errors.Join(chapterErrors...))
	}

	return nil
}

func shouldSkipChapter(err error) bool {
	return errors.Is(err, manga.ErrChapterMetadataMissing) || errors.Is(err, manga.ErrChapterNoPages)
}

func createCBZ(chapterName string, images [][]byte) ([]byte, error) {
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

func sanitizeFileName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "untitled"
	}

	trimmed = strings.ReplaceAll(trimmed, "/", "-")
	trimmed = strings.ReplaceAll(trimmed, "\\", "-")
	trimmed = strings.Trim(trimmed, ". ")

	return filepath.Clean(trimmed)
}
