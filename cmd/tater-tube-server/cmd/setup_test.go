package cmd

import "testing"

func TestIsDirectFileStreamPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "stream endpoint", path: "/api/files/stream", want: true},
		{name: "stream subpath", path: "/api/files/stream/movie.mkv", want: true},
		{name: "activity history", path: "/api/files/streams/history", want: false},
		{name: "active streams", path: "/api/files/active-streams", want: false},
		{name: "similar endpoint", path: "/api/files/stream-info", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDirectFileStreamPath(test.path); got != test.want {
				t.Fatalf("isDirectFileStreamPath(%q) = %v, want %v", test.path, got, test.want)
			}
		})
	}
}
