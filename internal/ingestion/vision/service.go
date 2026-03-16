package vision

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"pa/internal/llm"

	"github.com/rwcarlsen/goexif/exif"
)

// Metadata represents extracted image metadata.
type Metadata struct {
	FileName     string         `json:"file_name"`
	FilePath     string         `json:"file_path"`
	FileSize     int64          `json:"file_size"`
	Width        int            `json:"width"`
	Height       int            `json:"height"`
	DateTaken    *time.Time     `json:"date_taken,omitempty"`
	Camera       string         `json:"camera,omitempty"`
	Lens         string         `json:"lens,omitempty"`
	ISO          string         `json:"iso,omitempty"`
	FocalLength  string         `json:"focal_length,omitempty"`
	Aperture     string         `json:"aperture,omitempty"`
	ShutterSpeed string         `json:"shutter_speed,omitempty"`
	Additional   map[string]any `json:"additional,omitempty"`
}

// VisionService provides image processing capabilities.
type VisionService struct {
	visionProvider llm.VisionProvider
}

// NewVisionService creates a vision service.
func NewVisionService(visionProvider llm.VisionProvider) *VisionService {
	return &VisionService{
		visionProvider: visionProvider,
	}
}

// Caption generates a caption for an image.
func (s *VisionService) Caption(ctx context.Context, imagePath string, prompt string) (string, error) {
	// Read and encode image
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}

	base64Image := base64.StdEncoding.EncodeToString(imageData)
	mediaType := getMediaType(imagePath)

	messages := []llm.VisionMessage{
		{
			Role:        llm.RoleUser,
			Content:     prompt,
			ImageBase64: base64Image,
			MediaType:   mediaType,
		},
	}

	caption, err := s.visionProvider.Vision(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("vision call: %w", err)
	}

	return caption, nil
}

// ExtractMetadata extracts all available metadata from an image file.
func (s *VisionService) ExtractMetadata(filePath string) (*Metadata, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	// Get basic info
	m := &Metadata{
		FileName: filepath.Base(filePath),
		FilePath: filePath,
		FileSize: fileInfo.Size(),
	}

	// Extract image dimensions
	w, h, err := getImageDimensions(filePath)
	if err != nil {
		slog.Warn("failed to get image dimensions", "path", filePath, "error", err)
	} else {
		m.Width = w
		m.Height = h
	}

	// Extract EXIF data
	exifData, err := extractEXIFData(filePath)
	if err != nil {
		slog.Debug("no EXIF data found", "path", filePath, "error", err)
	} else {
		m.DateTaken = exifData.DateTaken
		m.Camera = exifData.Camera
		m.Lens = exifData.Lens
		m.ISO = exifData.ISO
		m.FocalLength = exifData.FocalLength
		m.Aperture = exifData.Aperture
		m.ShutterSpeed = exifData.ShutterSpeed
		m.Additional = exifData.Additional
	}

	return m, nil
}

func getMediaType(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "image/jpeg"
	}
}

func getImageDimensions(filePath string) (int, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
}

// EXIFData represents extracted EXIF information.
type EXIFData struct {
	DateTaken    *time.Time
	Camera       string
	Lens         string
	ISO          string
	FocalLength  string
	Aperture     string
	ShutterSpeed string
	Additional   map[string]any
}

func extractEXIFData(filePath string) (*EXIFData, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	exifData, err := exif.Decode(file)
	if err != nil {
		return nil, err
	}

	data := &EXIFData{
		Additional: make(map[string]any),
	}

	// DateTime
	dt, err := exifData.DateTime()
	if err == nil {
		data.DateTaken = &dt
	}

	// Camera model
	if model, err := exifData.Get(exif.Model); err == nil {
		data.Camera = model.String()
	}

	// ISO
	if iso, err := exifData.Get(exif.ISOSpeedRatings); err == nil {
		data.ISO = iso.String()
	}

	// Focal length
	if fl, err := exifData.Get(exif.FocalLength); err == nil {
		data.FocalLength = fl.String()
	}

	// Aperture (F-number)
	if fn, err := exifData.Get(exif.FNumber); err == nil {
		data.Aperture = fn.String()
	}

	// Exposure time (shutter speed)
	if et, err := exifData.Get(exif.ExposureTime); err == nil {
		data.ShutterSpeed = et.String()
	}

	// Lens model (if available)
	if lens, err := exifData.Get(exif.LensModel); err == nil {
		data.Lens = lens.String()
	}

	return data, nil
}
