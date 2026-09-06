package portal

import (
	"bytes"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	_ "golang.org/x/image/webp"
)

var imageNamePattern = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png)$`)

type ImageStore struct {
	directory string
	maxBytes  int64
}

func NewImageStore(directory string) (*ImageStore, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &ImageStore{directory: abs, maxBytes: 3 << 20}, nil
}

func (store *ImageStore) Save(name string, source io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(source, store.maxBytes+1))
	if err != nil || len(data) == 0 || int64(len(data)) > store.maxBytes {
		return "", errors.New("invalid image size")
	}
	mime := http.DetectContentType(data[:min(len(data), 512)])
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" {
		return "", errors.New("unsupported image")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 || int64(config.Width)*int64(config.Height) > 16_000_000 {
		return "", errors.New("invalid image")
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("invalid image")
	}
	random, err := randomHex(16)
	if err != nil {
		return "", err
	}
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
	}
	filename := random + ext
	temporary, err := os.CreateTemp(store.directory, ".upload-")
	if err != nil {
		return "", err
	}
	tmp := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(tmp)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", err
	}
	if format == "jpeg" {
		err = jpeg.Encode(temporary, decoded, &jpeg.Options{Quality: 88})
	} else {
		err = png.Encode(temporary, decoded)
	}
	if err != nil || temporary.Close() != nil {
		return "", errors.New("could not sanitize image")
	}
	if info, err := os.Stat(tmp); err != nil || info.Size() > store.maxBytes {
		return "", errors.New("sanitized image is too large")
	}
	if err := os.Rename(tmp, filepath.Join(store.directory, filename)); err != nil {
		return "", err
	}
	committed = true
	_ = name
	return filename, nil
}

func (store *ImageStore) Open(filename string) (*os.File, error) {
	if !imageNamePattern.MatchString(filename) {
		return nil, os.ErrNotExist
	}
	return os.Open(filepath.Join(store.directory, filename))
}

func (store *ImageStore) Delete(filename string) {
	if imageNamePattern.MatchString(filename) {
		_ = os.Remove(filepath.Join(store.directory, filename))
	}
}
