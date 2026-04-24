package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func sanitizeFileName(title string) string {
	if title == "" {
		return ""
	}

	replacements := map[rune]rune{
		'/':  '-',
		'\\': '-',
		'<':  '-',
		'>':  '-',
		':':  '-',
		'"':  '-',
		'|':  '-',
		'?':  0,
		'*':  0,
	}

	var result strings.Builder
	for _, char := range title {
		if char < 32 {
			continue
		}

		if replacement, exists := replacements[char]; exists {
			if replacement != 0 {
				result.WriteRune(replacement)
			}
			continue
		}

		result.WriteRune(char)
	}

	sanitized := strings.Trim(result.String(), " .")
	for strings.Contains(sanitized, "  ") {
		sanitized = strings.ReplaceAll(sanitized, "  ", " ")
	}

	runes := []rune(sanitized)
	if len(runes) > 100 {
		truncated := string(runes[:100])
		if lastSpace := strings.LastIndex(truncated, " "); lastSpace > 70 {
			sanitized = truncated[:lastSpace]
		} else {
			sanitized = truncated
		}
	}

	return strings.TrimSpace(sanitized)
}

func (media *Media) populateInfo() error {
	jsonPath := filepath.Join(media.tmpDir, media.randomName+".info.json")

	buf, err := os.ReadFile(jsonPath)
	if err != nil {
		return fmt.Errorf("error reading json file '%s': %w", jsonPath, err)
	}

	if err := json.Unmarshal(buf, media); err != nil {
		return fmt.Errorf("error parsing json content: %w", err)
	}

	if err := os.Remove(jsonPath); err != nil {
		return fmt.Errorf("error deleting json file '%s': %w", jsonPath, err)
	}

	return nil
}

func (media *Media) renameToReadableName() error {
	if media.Title == "" {
		return fmt.Errorf("no title available")
	}

	sanitizedTitle := sanitizeFileName(media.Title)
	if sanitizedTitle == "" {
		return fmt.Errorf("title sanitization resulted in empty string")
	}

	extension := filepath.Ext(media.Path)
	if extension == "" {
		extension = ".mp4"
	}
	if media.audioOnly {
		extension = ".mp3"
	}

	newFileName := sanitizedTitle + extension
	newPath := filepath.Join(media.tmpDir, newFileName)

	if _, err := os.Stat(newPath); err == nil {
		newFileName = sanitizedTitle + "_" + media.randomName[:8] + extension
		newPath = filepath.Join(media.tmpDir, newFileName)
	}

	if err := os.Rename(media.Path, newPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	oldPath := media.Path
	media.Path = newPath
	media.FileName = newFileName

	log.Printf("[%s]: renamed file from '%s' to '%s'", media.user, filepath.Base(oldPath), newFileName)

	return nil
}
