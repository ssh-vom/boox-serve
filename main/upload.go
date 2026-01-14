package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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

func DownloadAndUploadLibGen(ctx context.Context, booxClient *BooxClient, httpClient *http.Client, items []TitleAndHash) error {
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

func DownloadAndUploadMangaChapters(ctx context.Context, booxClient *BooxClient, httpClient *http.Client, mangaTitle string, chapters []Chapter, updates chan<- ProgressUpdate) error {
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
		label := formatChapterLabel(chapter)
		prefix := fmt.Sprintf("Chapter %d/%d: ", index+1, len(chapters))

		tracker.message(prefix + "Downloading pages for " + label)
		chapterDetails, err := FetchChapterDetails(ctx, httpClient, chapter.ID)
		if err != nil {
			if shouldSkipChapter(err) {
				chapterErrors = append(chapterErrors, err)
				tracker.skip(stepsPerChapter, prefix+"Skipped "+label)
				continue
			}
			return fmt.Errorf("error fetching chapter details: %w", err)
		}

		images, err := DownloadChapterImages(ctx, httpClient, chapterDetails)
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
		cbzData, err := CreateCBZ(chapterName, images)
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
	return errors.Is(err, errChapterMetadataMissing) || errors.Is(err, errChapterNoPages)
}
