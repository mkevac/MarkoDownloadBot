package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestMediaDelete(t *testing.T) {
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_delete_"+uuid.New().String()+".txt")

	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	file.Close()

	media := &Media{
		Path: testFile,
	}

	err = media.Delete()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Error("File should have been deleted")
	}

	err = media.Delete()
	if err == nil {
		t.Error("Expected error when deleting non-existent file")
	}
}

func TestMediaGetFileSize(t *testing.T) {
	tmpDir := os.TempDir()
	testFile := filepath.Join(tmpDir, "test_size_"+uuid.New().String()+".txt")
	testContent := "Hello, World!"

	err := os.WriteFile(testFile, []byte(testContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer os.Remove(testFile)

	media := &Media{
		Path: testFile,
	}

	size, err := media.GetFileSize()
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	expectedSize := int64(len(testContent))
	if size != expectedSize {
		t.Errorf("Expected size %d, got %d", expectedSize, size)
	}

	media.Path = "/non/existent/file.txt"
	_, err = media.GetFileSize()
	if err == nil {
		t.Error("Expected error when getting size of non-existent file")
	}
}

func TestDownloadMediaInvalidURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "empty URL", url: ""},
		{name: "malformed URL", url: "not-a-url"},
		{name: "URL without scheme", url: "example.com/video"},
		{name: "URL without host", url: "https://"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := os.TempDir()
			_, err := DownloadMedia(context.Background(), tt.url, "testuser", tmpDir, "", false, nil)
			if err == nil {
				t.Error("Expected error for invalid URL")
			}
		})
	}
}
