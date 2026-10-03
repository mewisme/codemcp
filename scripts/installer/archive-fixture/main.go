package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	format := flag.String("format", "", "archive format: tar or zip")
	fixture := flag.String("case", "valid", "fixture case")
	output := flag.String("output", "", "archive output path")
	flag.Parse()
	if strings.TrimSpace(*output) == "" {
		fatal(errors.New("output path is required"))
	}
	var err error
	switch *format {
	case "tar":
		err = writeTar(*output, *fixture)
	case "zip":
		err = writeZip(*output, *fixture)
	default:
		err = fmt.Errorf("unsupported format %q", *format)
	}
	if err != nil {
		fatal(err)
	}
}

func writeTar(path, fixture string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	entries, err := tarFixture(fixture)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o755, Typeflag: entry.kind, Linkname: entry.link}
		if entry.kind == tar.TypeReg || entry.kind == 0 {
			header.Size = int64(len(entry.content))
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if header.Size > 0 {
			if _, err := writer.Write(entry.content); err != nil {
				return err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type tarItem struct {
	name    string
	content []byte
	kind    byte
	link    string
}

func tarFixture(fixture string) ([]tarItem, error) {
	binary := []byte("#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then [ \"${TEST_STARTUP_EXIT:-0}\" = 0 ] || exit \"$TEST_STARTUP_EXIT\"; exit 0; fi\nif [ -n \"${TEST_INSTALL_ENV_MARKER:-}\" ]; then printf '%s' \"${CM_INSTALL_INTEGRATIONS-<unset>}\" >\"$TEST_INSTALL_ENV_MARKER\"; fi\n: >\"$TEST_INSTALL_MARKER\"\n")
	regular := func(name string, content []byte) tarItem {
		return tarItem{name: name, content: content, kind: tar.TypeReg}
	}
	switch fixture {
	case "valid":
		return []tarItem{regular("cm", binary)}, nil
	case "valid-large":
		padding := bytes.Repeat([]byte("# release extraction padding\n"), 500_000)
		return []tarItem{regular("cm", append(binary, padding...))}, nil
	case "traversal":
		return []tarItem{regular("../escape", []byte("bad")), regular("cm", binary)}, nil
	case "absolute":
		return []tarItem{regular("/escape", []byte("bad")), regular("cm", binary)}, nil
	case "drive":
		return []tarItem{regular(`C:\escape`, []byte("bad")), regular("cm", binary)}, nil
	case "duplicate":
		return []tarItem{regular("cm", binary), regular("cm", binary)}, nil
	case "missing":
		return []tarItem{regular("README.md", []byte("missing"))}, nil
	case "symlink":
		return []tarItem{{name: "cm", kind: tar.TypeSymlink, link: "other"}}, nil
	case "nonregular":
		return []tarItem{regular("cm", binary), {name: "payload.fifo", kind: tar.TypeFifo}}, nil
	case "empty":
		return []tarItem{regular("cm", nil)}, nil
	case "aliased":
		return []tarItem{regular("./cm", binary)}, nil
	default:
		return nil, fmt.Errorf("unsupported tar fixture %q", fixture)
	}
}

func writeZip(path, fixture string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	writer := zip.NewWriter(file)
	entries, err := zipFixture(fixture)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		if entry.reparse {
			header.ExternalAttrs |= 0x400
		}
		stream, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if len(entry.content) > 0 {
			if _, err := stream.Write(entry.content); err != nil {
				return err
			}
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

type zipItem struct {
	name    string
	content []byte
	mode    os.FileMode
	reparse bool
}

func zipFixture(fixture string) ([]zipItem, error) {
	binary := []byte("fixture-binary")
	regular := func(name string, content []byte) zipItem {
		return zipItem{name: name, content: content, mode: 0o755}
	}
	switch fixture {
	case "valid":
		return []zipItem{regular("cm.exe", binary)}, nil
	case "traversal":
		return []zipItem{regular("../escape", []byte("bad")), regular("cm.exe", binary)}, nil
	case "absolute":
		return []zipItem{regular("/escape", []byte("bad")), regular("cm.exe", binary)}, nil
	case "drive":
		return []zipItem{regular(`C:\escape`, []byte("bad")), regular("cm.exe", binary)}, nil
	case "duplicate":
		return []zipItem{regular("cm.exe", binary), regular("cm.exe", binary)}, nil
	case "missing":
		return []zipItem{regular("README.md", []byte("missing"))}, nil
	case "symlink":
		return []zipItem{{name: "cm.exe", content: []byte("other"), mode: os.ModeSymlink | 0o777}}, nil
	case "reparse":
		return []zipItem{{name: "cm.exe", content: binary, mode: 0o755, reparse: true}}, nil
	case "nonregular":
		return []zipItem{regular("cm.exe", binary), {name: "payload.pipe", mode: os.ModeNamedPipe | 0o600}}, nil
	case "empty":
		return []zipItem{regular("cm.exe", nil)}, nil
	case "aliased":
		return []zipItem{regular("./cm.exe", binary)}, nil
	default:
		return nil, fmt.Errorf("unsupported zip fixture %q", fixture)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
