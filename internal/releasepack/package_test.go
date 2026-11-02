package releasepack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestArchiveContentsPermissionsAndDeterminism(t *testing.T) {
	for _, windows := range []bool{true, false} {
		name := "iptest"
		if windows {
			name += ".exe"
		}
		entries := []entry{{name: name, mode: 0755, data: []byte("binary")}, {name: "README.md", mode: 0644, data: []byte("usage")}, {name: "LICENSE", mode: 0644, data: []byte("MIT License")}}
		var first, second bytes.Buffer
		if err := writeArchive(&first, entries, windows); err != nil {
			t.Fatal(err)
		}
		if err := writeArchive(&second, entries, windows); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first.Bytes(), second.Bytes()) {
			t.Fatal("same inputs produced different archives")
		}
		seen := make(map[string][]byte)
		if windows {
			archive, err := zip.NewReader(bytes.NewReader(first.Bytes()), int64(first.Len()))
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range archive.File {
				body, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				seen[file.Name], err = io.ReadAll(body)
				body.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
		} else {
			compressed, err := gzip.NewReader(bytes.NewReader(first.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			archive := tar.NewReader(compressed)
			for {
				header, err := archive.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if header.Name == "iptest" && header.Mode != 0755 {
					t.Fatal("Unix executable permission lost")
				}
				seen[header.Name], err = io.ReadAll(archive)
				if err != nil {
					t.Fatal(err)
				}
			}
			compressed.Close()
		}
		if len(seen) != 3 || string(seen[name]) != "binary" || string(seen["README.md"]) != "usage" || string(seen["LICENSE"]) != "MIT License" {
			t.Fatalf("missing or extra package content: %v", seen)
		}
	}
}

func TestPackageRejectsInvalidVersionsTargetsAndMismatchedBinaries(t *testing.T) {
	for _, version := range []string{"", "1.0.0", "../v1.0.0", "v1.0.0/../../file", "v01.0.0", "v1.0.0;command"} {
		if _, err := Name(version, "linux", "amd64"); err == nil {
			t.Errorf("accepted unsafe version %q", version)
		}
	}
	if _, err := Name("v1.0.0", "android", "arm64"); err == nil {
		t.Fatal("advertised unsupported target")
	}
	options := Options{Version: "v0.1.0-rc.1", OS: "linux", Arch: "amd64"}
	valid := func() *debug.BuildInfo {
		return &debug.BuildInfo{Path: module + "/cmd/iptest", Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "linux"}, {Key: "GOARCH", Value: "amd64"}, {Key: "CGO_ENABLED", Value: "0"},
			{Key: "-ldflags", Value: "-s -w -X " + module + "/internal/buildinfo.Version=v0.1.0-rc.1"},
		}}
	}
	if err := validateBuild(valid(), options); err != nil {
		t.Fatal(err)
	}
	for index := range 3 {
		info := valid()
		info.Settings[index].Value = "wrong"
		if err := validateBuild(info, options); err == nil {
			t.Fatalf("mismatched build setting %d accepted", index)
		}
	}
	info := valid()
	info.Path = module + "/cmd/releasepack"
	if err := validateBuild(info, options); err == nil {
		t.Fatal("packaging tool accepted as the application")
	}
	if err := validateBuild(nil, options); err == nil {
		t.Fatal("missing metadata accepted")
	}
	if !validVersionStamp([]byte("prefix iptest-version:v0.1.0\x00suffix"), "v0.1.0") || validVersionStamp([]byte("iptest-version:v0.1.0-rc.1\x00"), "v0.1.0") || validVersionStamp([]byte("v0.1.0\x00"), "v0.1.0") {
		t.Fatal("binary version stamp was not checked exactly")
	}
}

func TestCompleteChecksumManifestIncludesOnlyTheSelectedSixPackages(t *testing.T) {
	directory := t.TempDir()
	version := "v0.1.0-rc.1"
	var expected []string
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := Name(version, platform, arch)
			data := []byte(platform + arch)
			if err := os.WriteFile(filepath.Join(directory, name), data, 0644); err != nil {
				t.Fatal(err)
			}
			expected = append(expected, fmt.Sprintf("%x  %s", sha256.Sum256(data), name))
		}
	}
	if err := os.WriteFile(filepath.Join(directory, "local-secrets.txt"), []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Checksums(directory, version, true); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != strings.Join(expected, "\n")+"\n" {
		t.Fatalf("incorrect checksum manifest: %s", manifest)
	}
	name, _ := Name(version, "windows", "amd64")
	if err := os.Remove(filepath.Join(directory, name)); err != nil {
		t.Fatal(err)
	}
	if err := Checksums(directory, version, true); err == nil {
		t.Fatal("incomplete publication manifest accepted")
	}
	if err := Checksums(directory, version, false); err != nil {
		t.Fatal(err)
	}
}
