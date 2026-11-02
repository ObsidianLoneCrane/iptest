// Package reputation retrieves provider facts about an observed egress IP.
package reputation

// Detections retains source booleans; missing or invalid flags are omitted.
type Detections struct {
	Compromised *bool `json:"compromised"`
	Proxy       *bool `json:"proxy"`
	VPN         *bool `json:"vpn"`
	Tor         *bool `json:"tor"`
	Hosting     *bool `json:"hosting"`
	Scraper     *bool `json:"scraper"`
}

// Report contains the available provider facts. EstimatedDevices refers to the
// requested address, never the whole subnet. Nil estimates and flags are absent.
// History retains valid individual counts. Nil history supplies no counts;
// an empty non-nil history represents an explicitly empty provider history.
// Warning records a usable response carrying a provider warning, without
// retaining or displaying untrusted provider prose.
type Report struct {
	EstimatedDevices *int64
	NetworkType      string
	Detections       Detections
	AttackHistory    map[string]int64
	Warning          bool
}
