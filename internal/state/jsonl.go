package state

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const DefaultMaxJSONLLineBytes = 1 << 20

func AppendJSONL(path string, value any, perm os.FileMode, maxLineBytes int) error {
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxJSONLLineBytes
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	data := buffer.Bytes()
	if len(data) > maxLineBytes {
		return fmt.Errorf("JSONL record exceeds %d byte limit", maxLineBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return err
	}
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func ReadJSONL(path string, maxLineBytes int, fn func([]byte) error) error {
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxJSONLLineBytes
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	for {
		line, complete, err := readJSONLLine(reader, maxLineBytes)
		if complete && fn != nil {
			if fnErr := fn(line); fnErr != nil {
				return fnErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ReadJSONLRecoverTail reads complete JSONL records while treating an
// incomplete or malformed final record as a recoverable tail issue. Corruption
// before the final record is still returned as an error.
func ReadJSONLRecoverTail(path string, maxLineBytes int, fn func([]byte) error) (bool, error) {
	if maxLineBytes <= 0 {
		maxLineBytes = DefaultMaxJSONLLineBytes
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var pending []byte
	for {
		line, complete, readErr := readJSONLLine(reader, maxLineBytes)
		if complete {
			if pending != nil && fn != nil {
				if fnErr := fn(pending); fnErr != nil {
					return false, fnErr
				}
			}
			pending = append(pending[:0], line...)
		}
		if errors.Is(readErr, io.EOF) {
			tailIssue := !complete && len(line) > 0
			if pending != nil && fn != nil {
				if fnErr := fn(pending); fnErr != nil {
					if !tailIssue {
						return true, nil
					}
					return false, fnErr
				}
			}
			return tailIssue, nil
		}
		if readErr != nil {
			return false, readErr
		}
	}
}

// RepairJSONLTail removes an incomplete or syntactically malformed final JSONL
// record while preserving every valid record before it. It is safe to call
// before appending after a process crash or short write.
func RepairJSONLTail(path string) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("JSONL path is not a regular file")
	}
	size := info.Size()
	if size == 0 {
		return false, nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, size-1); err != nil {
		return false, err
	}
	if last[0] == '\n' {
		start, err := previousJSONLBoundary(file, size-1)
		if err != nil {
			return false, err
		}
		record := make([]byte, size-1-start)
		if _, err := file.ReadAt(record, start); err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		if json.Valid(record) {
			return false, nil
		}
		return true, truncateJSONLFile(file, start)
	}
	const chunkSize int64 = 4096
	truncateAt := int64(0)
	for end := size; end > 0; {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		buffer := make([]byte, end-start)
		if _, err := file.ReadAt(buffer, start); err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		if index := bytes.LastIndexByte(buffer, '\n'); index >= 0 {
			truncateAt = start + int64(index) + 1
			break
		}
		end = start
	}
	return true, truncateJSONLFile(file, truncateAt)
}

// DropJSONLTailRecord removes the final physical JSONL record regardless of
// whether it is newline-terminated. Callers should validate that the final
// record is recoverably corrupt before using it.
func DropJSONLTailRecord(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("JSONL path is not a regular file")
	}
	if info.Size() == 0 {
		return nil
	}
	last := []byte{0}
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	before := info.Size()
	if last[0] == '\n' {
		before--
	}
	start, err := previousJSONLBoundary(file, before)
	if err != nil {
		return err
	}
	return truncateJSONLFile(file, start)
}

func truncateJSONLFile(file *os.File, offset int64) error {
	if err := file.Truncate(offset); err != nil {
		return err
	}
	return file.Sync()
}

func previousJSONLBoundary(file *os.File, before int64) (int64, error) {
	const chunkSize int64 = 4096
	for end := before; end > 0; {
		start := end - chunkSize
		if start < 0 {
			start = 0
		}
		buffer := make([]byte, end-start)
		if _, err := file.ReadAt(buffer, start); err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		if index := bytes.LastIndexByte(buffer, '\n'); index >= 0 {
			return start + int64(index) + 1, nil
		}
		end = start
	}
	return 0, nil
}

func readJSONLLine(reader *bufio.Reader, maxLineBytes int) ([]byte, bool, error) {
	line := make([]byte, 0)
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(line)+len(fragment) > maxLineBytes {
			return nil, false, fmt.Errorf("JSONL record exceeds %d byte limit", maxLineBytes)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			return line[:len(line)-1], true, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return line, false, io.EOF
		default:
			return nil, false, err
		}
	}
}
