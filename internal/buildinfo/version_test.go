package buildinfo

import "testing"

func TestDevelopmentAndReleaseMetadata(t *testing.T) {
	version, commit, date := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = version, commit, date })
	Version, Commit, Date = "dev", "unknown", "unknown"
	if got := String(); got != "iptest dev" {
		t.Fatalf("development version=%q", got)
	}
	Version, Commit, Date = "v0.1.0-rc.1", "abc123", "2026-11-01T00:00:00Z"
	if got := String(); got != "iptest v0.1.0-rc.1 (commit abc123; built 2026-11-01T00:00:00Z)" {
		t.Fatalf("release version=%q", got)
	}
}
