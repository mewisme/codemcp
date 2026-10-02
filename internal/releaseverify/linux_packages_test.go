package releaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const testLinuxPackageMaintainer = "mewisme <123456+mewisme@users.noreply.github.com>"

func TestInspectDebPackageCanonicalContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.deb")
	if err := os.WriteFile(path, buildDebFixture(t, false), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := inspectDebPackage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLinuxPackageInfo(info, "amd64", "1.2.3-next"); err != nil {
		t.Fatal(err)
	}
	if string(info.Binary) != "fixture-cm" {
		t.Fatalf("binary = %q", info.Binary)
	}
}

func TestInspectDebPackageRejectsExtraExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.deb")
	if err := os.WriteFile(path, buildDebFixture(t, true), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectDebPackage(path); err == nil {
		t.Fatal("deb package with retired executable alias was accepted")
	}
}

func TestInspectDebPackageRejectsMaintainerScript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.deb")
	if err := os.WriteFile(path, buildDebFixtureWithControlEntry(t, "postinst"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectDebPackage(path); err == nil || !strings.Contains(err.Error(), "unexpected maintainer metadata") {
		t.Fatalf("error = %v", err)
	}
}

func TestInspectRPMPackageCanonicalContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.rpm")
	if err := os.WriteFile(path, buildRPMFixture(t, false), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := inspectRPMPackage(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateLinuxPackageInfo(info, "x86_64", "1.2.3-next"); err != nil {
		t.Fatal(err)
	}
	if string(info.Binary) != "fixture-cm" {
		t.Fatalf("binary = %q", info.Binary)
	}
}

func TestInspectRPMPackageRejectsMaintainerScriptlet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.rpm")
	if err := os.WriteFile(path, buildRPMFixtureWithHeaderTag(t, 1024, "#!/bin/sh\nprintf mutated\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectRPMPackage(path); err == nil || !strings.Contains(err.Error(), "post-install scriptlet") {
		t.Fatalf("error = %v", err)
	}
}

func TestInspectRPMPackageRejectsUserStatePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codemcp_linux_amd64.rpm")
	if err := os.WriteFile(path, buildRPMFixture(t, true), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectRPMPackage(path); err == nil {
		t.Fatal("rpm package with user-state payload was accepted")
	}
}

func TestNormalizedPackagePathRejectsTraversal(t *testing.T) {
	for _, value := range []string{"../cm", "../../home/user/.cm/state", "C:/cm", "//server/share"} {
		if _, err := normalizedPackagePath(value); err == nil {
			t.Fatalf("unsafe package path %q was accepted", value)
		}
	}
}

func TestVerifyPackagedBinaryVersionDisablesTelemetryState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Linux package executable fixture requires a POSIX host")
	}
	binary := []byte("#!/bin/sh\n" +
		"if [ \"${CM_TELEMETRY:-}\" != \"0\" ]; then mkdir -p \"$CM_CONFIG_DIR\"; printf leaked > \"$CM_CONFIG_DIR/state\"; fi\n" +
		"printf 'cm version 9.9.9 (fixture) fixture\\n'\n")
	if err := verifyPackagedBinaryVersion(context.Background(), binary, "v9.9.9", "deb"); err != nil {
		t.Fatal(err)
	}
}

func buildDebFixture(t *testing.T, includeAlias bool) []byte {
	return buildDebFixtureForArch(t, "amd64", includeAlias, []byte("fixture-cm"))
}

func buildDebFixtureForArch(t *testing.T, arch string, includeAlias bool, binaryData []byte) []byte {
	t.Helper()
	return buildDebFixtureForArchWithControlEntries(t, arch, includeAlias, binaryData, nil)
}

func buildDebFixtureWithControlEntry(t *testing.T, name string) []byte {
	t.Helper()
	return buildDebFixtureForArchWithControlEntries(t, "amd64", false, []byte("fixture-cm"), []tarFixtureEntry{{
		name: name,
		mode: 0755,
		data: []byte("#!/bin/sh\nprintf mutated\n"),
	}})
}

func buildDebFixtureForArchWithControlEntries(t *testing.T, arch string, includeAlias bool, binaryData []byte, extraControl []tarFixtureEntry) []byte {
	t.Helper()
	controlEntries := []tarFixtureEntry{{
		name: "control",
		mode: 0644,
		data: []byte("Package: codemcp\nVersion: 1.2.3~next\nSection: utils\nPriority: optional\nArchitecture: " + arch + "\nMaintainer: " + testLinuxPackageMaintainer + "\nHomepage: " + ExpectedGitRemote + "\nDescription: " + linuxPackageDescription + "\n"),
	}}
	controlEntries = append(controlEntries, extraControl...)
	control := makeGzipTar(t, controlEntries)
	entries := []tarFixtureEntry{
		{name: "./usr/", mode: 0755, directory: true},
		{name: "./usr/bin/", mode: 0755, directory: true},
		{name: "./usr/bin/cm", mode: 0755, data: binaryData},
	}
	if includeAlias {
		entries = append(entries, tarFixtureEntry{name: "./usr/bin/cgm", mode: 0755, data: []byte("alias")})
	}
	data := makeGzipTar(t, entries)
	var out bytes.Buffer
	out.WriteString("!<arch>\n")
	writeArMember(t, &out, "debian-binary", []byte("2.0\n"))
	writeArMember(t, &out, "control.tar.gz", control)
	writeArMember(t, &out, "data.tar.gz", data)
	return out.Bytes()
}

type tarFixtureEntry struct {
	name      string
	mode      int64
	directory bool
	data      []byte
}

func makeGzipTar(t *testing.T, entries []tarFixtureEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		typeflag := byte(tar.TypeReg)
		if entry.directory {
			typeflag = tar.TypeDir
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Uid:      0,
			Gid:      0,
			Typeflag: typeflag,
			Size:     int64(len(entry.data)),
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.data) > 0 {
			if _, err := tw.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeArMember(t *testing.T, out *bytes.Buffer, name string, data []byte) {
	t.Helper()
	header := fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d%s", name+"/", 0, 0, 0, 0644, len(data), "`\n")
	if len(header) != 60 {
		t.Fatalf("ar header length = %d", len(header))
	}
	out.WriteString(header)
	out.Write(data)
	if len(data)%2 != 0 {
		out.WriteByte('\n')
	}
}

func buildRPMFixture(t *testing.T, includeUserState bool) []byte {
	return buildRPMFixtureForArch(t, "x86_64", includeUserState, []byte("fixture-cm"))
}

func buildRPMFixtureForArch(t *testing.T, arch string, includeUserState bool, binaryData []byte) []byte {
	t.Helper()
	return buildRPMFixtureForArchWithHeaderTags(t, arch, includeUserState, binaryData, nil)
}

func buildRPMFixtureWithHeaderTag(t *testing.T, tag uint32, value string) []byte {
	t.Helper()
	return buildRPMFixtureForArchWithHeaderTags(t, "x86_64", false, []byte("fixture-cm"), map[uint32]string{tag: value})
}

func buildRPMFixtureForArchWithHeaderTags(t *testing.T, arch string, includeUserState bool, binaryData []byte, extraTags map[uint32]string) []byte {
	t.Helper()
	payloadEntries := []cpioFixtureEntry{{
		name: "/usr/bin/cm",
		mode: 0100755,
		data: binaryData,
	}}
	if includeUserState {
		payloadEntries = append(payloadEntries, cpioFixtureEntry{
			name: "/home/user/.cm/state/config.json",
			mode: 0100644,
			data: []byte("{}"),
		})
	}
	payload := makeGzipCPIO(t, payloadEntries)
	lead := make([]byte, 96)
	copy(lead[:4], []byte{0xed, 0xab, 0xee, 0xdb})
	signature := buildRPMHeader(nil)
	values := map[uint32]string{
		1000: "codemcp",
		1001: "1.2.3~next",
		1002: "1",
		1004: linuxPackageDescription,
		1005: linuxPackageDescription,
		1011: linuxPackageVendor,
		1014: "MIT",
		1015: testLinuxPackageMaintainer,
		1020: ExpectedGitRemote,
		1022: arch,
		1124: "cpio",
		1125: "gzip",
		1126: "9",
	}
	for tag, value := range extraTags {
		values[tag] = value
	}
	main := buildRPMHeader(values)
	var out bytes.Buffer
	out.Write(lead)
	out.Write(signature)
	for out.Len()%8 != 0 {
		out.WriteByte(0)
	}
	out.Write(main)
	out.Write(payload)
	return out.Bytes()
}

func buildRPMHeader(values map[uint32]string) []byte {
	tags := make([]uint32, 0, len(values))
	for tag := range values {
		tags = append(tags, tag)
	}
	for i := 0; i < len(tags); i++ {
		for j := i + 1; j < len(tags); j++ {
			if tags[j] < tags[i] {
				tags[i], tags[j] = tags[j], tags[i]
			}
		}
	}
	var store bytes.Buffer
	indexes := make([][4]uint32, 0, len(tags))
	for _, tag := range tags {
		offset := uint32(store.Len())
		store.WriteString(values[tag])
		store.WriteByte(0)
		indexes = append(indexes, [4]uint32{tag, 6, offset, 1})
	}
	var out bytes.Buffer
	out.Write([]byte{0x8e, 0xad, 0xe8, 0x01, 0, 0, 0, 0})
	_ = binary.Write(&out, binary.BigEndian, uint32(len(indexes)))
	_ = binary.Write(&out, binary.BigEndian, uint32(store.Len()))
	for _, index := range indexes {
		for _, value := range index {
			_ = binary.Write(&out, binary.BigEndian, value)
		}
	}
	out.Write(store.Bytes())
	return out.Bytes()
}

type cpioFixtureEntry struct {
	name string
	mode uint32
	data []byte
}

func makeGzipCPIO(t *testing.T, entries []cpioFixtureEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	ino := uint32(1)
	for _, entry := range entries {
		writeCPIOEntry(t, &raw, ino, entry)
		ino++
	}
	writeCPIOEntry(t, &raw, ino, cpioFixtureEntry{name: "TRAILER!!!", mode: 0})
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func writeCPIOEntry(t *testing.T, out *bytes.Buffer, ino uint32, entry cpioFixtureEntry) {
	t.Helper()
	name := append([]byte(entry.name), 0)
	fields := []uint64{
		uint64(ino),
		uint64(entry.mode),
		0,
		0,
		1,
		0,
		uint64(len(entry.data)),
		0,
		0,
		0,
		0,
		uint64(len(name)),
		0,
	}
	out.WriteString("070701")
	for _, value := range fields {
		text := strconv.FormatUint(value, 16)
		if len(text) > 8 {
			t.Fatalf("cpio field overflow: %x", value)
		}
		out.WriteString(fmt.Sprintf("%08s", text))
	}
	out.Write(name)
	for out.Len()%4 != 0 {
		out.WriteByte(0)
	}
	out.Write(entry.data)
	for out.Len()%4 != 0 {
		out.WriteByte(0)
	}
}
