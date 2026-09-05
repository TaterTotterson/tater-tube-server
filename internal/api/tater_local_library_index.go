package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const taterLocalLibraryIndexSchema = 4

type taterLocalLibraryStats struct {
	Files           int   `json:"files"`
	Movies          int   `json:"movies"`
	Shows           int   `json:"shows"`
	Episodes        int   `json:"episodes"`
	Artists         int   `json:"artists"`
	Albums          int   `json:"albums"`
	Songs           int   `json:"songs"`
	Artwork         int   `json:"artwork"`
	MissingArtwork  int   `json:"missing_artwork"`
	Metadata        int   `json:"metadata"`
	MissingMetadata int   `json:"missing_metadata"`
	Errors          int   `json:"errors"`
	SizeBytes       int64 `json:"size_bytes"`
}

type taterLocalLibraryCategoryIndex struct {
	ID          string                 `json:"id"`
	Name        string                 `json:"name"`
	LibraryType string                 `json:"library_type"`
	Paths       []string               `json:"paths"`
	Enabled     bool                   `json:"enabled"`
	Stats       taterLocalLibraryStats `json:"stats"`
	Errors      []string               `json:"errors,omitempty"`
}

type taterLocalLibraryFileIndex struct {
	Key                string   `json:"key"`
	CategoryID         string   `json:"category_id"`
	LibraryType        string   `json:"library_type"`
	SourceIndex        int      `json:"source_index"`
	Path               string   `json:"path"`
	SizeBytes          int64    `json:"size_bytes"`
	ModifiedUnix       int64    `json:"modified_unix"`
	ModifiedUnixNano   int64    `json:"modified_unix_nano"`
	Title              string   `json:"title,omitempty"`
	Artist             string   `json:"artist,omitempty"`
	AlbumArtist        string   `json:"album_artist,omitempty"`
	Album              string   `json:"album,omitempty"`
	Genres             []string `json:"genres,omitempty"`
	Year               string   `json:"year,omitempty"`
	Description        string   `json:"description,omitempty"`
	OriginalTitle      string   `json:"original_title,omitempty"`
	Tagline            string   `json:"tagline,omitempty"`
	ContentRating      string   `json:"content_rating,omitempty"`
	CommunityRating    float64  `json:"community_rating,omitempty"`
	Studios            []string `json:"studios,omitempty"`
	Countries          []string `json:"countries,omitempty"`
	Actors             []string `json:"actors,omitempty"`
	Directors          []string `json:"directors,omitempty"`
	Writers            []string `json:"writers,omitempty"`
	IMDbID             string   `json:"imdb_id,omitempty"`
	TMDBID             int64    `json:"tmdb_id,omitempty"`
	TVDBID             int64    `json:"tvdb_id,omitempty"`
	Track              int      `json:"track,omitempty"`
	Disc               int      `json:"disc,omitempty"`
	DurationSeconds    float64  `json:"duration_seconds,omitempty"`
	HasEmbeddedArtwork bool     `json:"has_embedded_artwork,omitempty"`
}

type taterLocalMusicAlbumIndex struct {
	ID                      string   `json:"id"`
	CategoryID              string   `json:"category_id"`
	CategoryName            string   `json:"category_name"`
	SourceIndex             int      `json:"source_index"`
	Path                    string   `json:"path"`
	Title                   string   `json:"title"`
	Artist                  string   `json:"artist"`
	AlbumArtist             string   `json:"album_artist,omitempty"`
	Description             string   `json:"description,omitempty"`
	Genres                  []string `json:"genres,omitempty"`
	Styles                  []string `json:"styles,omitempty"`
	Year                    string   `json:"year,omitempty"`
	TrackCount              int      `json:"track_count"`
	DiscCount               int      `json:"disc_count"`
	SizeBytes               int64    `json:"size_bytes"`
	ModifiedUnix            int64    `json:"modified_unix"`
	HasArtwork              bool     `json:"has_artwork"`
	ArtworkSource           string   `json:"artwork_source,omitempty"`
	ArtworkRef              string   `json:"artwork_ref,omitempty"`
	ArtworkURL              string   `json:"artwork_url,omitempty"`
	ArtworkLocked           bool     `json:"artwork_locked"`
	MusicBrainzID           string   `json:"musicbrainz_id,omitempty"`
	MusicBrainzArtistID     string   `json:"musicbrainz_artist_id,omitempty"`
	ArtworkUpdated          int64    `json:"artwork_updated,omitempty"`
	ArtworkStorage          string   `json:"artwork_storage,omitempty"`
	MetadataAvailable       bool     `json:"metadata_available"`
	HasMetadata             bool     `json:"has_metadata"`
	MetadataSource          string   `json:"metadata_source,omitempty"`
	NFORef                  string   `json:"nfo_ref,omitempty"`
	ArtistMetadataAvailable bool     `json:"artist_metadata_available"`
	HasArtistMetadata       bool     `json:"has_artist_metadata"`
	ArtistNFORef            string   `json:"artist_nfo_ref,omitempty"`
}

type taterLocalVideoIndex struct {
	ID              string   `json:"id"`
	CategoryID      string   `json:"category_id"`
	CategoryName    string   `json:"category_name"`
	LibraryType     string   `json:"library_type"`
	MediaType       string   `json:"media_type"`
	SourceIndex     int      `json:"source_index"`
	Path            string   `json:"path"`
	Title           string   `json:"title"`
	Year            string   `json:"year,omitempty"`
	Description     string   `json:"description,omitempty"`
	OriginalTitle   string   `json:"original_title,omitempty"`
	Tagline         string   `json:"tagline,omitempty"`
	ContentRating   string   `json:"content_rating,omitempty"`
	CommunityRating float64  `json:"community_rating,omitempty"`
	Genres          []string `json:"genres,omitempty"`
	Studios         []string `json:"studios,omitempty"`
	Countries       []string `json:"countries,omitempty"`
	Actors          []string `json:"actors,omitempty"`
	Directors       []string `json:"directors,omitempty"`
	Writers         []string `json:"writers,omitempty"`
	IMDbID          string   `json:"imdb_id,omitempty"`
	TVDBID          int64    `json:"tvdb_id,omitempty"`
	SizeBytes       int64    `json:"size_bytes"`
	ModifiedUnix    int64    `json:"modified_unix"`
	HasArtwork      bool     `json:"has_artwork"`
	ArtworkSource   string   `json:"artwork_source,omitempty"`
	ArtworkRef      string   `json:"artwork_ref,omitempty"`
	ArtworkURL      string   `json:"artwork_url,omitempty"`
	ArtworkLocked   bool     `json:"artwork_locked"`
	TMDBID          int64    `json:"tmdb_id,omitempty"`
	ArtworkUpdated  int64    `json:"artwork_updated,omitempty"`
	HasMetadata     bool     `json:"has_metadata"`
	MetadataSource  string   `json:"metadata_source,omitempty"`
	NFORef          string   `json:"nfo_ref,omitempty"`
}

type taterLocalLibraryIndex struct {
	Schema                int                              `json:"schema"`
	ConfigFingerprint     string                           `json:"config_fingerprint"`
	GeneratedAt           time.Time                        `json:"generated_at"`
	VideoDurationsScanned bool                             `json:"video_durations_scanned,omitempty"`
	Categories            []taterLocalLibraryCategoryIndex `json:"categories"`
	Albums                []taterLocalMusicAlbumIndex      `json:"albums"`
	Videos                []taterLocalVideoIndex           `json:"videos"`
	Files                 []taterLocalLibraryFileIndex     `json:"files"`
}

type taterMusicArtworkOverride struct {
	AlbumID             string    `json:"album_id"`
	Source              string    `json:"source"`
	Ref                 string    `json:"ref"`
	ContentType         string    `json:"content_type,omitempty"`
	MusicBrainzID       string    `json:"musicbrainz_id,omitempty"`
	MusicBrainzArtistID string    `json:"musicbrainz_artist_id,omitempty"`
	Genres              []string  `json:"genres,omitempty"`
	Locked              bool      `json:"locked"`
	UpdatedAt           time.Time `json:"updated_at"`
	Storage             string    `json:"storage,omitempty"`
}

type taterMusicArtworkStore struct {
	Schema int                                  `json:"schema"`
	Items  map[string]taterMusicArtworkOverride `json:"items"`
}

type taterLocalLibraryScanStatus struct {
	Running         bool      `json:"running"`
	Phase           string    `json:"phase"`
	Message         string    `json:"message,omitempty"`
	StartedAt       time.Time `json:"started_at,omitempty"`
	FinishedAt      time.Time `json:"finished_at,omitempty"`
	ProgressCurrent int       `json:"progress_current"`
	ProgressTotal   int       `json:"progress_total"`
	ProgressPercent int       `json:"progress_percent"`
	FilesScanned    int       `json:"files_scanned"`
	FilesTotal      int       `json:"files_total"`
	AlbumsProcessed int       `json:"albums_processed"`
	AlbumsTotal     int       `json:"albums_total"`
	VideosProcessed int       `json:"videos_processed"`
	VideosTotal     int       `json:"videos_total"`
	ArtworkFound    int       `json:"artwork_found"`
	MetadataFound   int       `json:"metadata_found"`
	GenreMatches    int       `json:"genre_matches"`
	GenreUnmatched  int       `json:"genre_unmatched"`
	Error           string    `json:"error,omitempty"`
}

type taterLocalLibraryScanProgress struct {
	Phase        string
	Message      string
	FilesScanned int
	FilesTotal   int
	Current      int
	Total        int
}

type taterLocalLibraryScanRequest struct {
	ScrapeMissingArtwork bool   `json:"scrape_missing_artwork"`
	ArtworkLibraryType   string `json:"artwork_library_type,omitempty"`
}

type taterLocalDurationProbeJob struct {
	FileIndex int
	Path      string
}

type taterLocalDurationProbeResult struct {
	FileIndex       int
	DurationSeconds float64
}

type taterLocalMusicArtworkRequest struct {
	AlbumID string `json:"album_id"`
	Force   bool   `json:"force"`
	Locked  *bool  `json:"locked,omitempty"`
}

type taterLocalVideoArtworkRequest struct {
	MediaID string `json:"media_id"`
	Force   bool   `json:"force"`
}

var taterLocalLibraryScans = struct {
	sync.RWMutex
	Items map[string]taterLocalLibraryScanStatus
}{Items: map[string]taterLocalLibraryScanStatus{}}

func taterLocalLibraryDataDir(cfg *config.Config) string {
	root := ""
	if cfg != nil {
		root = strings.TrimSpace(cfg.Metadata.RootPath)
	}
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Join(filepath.Clean(root), ".tater-tube", "local-media")
}

func taterLocalLibraryIndexPath(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "library-index.json")
}

func taterMusicArtworkStorePath(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "music-artwork.json")
}

func taterMusicArtworkCacheDir(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "music-artwork")
}

func taterLocalLibraryFingerprint(cfg *config.Config) string {
	type fingerprintCategory struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		LibraryType string   `json:"library_type"`
		Paths       []string `json:"paths"`
		Enabled     bool     `json:"enabled"`
	}
	payload := struct {
		Enabled    bool                  `json:"enabled"`
		FFmpegPath string                `json:"ffmpeg_path"`
		Categories []fingerprintCategory `json:"categories"`
	}{}
	if cfg != nil {
		payload.Enabled = cfg.LocalMedia.Enabled != nil && *cfg.LocalMedia.Enabled
		payload.FFmpegPath = strings.TrimSpace(cfg.Transcoding.FFmpegPath)
		for _, cat := range cfg.LocalMedia.Categories {
			payload.Categories = append(payload.Categories, fingerprintCategory{
				ID:          strings.TrimSpace(cat.ID),
				Name:        strings.TrimSpace(cat.Name),
				LibraryType: strings.ToLower(strings.TrimSpace(cat.LibraryType)),
				Paths:       taterLocalMediaCategoryPaths(cat),
				Enabled:     cat.Enabled == nil || *cat.Enabled,
			})
		}
	}
	raw, _ := json.Marshal(payload)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:12])
}

func readTaterLocalLibraryIndex(cfg *config.Config) (taterLocalLibraryIndex, error) {
	var index taterLocalLibraryIndex
	raw, err := os.ReadFile(taterLocalLibraryIndexPath(cfg))
	if err != nil {
		return index, err
	}
	if err := json.Unmarshal(raw, &index); err != nil {
		return taterLocalLibraryIndex{}, err
	}
	if index.Schema != taterLocalLibraryIndexSchema {
		return taterLocalLibraryIndex{}, fmt.Errorf("unsupported local library index schema %d", index.Schema)
	}
	if index.Categories == nil {
		index.Categories = []taterLocalLibraryCategoryIndex{}
	}
	if index.Albums == nil {
		index.Albums = []taterLocalMusicAlbumIndex{}
	}
	if index.Videos == nil {
		index.Videos = []taterLocalVideoIndex{}
	}
	if index.Files == nil {
		index.Files = []taterLocalLibraryFileIndex{}
	}
	return index, nil
}

func writeTaterJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tater-local-media-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func readTaterMusicArtworkStore(cfg *config.Config) taterMusicArtworkStore {
	store := taterMusicArtworkStore{Schema: 1, Items: map[string]taterMusicArtworkOverride{}}
	raw, err := os.ReadFile(taterMusicArtworkStorePath(cfg))
	if err != nil {
		return store
	}
	if err := json.Unmarshal(raw, &store); err != nil || store.Items == nil {
		return taterMusicArtworkStore{Schema: 1, Items: map[string]taterMusicArtworkOverride{}}
	}
	store.Schema = 1
	return store
}

func writeTaterMusicArtworkStore(cfg *config.Config, store taterMusicArtworkStore) error {
	store.Schema = 1
	if store.Items == nil {
		store.Items = map[string]taterMusicArtworkOverride{}
	}
	return writeTaterJSON(taterMusicArtworkStorePath(cfg), store)
}

func taterLocalLibraryScanKey(cfg *config.Config) string {
	return taterLocalLibraryIndexPath(cfg)
}

func getTaterLocalLibraryScanStatus(cfg *config.Config) taterLocalLibraryScanStatus {
	key := taterLocalLibraryScanKey(cfg)
	taterLocalLibraryScans.RLock()
	status := taterLocalLibraryScans.Items[key]
	taterLocalLibraryScans.RUnlock()
	return status
}

func updateTaterLocalLibraryScanStatus(cfg *config.Config, update func(*taterLocalLibraryScanStatus)) {
	key := taterLocalLibraryScanKey(cfg)
	taterLocalLibraryScans.Lock()
	status := taterLocalLibraryScans.Items[key]
	update(&status)
	taterLocalLibraryScans.Items[key] = status
	taterLocalLibraryScans.Unlock()
}

func setTaterLocalLibraryProgress(status *taterLocalLibraryScanStatus, current, total int) {
	if status == nil {
		return
	}
	status.ProgressCurrent = max(0, current)
	status.ProgressTotal = max(0, total)
	status.ProgressPercent = 0
	if status.ProgressTotal > 0 {
		status.ProgressCurrent = min(status.ProgressCurrent, status.ProgressTotal)
		status.ProgressPercent = min(99, (status.ProgressCurrent*100)/status.ProgressTotal)
	}
}

func taterLocalLibraryTypeSupportsFile(libraryType, path string) bool {
	ext := filepath.Ext(path)
	switch strings.ToLower(strings.TrimSpace(libraryType)) {
	case "music":
		return isAudioExtension(ext)
	case "movies", "tv", "folders":
		return isMediaExtension(ext)
	default:
		return false
	}
}

func taterLocalLibraryFileKey(categoryID string, sourceIndex int, relPath string) string {
	return strings.TrimSpace(categoryID) + "\x00" + fmt.Sprintf("%d", sourceIndex) + "\x00" + cleanLocalRelativePath(relPath)
}

func taterLocalLibraryEnabled(cat config.LocalMediaCategory) bool {
	return cat.Enabled == nil || *cat.Enabled
}

func countTaterLocalLibraryFiles(ctx context.Context, cfg *config.Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("configuration is unavailable")
	}
	total := 0
	for _, cat := range cfg.LocalMedia.Categories {
		if !taterLocalLibraryEnabled(cat) {
			continue
		}
		libraryType := strings.ToLower(strings.TrimSpace(cat.LibraryType))
		for _, configuredRoot := range taterLocalMediaCategoryPaths(cat) {
			root := filepath.Clean(configuredRoot)
			info, err := os.Stat(root)
			if err != nil || info == nil || !info.IsDir() {
				continue
			}
			err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					return nil
				}
				if path != root && strings.HasPrefix(entry.Name(), ".") {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if !entry.IsDir() && taterLocalLibraryTypeSupportsFile(libraryType, path) {
					total++
				}
				return nil
			})
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}

func scanTaterLocalLibrary(
	ctx context.Context,
	cfg *config.Config,
	previous taterLocalLibraryIndex,
	progress func(taterLocalLibraryScanProgress),
) (taterLocalLibraryIndex, error) {
	index := taterLocalLibraryIndex{
		Schema:                taterLocalLibraryIndexSchema,
		ConfigFingerprint:     taterLocalLibraryFingerprint(cfg),
		GeneratedAt:           time.Now().UTC(),
		VideoDurationsScanned: true,
		Categories:            []taterLocalLibraryCategoryIndex{},
		Albums:                []taterLocalMusicAlbumIndex{},
		Videos:                []taterLocalVideoIndex{},
		Files:                 []taterLocalLibraryFileIndex{},
	}
	if cfg == nil {
		return index, fmt.Errorf("configuration is unavailable")
	}
	if progress != nil {
		progress(taterLocalLibraryScanProgress{
			Phase: "discovering", Message: "Counting local media files",
		})
	}
	totalFiles, err := countTaterLocalLibraryFiles(ctx, cfg)
	if err != nil {
		return index, err
	}
	if progress != nil {
		progress(taterLocalLibraryScanProgress{
			Phase: "scanning", Message: "Scanning local media libraries", FilesTotal: totalFiles,
			Total: totalFiles,
		})
	}
	previousFiles := make(map[string]taterLocalLibraryFileIndex, len(previous.Files))
	for _, file := range previous.Files {
		previousFiles[file.Key] = file
	}
	filesScanned := 0
	durationJobs := []taterLocalDurationProbeJob{}
	for _, cat := range cfg.LocalMedia.Categories {
		if err := ctx.Err(); err != nil {
			return index, err
		}
		category := taterLocalLibraryCategoryIndex{
			ID:          strings.TrimSpace(cat.ID),
			Name:        strings.TrimSpace(cat.Name),
			LibraryType: strings.ToLower(strings.TrimSpace(cat.LibraryType)),
			Paths:       append([]string(nil), taterLocalMediaCategoryPaths(cat)...),
			Enabled:     taterLocalLibraryEnabled(cat),
			Errors:      []string{},
		}
		if !category.Enabled {
			index.Categories = append(index.Categories, category)
			continue
		}
		for sourceIndex, root := range category.Paths {
			root = filepath.Clean(root)
			if info, err := os.Stat(root); err != nil || info == nil || !info.IsDir() {
				category.Stats.Errors++
				category.Errors = append(category.Errors, fmt.Sprintf("%s is not readable", root))
				continue
			}
			err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if walkErr != nil {
					category.Stats.Errors++
					return nil
				}
				if path != root && strings.HasPrefix(entry.Name(), ".") {
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.IsDir() || !taterLocalLibraryTypeSupportsFile(category.LibraryType, path) {
					return nil
				}
				info, err := entry.Info()
				if err != nil || info == nil {
					category.Stats.Errors++
					return nil
				}
				rel, err := filepath.Rel(root, path)
				if err != nil {
					category.Stats.Errors++
					return nil
				}
				rel = cleanLocalRelativePath(filepath.ToSlash(rel))
				key := taterLocalLibraryFileKey(category.ID, sourceIndex, rel)
				file := previousFiles[key]
				unchanged := file.Key == key && file.SizeBytes == info.Size() &&
					file.ModifiedUnixNano == info.ModTime().UnixNano()
				if !unchanged {
					file = taterLocalLibraryFileIndex{
						Key:              key,
						CategoryID:       category.ID,
						LibraryType:      category.LibraryType,
						SourceIndex:      sourceIndex,
						Path:             rel,
						SizeBytes:        info.Size(),
						ModifiedUnix:     info.ModTime().Unix(),
						ModifiedUnixNano: info.ModTime().UnixNano(),
					}
					if category.LibraryType == "music" {
						metadata := taterLocalMusicMetadataForPath(cfg, path)
						file.Title = metadata.Title
						file.Artist = metadata.Artist
						file.AlbumArtist = metadata.AlbumArtist
						file.Album = metadata.Album
						file.Genres = append([]string(nil), metadata.Genres...)
						file.Year = metadata.Year
						file.Track = metadata.Track
						file.Disc = metadata.Disc
						file.DurationSeconds = metadata.Duration
						file.HasEmbeddedArtwork = metadata.HasArtwork
					}
				}
				// NFO sidecars can change without changing the video itself, so refresh
				// their small metadata payload even when the indexed media file is unchanged.
				if category.LibraryType != "music" {
					mediaType := "movie"
					if category.LibraryType == "tv" {
						mediaType = "episode"
					}
					item := taterUsenetItem{MediaType: mediaType}
					taterApplyLocalMetadata(path, &item)
					file.Title = cleanTaterText(item.Title)
					file.Year = strings.TrimSpace(item.Date)
					file.Genres = append([]string(nil), item.Genres...)
					file.Description = cleanTaterText(item.Description)
					file.OriginalTitle = cleanTaterText(item.OriginalTitle)
					file.Tagline = cleanTaterText(item.Tagline)
					file.ContentRating = cleanTaterText(item.ContentRating)
					file.CommunityRating = item.CommunityRating
					file.Studios = append([]string(nil), item.Studios...)
					file.Countries = append([]string(nil), item.Countries...)
					file.Actors = append([]string(nil), item.Actors...)
					file.Directors = append([]string(nil), item.Directors...)
					file.Writers = append([]string(nil), item.Writers...)
					file.IMDbID = strings.TrimSpace(item.IMDbID)
					file.TMDBID = item.TMDBID
					file.TVDBID = item.TVDBID
				}
				index.Files = append(index.Files, file)
				if category.LibraryType != "music" && file.DurationSeconds <= 0 {
					durationJobs = append(durationJobs, taterLocalDurationProbeJob{
						FileIndex: len(index.Files) - 1,
						Path:      path,
					})
				}
				filesScanned++
				if progress != nil && (filesScanned%25 == 0 || filesScanned == totalFiles) {
					progress(taterLocalLibraryScanProgress{
						Phase: "scanning", Message: "Scanning " + category.Name,
						FilesScanned: filesScanned, FilesTotal: totalFiles,
						Current: filesScanned, Total: totalFiles,
					})
				}
				return nil
			})
			if err != nil {
				return index, err
			}
		}
		index.Categories = append(index.Categories, category)
	}
	if err := probeTaterLocalLibraryDurations(
		ctx, cfg, &index, durationJobs, filesScanned, totalFiles, progress,
	); err != nil {
		return index, err
	}
	buildTaterLocalLibraryStats(cfg, &index)
	sort.SliceStable(index.Categories, func(i, j int) bool {
		if index.Categories[i].LibraryType != index.Categories[j].LibraryType {
			return index.Categories[i].LibraryType < index.Categories[j].LibraryType
		}
		return strings.ToLower(index.Categories[i].Name) < strings.ToLower(index.Categories[j].Name)
	})
	return index, nil
}

func probeTaterLocalLibraryDurations(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	jobs []taterLocalDurationProbeJob,
	filesScanned int,
	filesTotal int,
	progress func(taterLocalLibraryScanProgress),
) error {
	if len(jobs) == 0 || index == nil {
		return ctx.Err()
	}

	workerCount := min(4, len(jobs))
	jobQueue := make(chan taterLocalDurationProbeJob)
	results := make(chan taterLocalDurationProbeResult)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for job := range jobQueue {
				duration := probeMediaDurationSeconds(ctx, cfg.Transcoding.FFmpegPath, job.Path)
				select {
				case results <- taterLocalDurationProbeResult{
					FileIndex: job.FileIndex, DurationSeconds: duration,
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobQueue)
		for _, job := range jobs {
			select {
			case jobQueue <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	completed := 0
	for result := range results {
		if result.FileIndex >= 0 && result.FileIndex < len(index.Files) {
			index.Files[result.FileIndex].DurationSeconds = result.DurationSeconds
		}
		completed++
		if progress != nil && (completed%10 == 0 || completed == len(jobs)) {
			progress(taterLocalLibraryScanProgress{
				Phase: "durations", Message: fmt.Sprintf(
					"Reading video durations (%d/%d)", completed, len(jobs),
				),
				FilesScanned: filesScanned, FilesTotal: filesTotal,
				Current: completed, Total: len(jobs),
			})
		}
	}
	return ctx.Err()
}

func buildTaterLocalLibraryStats(cfg *config.Config, index *taterLocalLibraryIndex) {
	if index == nil {
		return
	}
	categoryByID := map[string]*taterLocalLibraryCategoryIndex{}
	for i := range index.Categories {
		index.Categories[i].Stats.Files = 0
		index.Categories[i].Stats.Movies = 0
		index.Categories[i].Stats.Shows = 0
		index.Categories[i].Stats.Episodes = 0
		index.Categories[i].Stats.Artists = 0
		index.Categories[i].Stats.Albums = 0
		index.Categories[i].Stats.Songs = 0
		index.Categories[i].Stats.Artwork = 0
		index.Categories[i].Stats.MissingArtwork = 0
		index.Categories[i].Stats.Metadata = 0
		index.Categories[i].Stats.MissingMetadata = 0
		index.Categories[i].Stats.SizeBytes = 0
		categoryByID[index.Categories[i].ID] = &index.Categories[i]
	}
	categoryConfig := map[string]config.LocalMediaCategory{}
	if cfg != nil {
		for _, cat := range cfg.LocalMedia.Categories {
			categoryConfig[strings.TrimSpace(cat.ID)] = cat
		}
	}
	showSets := map[string]map[string]bool{}
	artistSets := map[string]map[string]bool{}
	type albumBuild struct {
		Album taterLocalMusicAlbumIndex
		Discs map[int]bool
	}
	albums := map[string]*albumBuild{}
	for _, file := range index.Files {
		category := categoryByID[file.CategoryID]
		if category == nil {
			continue
		}
		category.Stats.Files++
		category.Stats.SizeBytes += file.SizeBytes
		switch file.LibraryType {
		case "movies":
			category.Stats.Movies++
		case "tv":
			category.Stats.Episodes++
			showName := strings.Split(cleanLocalRelativePath(file.Path), "/")[0]
			if showSets[file.CategoryID] == nil {
				showSets[file.CategoryID] = map[string]bool{}
			}
			if showName != "" {
				showSets[file.CategoryID][strings.ToLower(showName)] = true
			}
		case "music":
			category.Stats.Songs++
			albumRel := cleanLocalRelativePath(filepath.ToSlash(filepath.Dir(file.Path)))
			albumID := taterMusicAlbumID(file.CategoryID, file.SourceIndex, albumRel)
			build := albums[albumID]
			if build == nil {
				title, artist := localMusicAlbumTitle(category.Name, albumRel)
				build = &albumBuild{Album: taterLocalMusicAlbumIndex{
					ID:           albumID,
					CategoryID:   file.CategoryID,
					CategoryName: category.Name,
					SourceIndex:  file.SourceIndex,
					Path:         albumRel,
					Title:        title,
					Artist:       artist,
				}, Discs: map[int]bool{}}
				albums[albumID] = build
			}
			album := &build.Album
			if file.Album != "" {
				album.Title = file.Album
			}
			if file.AlbumArtist != "" {
				album.AlbumArtist = file.AlbumArtist
				album.Artist = file.AlbumArtist
			} else if file.Artist != "" && album.AlbumArtist == "" {
				album.Artist = file.Artist
			}
			if album.Year == "" && file.Year != "" {
				album.Year = file.Year
			}
			album.Genres = mergeTaterMusicGenres(album.Genres, file.Genres)
			album.TrackCount++
			album.SizeBytes += file.SizeBytes
			if file.ModifiedUnix > album.ModifiedUnix {
				album.ModifiedUnix = file.ModifiedUnix
			}
			if file.Disc > 0 {
				build.Discs[file.Disc] = true
			}
			if file.HasEmbeddedArtwork && album.ArtworkSource == "" {
				album.ArtworkSource = "embedded"
				album.ArtworkRef = file.Path
			}
		}
	}
	for categoryID, shows := range showSets {
		if category := categoryByID[categoryID]; category != nil {
			category.Stats.Shows = len(shows)
		}
	}
	index.Videos = buildTaterLocalVideoArtworkIndex(cfg, index.Files, index.Categories)
	for _, video := range index.Videos {
		if category := categoryByID[video.CategoryID]; category != nil {
			if video.HasArtwork {
				category.Stats.Artwork++
			} else {
				category.Stats.MissingArtwork++
			}
			if video.HasMetadata {
				category.Stats.Metadata++
			} else {
				category.Stats.MissingMetadata++
			}
		}
	}
	overrides := readTaterMusicArtworkStore(cfg)
	index.Albums = make([]taterLocalMusicAlbumIndex, 0, len(albums))
	for _, build := range albums {
		album := build.Album
		album.DiscCount = len(build.Discs)
		if album.DiscCount == 0 && album.TrackCount > 0 {
			album.DiscCount = 1
		}
		cat := categoryConfig[album.CategoryID]
		override := overrides.Items[album.ID]
		if override.AlbumID == album.ID {
			album.Genres = mergeTaterMusicGenres(album.Genres, override.Genres)
			album.MusicBrainzArtistID = strings.TrimSpace(override.MusicBrainzArtistID)
		}
		applyTaterMusicAlbumArtwork(cfg, cat, &album, override)
		applyTaterLocalMusicNFO(cfg, &album)
		album.ArtworkURL = taterLocalMusicAdminArtworkURL(album)
		if category := categoryByID[album.CategoryID]; category != nil {
			category.Stats.Albums++
			if album.HasArtwork {
				category.Stats.Artwork++
			} else {
				category.Stats.MissingArtwork++
			}
			if album.MetadataAvailable {
				if album.HasMetadata && (!album.ArtistMetadataAvailable || album.HasArtistMetadata) {
					category.Stats.Metadata++
				} else {
					category.Stats.MissingMetadata++
				}
			}
			if artistSets[album.CategoryID] == nil {
				artistSets[album.CategoryID] = map[string]bool{}
			}
			if artist := strings.ToLower(strings.TrimSpace(album.Artist)); artist != "" {
				artistSets[album.CategoryID][artist] = true
			}
		}
		index.Albums = append(index.Albums, album)
	}
	for categoryID, artists := range artistSets {
		if category := categoryByID[categoryID]; category != nil {
			category.Stats.Artists = len(artists)
		}
	}
	sort.SliceStable(index.Albums, func(i, j int) bool {
		left := strings.ToLower(index.Albums[i].Artist + "\x00" + index.Albums[i].Title)
		right := strings.ToLower(index.Albums[j].Artist + "\x00" + index.Albums[j].Title)
		return left < right
	})
}

func applyTaterMusicAlbumArtwork(
	cfg *config.Config,
	cat config.LocalMediaCategory,
	album *taterLocalMusicAlbumIndex,
	override taterMusicArtworkOverride,
) {
	if album == nil {
		return
	}
	album.HasArtwork = false
	album.ArtworkLocked = false
	album.MusicBrainzID = ""
	album.MusicBrainzArtistID = ""
	album.ArtworkUpdated = 0
	if override.AlbumID == album.ID {
		album.MusicBrainzID = strings.TrimSpace(override.MusicBrainzID)
		album.MusicBrainzArtistID = strings.TrimSpace(override.MusicBrainzArtistID)
	}
	validOverride := override.AlbumID == album.ID && taterMusicArtworkOverrideExists(cfg, cat, override)
	if validOverride && override.Locked {
		setTaterMusicAlbumArtwork(album, override.Source, override.Ref, true, override.MusicBrainzID, override.UpdatedAt)
		album.ArtworkStorage = override.Storage
		return
	}
	if album.ArtworkSource == "embedded" && album.ArtworkRef != "" {
		album.HasArtwork = true
		album.ArtworkStorage = "library"
	}
	if !album.HasArtwork {
		if localRef := findTaterLocalAlbumArtwork(cat, album.SourceIndex, album.Path); localRef != "" {
			setTaterMusicAlbumArtwork(album, "local", localRef, false, "", time.Time{})
			album.ArtworkStorage = "library"
		}
	}
	if !album.HasArtwork && validOverride {
		setTaterMusicAlbumArtwork(album, override.Source, override.Ref, override.Locked, override.MusicBrainzID, override.UpdatedAt)
		album.ArtworkStorage = override.Storage
	}
}

func setTaterMusicAlbumArtwork(
	album *taterLocalMusicAlbumIndex,
	source, ref string,
	locked bool,
	mbid string,
	updated time.Time,
) {
	album.HasArtwork = strings.TrimSpace(source) != "" && strings.TrimSpace(ref) != ""
	album.ArtworkSource = strings.TrimSpace(source)
	album.ArtworkRef = strings.TrimSpace(ref)
	album.ArtworkLocked = locked
	album.MusicBrainzID = strings.TrimSpace(mbid)
	album.ArtworkStorage = ""
	if !updated.IsZero() {
		album.ArtworkUpdated = updated.Unix()
	}
}

func taterMusicArtworkOverrideExists(
	cfg *config.Config,
	cat config.LocalMediaCategory,
	override taterMusicArtworkOverride,
) bool {
	switch override.Source {
	case "scraped", "manual":
		if override.Storage == "library" {
			for _, root := range taterLocalMediaCategoryPaths(cat) {
				path, err := safeLocalPath(root, override.Ref)
				if err == nil {
					if info, statErr := os.Stat(path); statErr == nil && info != nil && !info.IsDir() {
						return true
					}
				}
			}
			return false
		}
		path, err := safeTaterMusicArtworkCachePath(cfg, override.Ref)
		if err != nil {
			return false
		}
		info, err := os.Stat(path)
		return err == nil && info != nil && !info.IsDir()
	case "embedded", "local":
		paths := taterLocalMediaCategoryPaths(cat)
		if override.Ref == "" || len(paths) == 0 {
			return false
		}
		for _, root := range paths {
			path, err := safeLocalPath(root, override.Ref)
			if err == nil {
				if info, statErr := os.Stat(path); statErr == nil && info != nil && !info.IsDir() {
					return true
				}
			}
		}
	}
	return false
}

func findTaterLocalAlbumArtwork(cat config.LocalMediaCategory, sourceIndex int, albumRel string) string {
	paths := taterLocalMediaCategoryPaths(cat)
	if sourceIndex < 0 || sourceIndex >= len(paths) {
		return ""
	}
	albumPath, err := safeLocalPath(paths[sourceIndex], albumRel)
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(albumPath)
	if err != nil {
		return ""
	}
	priority := []string{"cover", "folder", "front", "album"}
	for _, base := range priority {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(strings.TrimSpace(entry.Name()))
			ext := strings.ToLower(filepath.Ext(name))
			if strings.TrimSuffix(name, ext) == base && isTaterArtworkImageExtension(ext) {
				rel, relErr := filepath.Rel(paths[sourceIndex], filepath.Join(albumPath, entry.Name()))
				if relErr == nil {
					return cleanLocalRelativePath(filepath.ToSlash(rel))
				}
			}
		}
	}
	return ""
}

func isTaterArtworkImageExtension(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	default:
		return false
	}
}

func safeTaterMusicArtworkCachePath(cfg *config.Config, ref string) (string, error) {
	base := taterMusicArtworkCacheDir(cfg)
	name := filepath.Base(strings.TrimSpace(ref))
	if name == "" || name == "." || name != strings.TrimSpace(ref) {
		return "", fmt.Errorf("invalid cached artwork reference")
	}
	return filepath.Join(base, name), nil
}

func taterLocalMusicAdminArtworkURL(album taterLocalMusicAlbumIndex) string {
	if !album.HasArtwork || strings.TrimSpace(album.ID) == "" {
		return ""
	}
	version := album.ArtworkUpdated
	if version <= 0 {
		version = album.ModifiedUnix
	}
	return fmt.Sprintf(
		"/api/local-media/music/artwork?album_id=%s&v=%d",
		url.QueryEscape(album.ID),
		version,
	)
}

func aggregateTaterLocalLibraryStats(categories []taterLocalLibraryCategoryIndex, libraryType string) taterLocalLibraryStats {
	var total taterLocalLibraryStats
	for _, category := range categories {
		if libraryType != "" && category.LibraryType != libraryType {
			continue
		}
		total.Files += category.Stats.Files
		total.Movies += category.Stats.Movies
		total.Shows += category.Stats.Shows
		total.Episodes += category.Stats.Episodes
		total.Artists += category.Stats.Artists
		total.Albums += category.Stats.Albums
		total.Songs += category.Stats.Songs
		total.Artwork += category.Stats.Artwork
		total.MissingArtwork += category.Stats.MissingArtwork
		total.Metadata += category.Stats.Metadata
		total.MissingMetadata += category.Stats.MissingMetadata
		total.Errors += category.Stats.Errors
		total.SizeBytes += category.Stats.SizeBytes
	}
	return total
}

func taterLocalMusicAlbumNeedsAttention(album taterLocalMusicAlbumIndex) bool {
	metadataMissing := album.MetadataAvailable && (!album.HasMetadata ||
		(album.ArtistMetadataAvailable && !album.HasArtistMetadata))
	return !album.HasArtwork || metadataMissing
}

func taterLocalVideoNeedsAttention(video taterLocalVideoIndex) bool {
	return !video.HasArtwork || !video.HasMetadata
}

func (s *Server) handleLocalMediaLibrary(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	cfg := s.configManager.GetConfig()
	index, err := readTaterLocalLibraryIndex(cfg)
	missing := os.IsNotExist(err)
	if err != nil && !missing {
		return RespondInternalError(c, "Failed to read local media index", err.Error())
	}
	if missing {
		index = taterLocalLibraryIndex{
			Schema:     taterLocalLibraryIndexSchema,
			Categories: []taterLocalLibraryCategoryIndex{},
			Albums:     []taterLocalMusicAlbumIndex{},
			Videos:     []taterLocalVideoIndex{},
			Files:      []taterLocalLibraryFileIndex{},
		}
		for _, cat := range cfg.LocalMedia.Categories {
			index.Categories = append(index.Categories, taterLocalLibraryCategoryIndex{
				ID: strings.TrimSpace(cat.ID), Name: strings.TrimSpace(cat.Name),
				LibraryType: strings.ToLower(strings.TrimSpace(cat.LibraryType)),
				Paths:       taterLocalMediaCategoryPaths(cat), Enabled: taterLocalLibraryEnabled(cat),
			})
		}
	}
	libraryType := strings.ToLower(strings.TrimSpace(c.Query("type")))
	categoryID := strings.TrimSpace(c.Query("category_id"))
	query := strings.ToLower(strings.TrimSpace(c.Query("q")))
	missingOnly := c.QueryBool("missing_only", false)
	filteredCategories := make([]taterLocalLibraryCategoryIndex, 0, len(index.Categories))
	for _, category := range index.Categories {
		if libraryType != "" && category.LibraryType != libraryType {
			continue
		}
		if categoryID != "" && category.ID != categoryID {
			continue
		}
		filteredCategories = append(filteredCategories, category)
	}
	albums := make([]taterLocalMusicAlbumIndex, 0, len(index.Albums))
	for _, album := range index.Albums {
		if libraryType != "" && libraryType != "music" {
			continue
		}
		if categoryID != "" && album.CategoryID != categoryID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
			album.Title, album.Artist, album.AlbumArtist, album.Year, strings.Join(album.Genres, " "),
		}, " ")), query) {
			continue
		}
		if missingOnly && !taterLocalMusicAlbumNeedsAttention(album) {
			continue
		}
		album.ArtworkURL = taterLocalMusicAdminArtworkURL(album)
		albums = append(albums, album)
	}
	videos := make([]taterLocalVideoIndex, 0, len(index.Videos))
	for _, video := range index.Videos {
		if libraryType != "" && video.LibraryType != libraryType {
			continue
		}
		if categoryID != "" && video.CategoryID != categoryID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
			video.Title, video.Year, video.CategoryName, video.Path,
		}, " ")), query) {
			continue
		}
		if missingOnly && !taterLocalVideoNeedsAttention(video) {
			continue
		}
		video.ArtworkURL = taterLocalVideoAdminArtworkURL(video)
		videos = append(videos, video)
	}
	totalAlbums := len(albums)
	totalVideos := len(videos)
	totalResults := max(totalAlbums, totalVideos)
	offset := queryInt(c, "offset", 0, 0, max(totalResults, 1))
	limit := queryInt(c, "limit", 120, 1, 500)
	albumEnd := min(totalAlbums, offset+limit)
	albumPage := []taterLocalMusicAlbumIndex{}
	if offset < totalAlbums {
		albumPage = albums[offset:albumEnd]
	}
	videoEnd := min(totalVideos, offset+limit)
	videoPage := []taterLocalVideoIndex{}
	if offset < totalVideos {
		videoPage = videos[offset:videoEnd]
	}
	return RespondSuccess(c, fiber.Map{
		"generated_at": index.GeneratedAt,
		"stale":        missing || index.ConfigFingerprint != taterLocalLibraryFingerprint(cfg),
		"categories":   filteredCategories,
		"stats":        aggregateTaterLocalLibraryStats(filteredCategories, ""),
		"albums":       albumPage,
		"total_albums": totalAlbums,
		"videos":       videoPage,
		"total_videos": totalVideos,
		"offset":       offset,
		"limit":        limit,
		"scan":         getTaterLocalLibraryScanStatus(cfg),
	})
}

func (s *Server) handleLocalMediaScanStatus(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	return RespondSuccess(c, getTaterLocalLibraryScanStatus(s.configManager.GetConfig()))
}

func taterLocalLibraryIndexNeedsMaintenance(cfg *config.Config) (bool, string) {
	if !taterLocalMediaEnabled(cfg) {
		return false, ""
	}
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return true, "Building the local media index"
	}
	if index.ConfigFingerprint != taterLocalLibraryFingerprint(cfg) {
		return true, "Updating the local media index"
	}
	if !index.VideoDurationsScanned {
		return true, "Backfilling video durations"
	}
	return false, ""
}

func beginTaterLocalLibraryScan(cfg *config.Config, message string) bool {
	key := taterLocalLibraryScanKey(cfg)
	taterLocalLibraryScans.Lock()
	defer taterLocalLibraryScans.Unlock()
	if taterLocalLibraryScans.Items[key].Running {
		return false
	}
	if strings.TrimSpace(message) == "" {
		message = "Starting local media scan"
	}
	taterLocalLibraryScans.Items[key] = taterLocalLibraryScanStatus{
		Running: true, Phase: "scanning", Message: message,
		StartedAt: time.Now().UTC(),
	}
	return true
}

func runTaterLocalLibraryScan(
	cfg *config.Config,
	request taterLocalLibraryScanRequest,
) (taterLocalLibraryIndex, error) {
	previous, _ := readTaterLocalLibraryIndex(cfg)
	index, err := scanTaterLocalLibrary(context.Background(), cfg, previous, func(progress taterLocalLibraryScanProgress) {
		updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
			status.Phase = progress.Phase
			status.FilesScanned = progress.FilesScanned
			status.FilesTotal = progress.FilesTotal
			status.Message = progress.Message
			setTaterLocalLibraryProgress(status, progress.Current, progress.Total)
		})
	})
	if err == nil && request.ScrapeMissingArtwork {
		artworkType := strings.ToLower(strings.TrimSpace(request.ArtworkLibraryType))
		if artworkType == "" {
			artworkType = "all"
		}
		musicArtworkFound := 0
		musicMetadataFound := 0
		if artworkType == "all" || artworkType == "music" {
			updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
				status.Phase = "artwork"
				status.Message = "Finding album artwork, genres, and NFO metadata"
				setTaterLocalLibraryProgress(status, 0, 0)
			})
			err = scrapeTaterMissingAlbumArtwork(context.Background(), cfg, &index, func(progress taterMusicEnrichmentProgress) {
				musicArtworkFound = progress.ArtworkFound
				musicMetadataFound = progress.MetadataFound
				updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
					status.AlbumsProcessed = progress.AlbumsProcessed
					status.AlbumsTotal = progress.AlbumsTotal
					status.ArtworkFound = progress.ArtworkFound
					status.MetadataFound = progress.MetadataFound
					status.GenreMatches = progress.GenreMatches
					status.GenreUnmatched = progress.GenreUnmatched
					if progress.Message != "" {
						status.Message = progress.Message
					}
					setTaterLocalLibraryProgress(status, progress.AlbumsProcessed, progress.AlbumsTotal)
				})
			})
		}
		if err == nil && (artworkType == "all" || artworkType == "movies" || artworkType == "tv") {
			updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
				status.Phase = "artwork"
				status.Message = "Finding movie and TV artwork and metadata"
				setTaterLocalLibraryProgress(status, 0, 0)
			})
			err = scrapeTaterMissingVideoArtwork(context.Background(), cfg, &index, artworkType, func(progress taterVideoArtworkProgress) {
				updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
					status.VideosProcessed = progress.VideosProcessed
					status.VideosTotal = progress.VideosTotal
					status.ArtworkFound = musicArtworkFound + progress.ArtworkFound
					status.MetadataFound = musicMetadataFound + progress.MetadataFound
					if progress.Message != "" {
						status.Message = progress.Message
					}
					setTaterLocalLibraryProgress(status, progress.VideosProcessed, progress.VideosTotal)
				})
			})
		}
	}
	if err == nil {
		index.GeneratedAt = time.Now().UTC()
		err = writeTaterJSON(taterLocalLibraryIndexPath(cfg), index)
	}
	finishedAt := time.Now().UTC()
	updateTaterLocalLibraryScanStatus(cfg, func(status *taterLocalLibraryScanStatus) {
		status.Running = false
		status.FinishedAt = finishedAt
		if err != nil {
			status.Phase = "error"
			status.Error = err.Error()
			status.Message = "Local media scan failed"
		} else {
			status.Phase = "complete"
			status.FilesScanned = len(index.Files)
			status.FilesTotal = len(index.Files)
			status.ProgressCurrent = status.ProgressTotal
			status.ProgressPercent = 100
			if status.AlbumsProcessed > 0 || status.VideosProcessed > 0 {
				status.Message = fmt.Sprintf(
					"Library updated: %d artwork and %d NFO files found, %d albums and %d videos checked",
					status.ArtworkFound,
					status.MetadataFound,
					status.AlbumsProcessed,
					status.VideosProcessed,
				)
			} else {
				status.Message = "Local media library is up to date"
			}
		}
	})
	if err != nil {
		slog.Warn("Local media scan failed", "error", err)
	}
	return index, err
}

func (s *Server) handleLocalMediaScan(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	request := taterLocalLibraryScanRequest{}
	if len(c.Body()) > 0 {
		if err := c.BodyParser(&request); err != nil {
			return RespondValidationError(c, "Invalid local media scan request", err.Error())
		}
	}
	cfg := s.configManager.GetConfig().DeepCopy()
	if !beginTaterLocalLibraryScan(cfg, "Starting local media scan") {
		status := getTaterLocalLibraryScanStatus(cfg)
		return RespondConflict(c, "A local media scan is already running", status.Message)
	}
	go func() {
		if _, err := runTaterLocalLibraryScan(cfg, request); err == nil {
			taterTVResetGuideForConfig(cfg)
			if _, guideErr := taterTVEnsureGuide(cfg, "", time.Now()); guideErr != nil {
				slog.Warn("Failed to rebuild Tube TV guide after local media scan", "error", guideErr)
			}
		}
	}()
	return RespondSuccess(c, getTaterLocalLibraryScanStatus(cfg))
}

func findTaterLocalMusicAlbum(index *taterLocalLibraryIndex, albumID string) (*taterLocalMusicAlbumIndex, bool) {
	if index == nil {
		return nil, false
	}
	for i := range index.Albums {
		if index.Albums[i].ID == albumID {
			return &index.Albums[i], true
		}
	}
	return nil, false
}

func (s *Server) handleLocalMediaMusicArtworkRefresh(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	request := taterLocalMusicArtworkRequest{}
	if err := c.BodyParser(&request); err != nil {
		return RespondValidationError(c, "Invalid artwork request", err.Error())
	}
	request.AlbumID = strings.TrimSpace(request.AlbumID)
	if request.AlbumID == "" {
		return RespondValidationError(c, "Album is required", "album_id is empty")
	}
	cfg := s.configManager.GetConfig()
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return RespondValidationError(c, "Scan the music library before finding artwork", err.Error())
	}
	album, ok := findTaterLocalMusicAlbum(&index, request.AlbumID)
	if !ok {
		return RespondNotFound(c, "Music album", request.AlbumID)
	}
	metadataComplete := album.HasMetadata &&
		(!album.ArtistMetadataAvailable || album.HasArtistMetadata)
	if album.ArtworkLocked && !request.Force && metadataComplete {
		return RespondConflict(c, "Album artwork is locked", "Unlock it before finding another cover")
	}
	var artworkErr error
	if !album.ArtworkLocked || request.Force {
		artworkErr = refreshTaterAlbumArtwork(c.Context(), cfg, &index, album, request.Force)
	}
	_, metadataErr := ensureTaterMusicNFO(c.Context(), cfg, album)
	refreshTaterLibraryArtworkStats(&index)
	if metadataErr != nil && !album.HasMetadata {
		return RespondValidationError(c, "No matching album metadata was found", metadataErr.Error())
	}
	if artworkErr != nil && !album.HasArtwork {
		return RespondValidationError(c, "No matching album artwork was found", artworkErr.Error())
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), index); err != nil {
		return RespondInternalError(c, "Failed to save album artwork and metadata", err.Error())
	}
	album.ArtworkURL = taterLocalMusicAdminArtworkURL(*album)
	return RespondSuccess(c, album)
}

func (s *Server) handleLocalMediaMusicArtworkUpdate(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	request := taterLocalMusicArtworkRequest{}
	if err := c.BodyParser(&request); err != nil {
		return RespondValidationError(c, "Invalid artwork request", err.Error())
	}
	if strings.TrimSpace(request.AlbumID) == "" || request.Locked == nil {
		return RespondValidationError(c, "Album and locked state are required", "album_id or locked is missing")
	}
	cfg := s.configManager.GetConfig()
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return RespondValidationError(c, "Scan the music library before managing artwork", err.Error())
	}
	album, ok := findTaterLocalMusicAlbum(&index, strings.TrimSpace(request.AlbumID))
	if !ok {
		return RespondNotFound(c, "Music album", request.AlbumID)
	}
	if !album.HasArtwork && *request.Locked {
		return RespondValidationError(c, "Find artwork before locking this album", "album has no artwork")
	}
	store := readTaterMusicArtworkStore(cfg)
	if *request.Locked {
		existing := store.Items[album.ID]
		existing.AlbumID = album.ID
		existing.Source = album.ArtworkSource
		existing.Ref = album.ArtworkRef
		existing.MusicBrainzID = album.MusicBrainzID
		existing.MusicBrainzArtistID = album.MusicBrainzArtistID
		existing.Locked = true
		existing.UpdatedAt = time.Now().UTC()
		store.Items[album.ID] = existing
		album.ArtworkLocked = true
	} else if existing, exists := store.Items[album.ID]; exists {
		existing.Locked = false
		existing.UpdatedAt = time.Now().UTC()
		store.Items[album.ID] = existing
		album.ArtworkLocked = false
	}
	if err := writeTaterMusicArtworkStore(cfg, store); err != nil {
		return RespondInternalError(c, "Failed to save artwork preference", err.Error())
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), index); err != nil {
		return RespondInternalError(c, "Failed to update local media index", err.Error())
	}
	album.ArtworkURL = taterLocalMusicAdminArtworkURL(*album)
	return RespondSuccess(c, album)
}

func (s *Server) handleLocalMediaMusicArtwork(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	return serveTaterIndexedMusicArtwork(c, s.configManager.GetConfig(), strings.TrimSpace(c.Query("album_id")))
}

func serveTaterIndexedMusicArtwork(c *fiber.Ctx, cfg *config.Config, albumID string) error {
	if albumID == "" {
		return RespondValidationError(c, "Album is required", "album_id is empty")
	}
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return RespondNotFound(c, "Album artwork", albumID)
	}
	album, ok := findTaterLocalMusicAlbum(&index, albumID)
	if !ok || !album.HasArtwork {
		return RespondNotFound(c, "Album artwork", albumID)
	}
	cat, ok := taterLocalMediaCategory(cfg, album.CategoryID)
	if !ok {
		return RespondNotFound(c, "Music library", album.CategoryID)
	}
	var path string
	switch album.ArtworkSource {
	case "scraped", "manual":
		if album.ArtworkStorage == "library" {
			paths := taterLocalMediaCategoryPaths(cat)
			if album.SourceIndex < 0 || album.SourceIndex >= len(paths) {
				err = fmt.Errorf("music source is unavailable")
			} else {
				path, err = safeLocalPath(paths[album.SourceIndex], album.ArtworkRef)
			}
		} else {
			path, err = safeTaterMusicArtworkCachePath(cfg, album.ArtworkRef)
		}
	case "embedded", "local":
		paths := taterLocalMediaCategoryPaths(cat)
		if album.SourceIndex < 0 || album.SourceIndex >= len(paths) {
			err = fmt.Errorf("music source is unavailable")
		} else {
			path, err = safeLocalPath(paths[album.SourceIndex], album.ArtworkRef)
		}
	default:
		err = fmt.Errorf("album artwork source is unavailable")
	}
	if err != nil {
		return RespondNotFound(c, "Album artwork", albumID)
	}
	if (album.ArtworkSource == "scraped" || album.ArtworkSource == "manual") && album.ArtworkStorage != "library" {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return RespondNotFound(c, "Album artwork", albumID)
		}
		contentType := "image/jpeg"
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png":
			contentType = "image/png"
		case ".webp":
			contentType = "image/webp"
		}
		c.Set(fiber.HeaderContentType, contentType)
		c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
		return c.Send(raw)
	}
	jpeg, err := taterLocalMusicArtworkForPath(c.Context(), cfg, path)
	if err != nil {
		return RespondNotFound(c, "Album artwork", albumID)
	}
	c.Set(fiber.HeaderContentType, "image/jpeg")
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.Send(jpeg)
}
