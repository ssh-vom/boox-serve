package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const mangaDexUserAgent = "boox-uploader-cli/0.1"

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

func addMangaDexHeaders(request *http.Request) {
	request.Header.Set("User-Agent", mangaDexUserAgent)
}

const coverFadeFrames = 6

func coverCacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("unable to resolve cache dir: %w", err)
	}

	return filepath.Join(cacheDir, "boox-uploader", "covers"), nil
}

func coverCacheKey(coverURL string) (string, error) {
	parsed, err := url.Parse(coverURL)
	if err != nil {
		return "", fmt.Errorf("invalid cover url: %w", err)
	}

	key := strings.TrimPrefix(parsed.Path, "/")
	if key == "" {
		return "", fmt.Errorf("cover url missing path")
	}

	key = strings.ReplaceAll(key, "/", "_")
	key = strings.ReplaceAll(key, ".", "_")
	return key, nil
}

func coverCachePaths(coverURL string) (string, []string, error) {
	cacheDir, err := coverCacheDir()
	if err != nil {
		return "", nil, err
	}

	key, err := coverCacheKey(coverURL)
	if err != nil {
		return "", nil, err
	}

	basePath := filepath.Join(cacheDir, key+".png")
	frames := make([]string, coverFadeFrames)
	for i := 0; i < coverFadeFrames; i++ {
		frames[i] = filepath.Join(cacheDir, fmt.Sprintf("%s-fade-%02d.png", key, i+1))
	}

	return basePath, frames, nil
}

func loadCachedCover(coverURL string) (coverImage, bool, error) {
	basePath, framePaths, err := coverCachePaths(coverURL)
	if err != nil {
		return coverImage{}, false, err
	}

	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			return coverImage{}, false, nil
		}
		return coverImage{}, false, err
	}

	file, err := os.Open(basePath)
	if err != nil {
		return coverImage{}, false, err
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		purgeCoverFiles(basePath, framePaths)
		return coverImage{}, false, nil
	}

	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if !framesExist(framePaths) {
		if err := generateCoverFrames(decoded, framePaths); err != nil {
			return coverImage{}, false, err
		}
	}

	return coverImage{filePath: basePath, frames: framePaths, width: width, height: height}, true, nil
}

func clearCoverCache() error {
	cacheDir, err := coverCacheDir()
	if err != nil {
		return err
	}

	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("unable to clear cover cache: %w", err)
	}

	return nil
}

func saveCoverImage(coverURL string, data []byte) (coverImage, error) {
	if len(data) == 0 {
		return coverImage{}, fmt.Errorf("empty image data")
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return coverImage{}, fmt.Errorf("unable to decode cover image: %w", err)
	}

	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	basePath, framePaths, err := coverCachePaths(coverURL)
	if err != nil {
		return coverImage{}, err
	}

	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return coverImage{}, fmt.Errorf("unable to create cover cache: %w", err)
	}

	if err := writeCoverPNG(basePath, decoded); err != nil {
		return coverImage{}, err
	}

	if err := generateCoverFrames(decoded, framePaths); err != nil {
		return coverImage{}, err
	}

	return coverImage{filePath: basePath, frames: framePaths, width: width, height: height}, nil
}

func framesExist(paths []string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func purgeCoverFiles(basePath string, framePaths []string) {
	if strings.TrimSpace(basePath) != "" {
		_ = os.Remove(basePath)
	}
	for _, path := range framePaths {
		if strings.TrimSpace(path) != "" {
			_ = os.Remove(path)
		}
	}
}

func writeCoverPNG(path string, source image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("unable to create cover file: %w", err)
	}
	defer file.Close()

	if err := png.Encode(file, source); err != nil {
		return fmt.Errorf("unable to encode cover png: %w", err)
	}

	return nil
}

func generateCoverFrames(source image.Image, framePaths []string) error {
	bounds := source.Bounds()

	for index, path := range framePaths {
		alpha := float64(index+1) / float64(len(framePaths))
		frame := image.NewNRGBA(bounds)
		draw.Draw(frame, bounds, source, bounds.Min, draw.Src)
		if alpha < 1 {
			for i := 3; i < len(frame.Pix); i += 4 {
				frame.Pix[i] = uint8(float64(frame.Pix[i]) * alpha)
			}
		}

		if err := writeCoverPNG(path, frame); err != nil {
			return err
		}
	}

	return nil
}

func renderKittyImageFromFile(filePath string, cols, rows, cropWidth, cropHeight int) (string, error) {
	if strings.TrimSpace(filePath) == "" {
		return "", fmt.Errorf("cover file path missing")
	}
	if cols <= 0 {
		cols = 20
	}
	if rows <= 0 {
		rows = 10
	}

	encoded := base64.StdEncoding.EncodeToString([]byte(filePath))
	params := fmt.Sprintf("a=T,f=100,t=f,c=%d,r=%d,q=2,C=1,z=1", cols, rows)
	if cropWidth > 0 && cropHeight > 0 {
		params = fmt.Sprintf("%s,w=%d,h=%d", params, cropWidth, cropHeight)
	}
	return fmt.Sprintf("\x1b_G%s;%s\x1b\\", params, encoded), nil
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

func formatChapterLabel(chapter Chapter) string {
	label := "Chapter"
	if chapter.Number != "" {
		label = fmt.Sprintf("Chapter %s", chapter.Number)
	}
	if chapter.Title != "" {
		label = fmt.Sprintf("%s - %s", label, chapter.Title)
	}
	if chapter.Volume != "" {
		label = fmt.Sprintf("Volume %s, %s", chapter.Volume, label)
	}
	return label
}

func removeDuplicates(arr []string) []string {
	seen := make(map[string]bool)
	result := []string{}

	for _, value := range arr {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
