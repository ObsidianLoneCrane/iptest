# Using iptest

Download the package for your operating system and CPU from:
https://github.com/ObsidianLoneCrane/iptest/releases

Windows uses ZIP packages. Linux and macOS use tar.gz packages that preserve the
executable permission. `amd64` means Intel/AMD x64; `arm64` includes Apple Silicon
and other ARM64 systems. Android is not included. Check the release notes for the
OS versions and configurations actually verified for that version.

Users do not need Go, this repository's setup scripts, or another speed-test
program. Extract the archive before running the executable from a terminal.

## Windows

```powershell
.\iptest.exe --version
.\iptest.exe
.\iptest.exe --family both
.\iptest.exe --speedtest compare
```

Interface sampling uses Windows PowerShell and its NetAdapter module.

## Linux and macOS

```sh
tar -xzf iptest_VERSION_OS_ARCH.tar.gz
./iptest --version
./iptest
./iptest --family both
./iptest --speedtest compare
```

Replace the example archive filename with your downloaded filename. The archive
contains an executable `iptest`; if your extraction utility removes its executable
permission, restore it with `chmod +x iptest`.

Linux reads interface counters from `/proc/net/dev`; supported desktop proxy
discovery uses GNOME `gsettings` or KDE readers/settings as applicable. macOS uses
the system `scutil` and `netstat` utilities. Follow the release notes for signing
status and ordinary downloaded-application first-launch instructions. Never
disable system-wide security settings to run a package.

## Useful options

- Default inspection selects the configured proxy/system route and inspects IPv4.
- `--family both` adds IPv6 and requires both families; `--family auto` keeps
  available families. `--family ipv6` requests only IPv6.
- `--format plain` selects line-oriented results instead of the grouped report.
- `--no-speedtest` skips active downloads; `--no-traffic` skips interface sampling.
- `--proxy http://127.0.0.1:7890` explicitly selects a proxy when automatic settings
  cannot be resolved. PAC scripts are detected but are not evaluated.
- `--help` lists all options without sending network requests.

Active speed tests consume real data: automatic mode reads at most 16 MiB of
response payload; comparison at most 48 MiB, plus protocol/transport overhead.
Results describe individual targets. Lookup/speed services may be unavailable;
partial results and diagnostics do not imply an IP is clean or unacceptable.

Exit codes are 0 for completed requested stages, 1 for partial/runtime failure,
2 for invalid arguments/configuration, and 130 for cancellation. A failed selected
proxy is not silently replaced by a direct connection.

## Verify your download

Compare the archive's SHA-256 digest with the release's `SHA256SUMS` entry before
extracting it. Windows: `Get-FileHash .\PACKAGE.zip -Algorithm SHA256`.
Linux: `sha256sum -c SHA256SUMS` when all listed archives are present, or
`sha256sum PACKAGE.tar.gz` to compare one digest. macOS:
`shasum -a 256 PACKAGE.tar.gz`. Replace example filenames with actual assets.

## License and source

MIT License. See the bundled LICENSE and the source/documentation at:
https://github.com/ObsidianLoneCrane/iptest
