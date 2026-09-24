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
