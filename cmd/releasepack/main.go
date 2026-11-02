// releasepack is a maintainer tool; it is not included in user packages.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/ObsidianLoneCrane/iptest/internal/releasepack"
)

func main() {
	options := releasepack.Options{}
	flag.StringVar(&options.Binary, "binary", "", "already-built iptest executable")
	flag.StringVar(&options.Version, "version", "", "release/candidate version, such as v0.1.0-rc.1")
	flag.StringVar(&options.OS, "os", runtime.GOOS, "binary target OS")
	flag.StringVar(&options.Arch, "arch", runtime.GOARCH, "binary target CPU")
	flag.StringVar(&options.Out, "out", "dist", "output directory")
	flag.StringVar(&options.Readme, "readme", "docs/install.md", "user installation instructions")
	flag.StringVar(&options.License, "license", "LICENSE", "project MIT license")
	manifest := flag.Bool("checksums-only", false, "only generate SHA256SUMS from existing packages")
	all := flag.Bool("require-all", false, "require all six packages for SHA256SUMS")
	flag.Parse()
	var err error
	if flag.NArg() != 0 {
		err = fmt.Errorf("positional arguments are not supported")
	} else if *manifest {
		err = releasepack.Checksums(options.Out, options.Version, *all)
	} else {
		var path string
		path, err = releasepack.Create(options)
		if err == nil {
			fmt.Println(path)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "releasepack:", err)
		os.Exit(1)
	}
}
