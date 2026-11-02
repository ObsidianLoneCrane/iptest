// Package releasepack creates distributable archives without external archivers.
package releasepack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	versioninfo "github.com/ObsidianLoneCrane/iptest/internal/buildinfo"
)

const module = "github.com/ObsidianLoneCrane/iptest"

var versionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+([.-][0-9A-Za-z]+)*)?$`)

// Options names exactly one already-built OS/CPU artifact and its public docs.
type Options struct {
	Binary, Readme, License, Out, Version, OS, Arch string
}

// Name validates the target/version and returns the public archive filename.
func Name(version, platform, arch string) (string, error) {
	if !versionPattern.MatchString(version) || (platform != "windows" && platform != "linux" && platform != "darwin") || (arch != "amd64" && arch != "arm64") {
		return "", errors.New("expected a vMAJOR.MINOR.PATCH[-prerelease] version and supported OS/CPU")
	}
	extension := ".tar.gz"
	if platform == "windows" {
		extension = ".zip"
	}
	return "iptest_" + version + "_" + platform + "_" + arch + extension, nil
}

// Create checks the executable's Go metadata before writing an exclusive archive.
// Existing release assets are never silently replaced.
func Create(options Options) (string, error) {
	name, err := Name(options.Version, options.OS, options.Arch)
	if err != nil {
		return "", err
	}
	info, err := buildinfo.ReadFile(options.Binary)
	if err != nil {
		return "", errors.New("could not read Go executable metadata")
	}
	if err := validateBuild(info, options); err != nil {
		return "", err
	}
	binaryName := "iptest"
	if options.OS == "windows" {
		binaryName += ".exe"
	}
	entries := []entry{{name: binaryName, path: options.Binary, mode: 0755}, {name: "README.md", path: options.Readme, mode: 0644}, {name: "LICENSE", path: options.License, mode: 0644}}
	// Read inputs before creating an output so missing docs cannot leave an asset.
	for index := range entries {
		entries[index].data, err = os.ReadFile(entries[index].path)
		if err != nil || len(entries[index].data) == 0 {
			return "", fmt.Errorf("missing or empty package input: %s", entries[index].name)
		}
	}
	if !validVersionStamp(entries[0].data, options.Version) {
		return "", errors.New("binary version stamp does not match the package version")
	}
	if !strings.Contains(string(entries[2].data), "MIT License") {
		return "", errors.New("package license must contain the project's MIT license")
	}
	if err := os.MkdirAll(options.Out, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(options.Out, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return "", err
	}
	err = writeArchive(file, entries, options.OS == "windows")
	closeErr := file.Close()
	if err = errors.Join(err, closeErr); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func validateBuild(info *debug.BuildInfo, options Options) error {
	if info == nil || info.Path != module+"/cmd/iptest" {
		return errors.New("binary is not the iptest application")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != options.OS || settings["GOARCH"] != options.Arch || settings["CGO_ENABLED"] != "0" {
		return errors.New("binary OS/CPU or CGO setting does not match the package")
	}
	return nil
}

func validVersionStamp(binary []byte, version string) bool {
	return bytes.Contains(binary, []byte(versioninfo.VersionPrefix+version+"\x00"))
}

type entry struct {
	name, path string
	mode       os.FileMode
	data       []byte
}

func writeArchive(writer io.Writer, entries []entry, windows bool) error {
	if windows {
		archive := zip.NewWriter(writer)
		for _, item := range entries {
			header := &zip.FileHeader{Name: item.name, Method: zip.Deflate}
			header.SetMode(item.mode)
			header.SetModTime(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC))
			member, err := archive.CreateHeader(header)
			if err != nil {
				archive.Close()
				return err
			}
			if _, err := member.Write(item.data); err != nil {
				archive.Close()
				return err
			}
		}
		return archive.Close()
	}
	compressed := gzip.NewWriter(writer)
	archive := tar.NewWriter(compressed)
	for _, item := range entries {
		header := &tar.Header{Name: item.name, Mode: int64(item.mode), Size: int64(len(item.data)), ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			archive.Close()
			compressed.Close()
			return err
		}
		if _, err := archive.Write(item.data); err != nil {
			archive.Close()
			compressed.Close()
			return err
		}
	}
	return errors.Join(archive.Close(), compressed.Close())
}

// Checksums writes a sorted SHA256SUMS manifest for this version only. A complete
// publication manifest requires all six packages; local candidate bundles may
// deliberately contain one native package.
func Checksums(directory, version string, requireAll bool) error {
	if _, err := Name(version, "windows", "amd64"); err != nil {
		return err
	}
	var names []string
	for _, platform := range []string{"windows", "linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name, _ := Name(version, platform, arch)
			if _, err := os.Stat(filepath.Join(directory, name)); err == nil {
				names = append(names, name)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
	}
	if len(names) == 0 || requireAll && len(names) != 6 {
		return errors.New("required release packages are missing")
	}
	sort.Strings(names)
	var manifest strings.Builder
	for _, name := range names {
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, readErr := io.Copy(digest, file)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, "%x  %s\n", digest.Sum(nil), name)
	}
	return os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(manifest.String()), 0644)
}
