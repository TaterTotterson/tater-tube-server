package registrar

import "testing"

func TestIsTaterTubeServerDownloadClient(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{TaterTubeServerDownloadClientName, true}, // exact registered name
		{"tater-tube-server", true},               // slug
		{"Tater Tube Server (SABnzbd)", true},
		{"My Tater Tube Server SAB", true},
		{"", false},
		{"qBittorrent", false},
		{"SABnzbd", false},
		{"NZBGet", false},
	}
	for _, tt := range tests {
		if got := IsTaterTubeServerDownloadClient(tt.name); got != tt.want {
			t.Errorf("IsTaterTubeServerDownloadClient(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
