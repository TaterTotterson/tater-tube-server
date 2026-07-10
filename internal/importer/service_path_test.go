package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/stretchr/testify/assert"
)

func TestCalculateProcessVirtualDir_FailedPath(t *testing.T) {
	// Setup
	s := &Service{
		configGetter: func() *config.Config {
			return &config.Config{
				Database: config.DatabaseConfig{
					Path: "/config/tater-tube-server.db",
				},
				SABnzbd: config.SABnzbdConfig{
					CompleteDir: "/mnt/remotes/tater-tube-server",
				},
			}
		},
	}

	tests := []struct {
		name         string
		nzbPath      string
		basePath     string
		category     string
		itemID       int64
		expectedPath string
	}{
		{
			name:         "normal nzb in root",
			nzbPath:      "/config/.nzbs/Movie.nzb",
			basePath:     "movies",
			expectedPath: "/mnt/remotes/tater-tube-server/movies",
		},
		{
			name:         "failed nzb in root",
			nzbPath:      "/config/.nzbs/failed/Movie.nzb",
			basePath:     "movies",
			expectedPath: "/mnt/remotes/tater-tube-server/movies",
		},
		{
			name:         "failed nzb in category subfolder",
			nzbPath:      "/config/.nzbs/failed/tv/Show.nzb",
			basePath:     "media",
			category:     "tv",
			expectedPath: "/mnt/remotes/tater-tube-server/media/tv",
		},
		{
			name:         "normal nzb in category subfolder",
			nzbPath:      "/config/.nzbs/tv/Show.nzb",
			basePath:     "media",
			category:     "tv",
			expectedPath: "/mnt/remotes/tater-tube-server/media/tv",
		},
		{
			name:         "no category nzb in watch dir subdirectory",
			nzbPath:      "/config/.nzbs/Show.S01E05.nzb",
			basePath:     "Plex_Media/Series/Show (2026)/Season 01",
			expectedPath: "/mnt/remotes/tater-tube-server/Plex_Media/Series/Show (2026)/Season 01",
		},
		{
			name:         "nzb in queue_id subfolder (no basePath)",
			nzbPath:      "/config/.nzbs/tv/22/Show.S01E01.nzb.gz",
			basePath:     "",
			category:     "tv",
			itemID:       22,
			expectedPath: "/mnt/remotes/tater-tube-server/tv",
		},
		{
			name:         "nzb in queue_id subfolder with basePath",
			nzbPath:      "/config/.nzbs/tv/22/Show.S01E01.nzb.gz",
			basePath:     "media",
			category:     "tv",
			itemID:       22,
			expectedPath: "/mnt/remotes/tater-tube-server/media/tv",
		},
		{
			name:         "failed nzb in queue_id subfolder",
			nzbPath:      "/config/.nzbs/failed/tv/22/Show.S01E01.nzb.gz",
			basePath:     "",
			category:     "tv",
			itemID:       22,
			expectedPath: "/mnt/remotes/tater-tube-server/tv",
		},
		{
			name:         "nzb in queue_id subfolder no category",
			nzbPath:      "/config/.nzbs/22/Show.S01E01.nzb.gz",
			basePath:     "",
			itemID:       22,
			expectedPath: "/mnt/remotes/tater-tube-server",
		},
		{
			// Production path after PR #717: ensurePersistentNzb stages the NZB in the
			// OS temp queue dir. The category must still be appended to the destination.
			name:         "temp queue nzb with category and basePath",
			nzbPath:      filepath.Join(os.TempDir(), ".tater-tube-server-queue", "22-Show.S01E01.nzb"),
			basePath:     "media",
			category:     "tv",
			itemID:       22,
			expectedPath: "/mnt/remotes/tater-tube-server/media/tv",
		},
		{
			name:         "temp queue nzb with category, basePath is CompleteDir",
			nzbPath:      filepath.Join(os.TempDir(), ".tater-tube-server-queue", "7-Movie.nzb"),
			basePath:     "/mnt/remotes/tater-tube-server",
			category:     "tv",
			itemID:       7,
			expectedPath: "/mnt/remotes/tater-tube-server/tv",
		},
		{
			name:         "temp queue nzb no category",
			nzbPath:      filepath.Join(os.TempDir(), ".tater-tube-server-queue", "9-Show.nzb"),
			basePath:     "media",
			itemID:       9,
			expectedPath: "/mnt/remotes/tater-tube-server/media",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := &database.ImportQueueItem{
				NzbPath: filepath.FromSlash(tt.nzbPath),
				ID:      tt.itemID,
			}
			if tt.category != "" {
				item.Category = &tt.category
			}
			basePath := tt.basePath

			result := s.calculateProcessVirtualDir(item, &basePath)

			// Normalize separators for comparison
			result = filepath.ToSlash(result)
			expected := filepath.ToSlash(tt.expectedPath)

			assert.Equal(t, expected, result)
		})
	}
}

func TestCalculateProcessVirtualDir_WindowsDriveLetterStripped(t *testing.T) {
	s := &Service{
		configGetter: func() *config.Config {
			return &config.Config{
				Database: config.DatabaseConfig{Path: "/config/tater-tube-server.db"},
				SABnzbd:  config.SABnzbdConfig{CompleteDir: "/complete"},
			}
		},
	}

	// Simulates the Windows CLI case where the watcher previously passed the
	// absolute watch directory (drive-lettered) as basePath. The virtual path
	// must NOT carry the drive letter or colon into the metadata layer.
	item := &database.ImportQueueItem{NzbPath: filepath.FromSlash("/config/.nzbs/test1.nzb")}
	basePath := `C:\rclone\tater-tube-server\nzb`

	got := s.calculateProcessVirtualDir(item, &basePath)

	assert.NotContains(t, got, ":", "virtual path must not leak Windows drive letters")
	assert.Equal(t, "/complete/rclone/tater-tube-server/nzb", filepath.ToSlash(got))
}

func TestSanitizeVirtualPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/complete/foo/bar", "/complete/foo/bar"},
		{`C:\rclone\tater-tube-server\nzb`, "/rclone/tater-tube-server/nzb"},
		{"/C:/rclone/tater-tube-server/nzb", "/rclone/tater-tube-server/nzb"},
		{"/complete/C:/x", "/complete/x"},
		{"plain/no/slash", "/plain/no/slash"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, tc.want, sanitizeVirtualPath(tc.in))
		})
	}
}
