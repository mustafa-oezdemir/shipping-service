package portal

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"
)

func TestImageStoreSanitizesAndScopesFiles(t *testing.T) {
	store, err := NewImageStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	filename, err := store.Save("../../avatar.php", &source)
	if err != nil {
		t.Fatal(err)
	}
	if !imageNamePattern.MatchString(filename) {
		t.Fatalf("unsafe generated filename: %q", filename)
	}
	file, err := store.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if _, err := store.Open("../" + filename); !os.IsNotExist(err) {
		t.Fatalf("path traversal was not rejected: %v", err)
	}
	if _, err := store.Save("payload.png", bytes.NewBufferString("not an image")); err == nil {
		t.Fatal("non-image payload was accepted")
	}
	store.Delete(filename)
	if _, err := store.Open(filename); !os.IsNotExist(err) {
		t.Fatalf("image was not deleted: %v", err)
	}
}
