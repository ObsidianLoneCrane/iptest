# Changelog

## Unreleased

- Inspect local interfaces and public IPv4 egress by default, with optional IPv6.
- Select configured environment/system static proxies or the current system route
  without silently bypassing a failed selected proxy.
- Display estimated country/state/city and available third-party IP facts without
  an overall cleanliness judgment.
- Sample interface traffic and perform bounded embedded HTTP speed measurements
  with public-target failover or sequential comparison.
- Present grouped terminal reports and optional plain output.
- Prepare MIT-licensed Windows/Linux/macOS packages, version metadata, SHA-256
  manifests, and native CI checks for amd64/arm64.
- Keep persistent main/develop/release branches with reviewed candidate promotion.

No version has been published yet. Windows development and local package checks
have passed; first GitHub native CI runs and remaining desktop/browser-download
acceptance are pending. Android is not included; PAC evaluation is unsupported.
