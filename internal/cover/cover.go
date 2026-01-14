package cover

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const FadeFrames = 6

type Image struct {
	FilePath string
	Frames   []string
	Width    int
	Height   int
}

func cacheDir() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("unable to resolve cache dir: %w", err)
	}

	return filepath.Join(cacheDir, "boox-serve", "covers"), nil
}

func cacheKey(coverURL string) (string, error) {
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

func cachePaths(coverURL string) (string, []string, error) {
	cacheDir, err := cacheDir()
	if err != nil {
		return "", nil, err
	}

	key, err := cacheKey(coverURL)
	if err != nil {
		return "", nil, err
	}

	basePath := filepath.Join(cacheDir, key+".png")
	frames := make([]string, FadeFrames)
	for i := 0; i < FadeFrames; i++ {
		frames[i] = filepath.Join(cacheDir, fmt.Sprintf("%s-fade-%02d.png", key, i+1))
	}

	return basePath, frames, nil
}

func LoadCachedCover(coverURL string) (Image, bool, error) {
	basePath, framePaths, err := cachePaths(coverURL)
	if err != nil {
		return Image{}, false, err
	}

	if _, err := os.Stat(basePath); err != nil {
		if os.IsNotExist(err) {
			return Image{}, false, nil
		}
		return Image{}, false, err
	}

	file, err := os.Open(basePath)
	if err != nil {
		return Image{}, false, err
	}
	defer file.Close()

	decoded, _, err := image.Decode(file)
	if err != nil {
		purgeCoverFiles(basePath, framePaths)
		return Image{}, false, nil
	}

	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if !framesExist(framePaths) {
		if err := generateCoverFrames(decoded, framePaths); err != nil {
			return Image{}, false, err
		}
	}

	return Image{FilePath: basePath, Frames: framePaths, Width: width, Height: height}, true, nil
}

func ClearCache() error {
	cacheDir, err := cacheDir()
	if err != nil {
		return err
	}

	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("unable to clear cover cache: %w", err)
	}

	return nil
}

func SaveCoverImage(coverURL string, data []byte) (Image, error) {
	if len(data) == 0 {
		return Image{}, fmt.Errorf("empty image data")
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("unable to decode cover image: %w", err)
	}

	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	basePath, framePaths, err := cachePaths(coverURL)
	if err != nil {
		return Image{}, err
	}

	if err := os.MkdirAll(filepath.Dir(basePath), 0o755); err != nil {
		return Image{}, fmt.Errorf("unable to create cover cache: %w", err)
	}

	if err := writeCoverPNG(basePath, decoded); err != nil {
		return Image{}, err
	}

	if err := generateCoverFrames(decoded, framePaths); err != nil {
		return Image{}, err
	}

	return Image{FilePath: basePath, Frames: framePaths, Width: width, Height: height}, nil
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

func RenderKittyImageFromFile(filePath string, cols, rows, cropWidth, cropHeight int) (string, error) {
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
