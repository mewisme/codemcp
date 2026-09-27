package telegram

import (
	"errors"
	"mime"
	"path/filepath"
	"strings"
)

var safeTelegramDocumentTypes = map[string]map[string]bool{
	".json":  {"application/json": true, "text/json": true, "text/plain": true},
	".jsonl": {"application/jsonl": true, "application/x-ndjson": true, "text/plain": true},
	".md":    {"text/markdown": true, "text/plain": true},
	".txt":   {"text/plain": true},
}

func ValidateDocument(document Document) error {
	if strings.TrimSpace(document.FileID) == "" {
		return errors.New("telegram document file id is required")
	}
	if err := validateTelegramFileSize(document.FileSize); err != nil {
		return errors.New("telegram document exceeds the allowed size")
	}
	if document.FileName != "" {
		base := filepath.Base(document.FileName)
		if base != document.FileName || base == "." || base == string(filepath.Separator) {
			return errors.New("telegram document filename is unsafe")
		}
		if err := validateDocumentType(document.FileName, document.MimeType); err != nil {
			return err
		}
	}
	return nil
}

func ValidateDocumentUpload(upload DocumentUpload) error {
	if strings.TrimSpace(upload.FileName) == "" || filepath.Base(upload.FileName) != upload.FileName {
		return errors.New("telegram document filename is unsafe")
	}
	if err := validateTelegramFileSize(int64(len(upload.Data))); err != nil {
		return errors.New("telegram document exceeds the allowed size")
	}
	if err := validateDocumentType(upload.FileName, upload.ContentType); err != nil {
		return err
	}
	return nil
}

func validateDocumentType(fileName, contentType string) error {
	ext := strings.ToLower(filepath.Ext(fileName))
	allowed, ok := safeTelegramDocumentTypes[ext]
	if !ok {
		return errors.New("telegram document type is unsupported")
	}
	contentType, _, _ = mime.ParseMediaType(strings.TrimSpace(contentType))
	if contentType == "" {
		return nil
	}
	if !allowed[strings.ToLower(contentType)] {
		return errors.New("telegram document content type does not match the filename")
	}
	return nil
}
