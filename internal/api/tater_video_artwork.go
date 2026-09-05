package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/gofiber/fiber/v2"
)

const taterTMDBResponseMaximumBytes = 4 * 1024 * 1024

const taterDiscoveryArtworkMissTTL = 24 * time.Hour

var (
	taterTMDBBaseURL      = "https://api.themoviedb.org/3"
	taterTMDBImageBaseURL = "https://image.tmdb.org/t/p/w500"
	taterTMDBHTTPClient   = &http.Client{Timeout: 30 * time.Second}
)

type taterVideoArtworkOverride struct {
	MediaID   string    `json:"media_id"`
	Source    string    `json:"source"`
	Ref       string    `json:"ref"`
	TMDBID    int64     `json:"tmdb_id,omitempty"`
	Locked    bool      `json:"locked"`
	UpdatedAt time.Time `json:"updated_at"`
}

type taterVideoArtworkStore struct {
	Schema int                                  `json:"schema"`
	Items  map[string]taterVideoArtworkOverride `json:"items"`
}

type taterTMDBSearchResult struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Name          string  `json:"name"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	PosterPath    string  `json:"poster_path"`
	Popularity    float64 `json:"popularity"`
}

type taterTMDBSearchResponse struct {
	Results []taterTMDBSearchResult `json:"results"`
}

type taterTMDBFindResponse struct {
	MovieResults []taterTMDBSearchResult `json:"movie_results"`
	TVResults    []taterTMDBSearchResult `json:"tv_results"`
}

type taterVideoArtworkCandidate struct {
	TMDBID     int64
	Title      string
	Year       string
	PosterPath string
	Popularity float64
}

type taterVideoArtworkProgress struct {
	VideosProcessed int
	VideosTotal     int
	ArtworkFound    int
	MetadataFound   int
	Message         string
}

func taterVideoArtworkStorePath(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "video-artwork.json")
}

func readTaterVideoArtworkStore(cfg *config.Config) taterVideoArtworkStore {
	store := taterVideoArtworkStore{Schema: 1, Items: map[string]taterVideoArtworkOverride{}}
	raw, err := os.ReadFile(taterVideoArtworkStorePath(cfg))
	if err != nil {
		return store
	}
	if err := json.Unmarshal(raw, &store); err != nil || store.Items == nil {
		return taterVideoArtworkStore{Schema: 1, Items: map[string]taterVideoArtworkOverride{}}
	}
	store.Schema = 1
	return store
}

func writeTaterVideoArtworkStore(cfg *config.Config, store taterVideoArtworkStore) error {
	store.Schema = 1
	if store.Items == nil {
		store.Items = map[string]taterVideoArtworkOverride{}
	}
	return writeTaterJSON(taterVideoArtworkStorePath(cfg), store)
}

func taterVideoMediaID(categoryID string, sourceIndex int, mediaType, relPath string) string {
	return "video:" +
		base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(categoryID))) + ":" +
		strconv.Itoa(sourceIndex) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(strings.TrimSpace(mediaType))) + ":" +
		base64.RawURLEncoding.EncodeToString([]byte(cleanLocalRelativePath(relPath)))
}

func taterLocalVideoMetadataFile(cfg *config.Config, video taterLocalVideoIndex) (taterLocalNFO, string, bool) {
	cat, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return taterLocalNFO{}, "", false
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return taterLocalNFO{}, "", false
	}
	target, err := safeLocalPath(roots[video.SourceIndex], video.Path)
	if err != nil {
		return taterLocalNFO{}, "", false
	}
	directory := target
	base := ""
	if video.MediaType != "show" {
		directory = filepath.Dir(target)
		base = strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	}
	meta, path, found := taterReadLocalMetadataFile(directory, base)
	if !found {
		return taterLocalNFO{}, "", false
	}
	rel, err := filepath.Rel(roots[video.SourceIndex], path)
	if err != nil {
		return taterLocalNFO{}, "", false
	}
	return meta, cleanLocalRelativePath(filepath.ToSlash(rel)), true
}

func applyTaterLocalNFOMetadata(video *taterLocalVideoIndex, meta taterLocalNFO, ref string) {
	if video == nil {
		return
	}
	if title := cleanTaterText(meta.Title); title != "" {
		video.Title = title
	}
	if year := taterLocalMetadataYear(meta); year != "" {
		video.Year = year
	}
	video.Description = cleanTaterText(meta.Plot)
	if video.Description == "" {
		video.Description = cleanTaterText(meta.Outline)
	}
	video.OriginalTitle = cleanTaterText(meta.OriginalTitle)
	video.Tagline = cleanTaterText(meta.Tagline)
	video.ContentRating = cleanTaterText(meta.MPAA)
	video.CommunityRating = taterLocalMetadataRating(meta.Rating)
	video.Genres = taterLocalMetadataGenres(meta)
	video.Studios = cleanTaterMetadataValues(meta.Studios)
	video.Countries = cleanTaterMetadataValues(meta.Countries)
	video.Actors = taterLocalMetadataActors(meta)
	video.Directors = cleanTaterMetadataValues(meta.Directors)
	video.Writers = cleanTaterMetadataValues(meta.Writers)
	video.IMDbID, video.TMDBID, video.TVDBID = taterLocalMetadataIDs(meta)
	video.HasMetadata = true
	video.MetadataSource = "nfo"
	video.NFORef = ref
}

func taterStoredVideoArtworkPath(
	cfg *config.Config,
	category config.LocalMediaCategory,
	sourceIndex int,
	relPath string,
) (string, bool) {
	libraryType := strings.ToLower(strings.TrimSpace(category.LibraryType))
	mediaType := "movie"
	mediaPath := cleanLocalRelativePath(relPath)
	if libraryType == "tv" {
		parts := strings.Split(mediaPath, "/")
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return "", false
		}
		mediaType = "show"
		mediaPath = parts[0]
	} else if libraryType != "movies" {
		return "", false
	}
	mediaID := taterVideoMediaID(category.ID, sourceIndex, mediaType, mediaPath)
	entry, ok := readTaterVideoArtworkStore(cfg).Items[mediaID]
	video := taterLocalVideoIndex{
		ID: mediaID, CategoryID: category.ID, SourceIndex: sourceIndex,
		LibraryType: libraryType, MediaType: mediaType, Path: mediaPath,
	}
	if !ok || !taterVideoArtworkRefExists(cfg, video, entry.Ref) {
		return "", false
	}
	roots := taterLocalMediaCategoryPaths(category)
	if sourceIndex < 0 || sourceIndex >= len(roots) {
		return "", false
	}
	path, err := safeLocalPath(roots[sourceIndex], entry.Ref)
	return path, err == nil
}

func buildTaterLocalVideoArtworkIndex(
	cfg *config.Config,
	files []taterLocalLibraryFileIndex,
	categories []taterLocalLibraryCategoryIndex,
) []taterLocalVideoIndex {
	categoryNames := map[string]string{}
	for _, category := range categories {
		categoryNames[category.ID] = category.Name
	}
	categoryConfigs := map[string]config.LocalMediaCategory{}
	if cfg != nil {
		for _, category := range cfg.LocalMedia.Categories {
			categoryConfigs[strings.TrimSpace(category.ID)] = category
		}
	}

	videos := []taterLocalVideoIndex{}
	type showBuild struct {
		Video taterLocalVideoIndex
	}
	shows := map[string]*showBuild{}
	for _, file := range files {
		switch file.LibraryType {
		case "movies":
			cat := categoryConfigs[file.CategoryID]
			roots := taterLocalMediaCategoryPaths(cat)
			root := ""
			if file.SourceIndex >= 0 && file.SourceIndex < len(roots) {
				root = roots[file.SourceIndex]
			}
			title, year := cleanMovieTitleAndYear(movieTitleSource(root, file.Path))
			if cleanTaterText(file.Title) != "" {
				title = cleanTaterText(file.Title)
			}
			if strings.TrimSpace(file.Year) != "" {
				year = strings.TrimSpace(file.Year)
			}
			if title == "" {
				title = cleanMovieTitleFromName(strings.TrimSuffix(filepath.Base(file.Path), filepath.Ext(file.Path)))
			}
			videos = append(videos, taterLocalVideoIndex{
				ID:           taterVideoMediaID(file.CategoryID, file.SourceIndex, "movie", file.Path),
				CategoryID:   file.CategoryID,
				CategoryName: categoryNames[file.CategoryID],
				LibraryType:  "movies",
				MediaType:    "movie",
				SourceIndex:  file.SourceIndex,
				Path:         cleanLocalRelativePath(file.Path),
				Title:        title,
				Year:         year,
				SizeBytes:    file.SizeBytes,
				ModifiedUnix: file.ModifiedUnix,
			})
		case "tv":
			parts := strings.Split(cleanLocalRelativePath(file.Path), "/")
			if len(parts) < 2 || strings.TrimSpace(parts[0]) == "" {
				continue
			}
			showPath := parts[0]
			id := taterVideoMediaID(file.CategoryID, file.SourceIndex, "show", showPath)
			build := shows[id]
			if build == nil {
				title, year := cleanMovieTitleAndYear(showPath)
				if title == "" {
					title = cleanShowTitle(showPath)
				}
				build = &showBuild{Video: taterLocalVideoIndex{
					ID:           id,
					CategoryID:   file.CategoryID,
					CategoryName: categoryNames[file.CategoryID],
					LibraryType:  "tv",
					MediaType:    "show",
					SourceIndex:  file.SourceIndex,
					Path:         showPath,
					Title:        title,
					Year:         year,
				}}
				shows[id] = build
			}
			build.Video.SizeBytes += file.SizeBytes
			if file.ModifiedUnix > build.Video.ModifiedUnix {
				build.Video.ModifiedUnix = file.ModifiedUnix
			}
		}
	}
	for _, build := range shows {
		video := build.Video
		if cat, ok := categoryConfigs[video.CategoryID]; ok {
			roots := taterLocalMediaCategoryPaths(cat)
			if video.SourceIndex >= 0 && video.SourceIndex < len(roots) {
				if showDir, err := safeLocalPath(roots[video.SourceIndex], video.Path); err == nil {
					if meta, found := taterReadLocalMetadata(showDir, ""); found {
						if title := cleanTaterText(meta.Title); title != "" {
							video.Title = title
						}
						if year := taterLocalMetadataYear(meta); year != "" {
							video.Year = year
						}
					}
				}
			}
		}
		videos = append(videos, video)
	}

	store := readTaterVideoArtworkStore(cfg)
	for i := range videos {
		video := &videos[i]
		if meta, ref, found := taterLocalVideoMetadataFile(cfg, *video); found {
			applyTaterLocalNFOMetadata(video, meta, ref)
		}
		override := store.Items[video.ID]
		if override.MediaID == video.ID && taterVideoArtworkRefExists(cfg, *video, override.Ref) {
			video.HasArtwork = true
			video.ArtworkSource = override.Source
			video.ArtworkRef = override.Ref
			video.ArtworkLocked = override.Locked
			if override.TMDBID > 0 {
				video.TMDBID = override.TMDBID
			}
			video.ArtworkUpdated = override.UpdatedAt.Unix()
		} else if ref := findTaterLocalVideoArtworkRef(cfg, *video); ref != "" {
			video.HasArtwork = true
			video.ArtworkSource = "local"
			video.ArtworkRef = ref
		}
		video.ArtworkURL = taterLocalVideoAdminArtworkURL(*video)
	}
	sort.SliceStable(videos, func(i, j int) bool {
		left := strings.ToLower(videos[i].LibraryType + "\x00" + videos[i].Title)
		right := strings.ToLower(videos[j].LibraryType + "\x00" + videos[j].Title)
		return left < right
	})
	return videos
}

func findTaterLocalVideoArtworkRef(cfg *config.Config, video taterLocalVideoIndex) string {
	path, ok := taterPlayerLocalArtworkPath(cfg, video.CategoryID, video.SourceIndex, video.Path)
	if !ok {
		return ""
	}
	cat, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return ""
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return ""
	}
	rel, err := filepath.Rel(roots[video.SourceIndex], path)
	if err != nil {
		return ""
	}
	return cleanLocalRelativePath(filepath.ToSlash(rel))
}

func taterVideoArtworkRefExists(cfg *config.Config, video taterLocalVideoIndex, ref string) bool {
	cat, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return false
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return false
	}
	path, err := safeLocalPath(roots[video.SourceIndex], ref)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info != nil && info.Mode().IsRegular() && info.Size() <= taterPlayerArtworkMaximumBytes
}

func taterLocalVideoAdminArtworkURL(video taterLocalVideoIndex) string {
	if !video.HasArtwork || strings.TrimSpace(video.ID) == "" {
		return ""
	}
	version := video.ArtworkUpdated
	if version <= 0 {
		version = video.ModifiedUnix
	}
	return fmt.Sprintf("/api/local-media/video/artwork?media_id=%s&v=%d", url.QueryEscape(video.ID), version)
}

func findTaterLocalVideo(index *taterLocalLibraryIndex, mediaID string) (*taterLocalVideoIndex, bool) {
	if index == nil {
		return nil, false
	}
	for i := range index.Videos {
		if index.Videos[i].ID == mediaID {
			return &index.Videos[i], true
		}
	}
	return nil, false
}

func taterTMDBConfigured(cfg *config.Config) bool {
	return cfg != nil && (cfg.LocalMedia.TMDBEnabled == nil || *cfg.LocalMedia.TMDBEnabled) &&
		strings.TrimSpace(cfg.LocalMedia.TMDBAPIKey) != ""
}

func taterTMDBRequest(ctx context.Context, cfg *config.Config, endpoint string, params url.Values) (*http.Response, error) {
	key := strings.TrimSpace(cfg.LocalMedia.TMDBAPIKey)
	if key == "" {
		return nil, fmt.Errorf("TMDB API key is not configured")
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("language", "en-US")
	if strings.HasPrefix(strings.TrimLeft(endpoint, "/"), "search/") {
		params.Set("include_adult", "false")
	}
	if len(key) <= 48 && !strings.Contains(key, ".") {
		params.Set("api_key", key)
	}
	rawURL := strings.TrimRight(taterTMDBBaseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
	if encoded := params.Encode(); encoded != "" {
		rawURL += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "TaterTubeServer/1.4 (https://github.com/TaterTotterson/tater-tube-server)")
	if len(key) > 48 || strings.Contains(key, ".") {
		request.Header.Set("Authorization", "Bearer "+key)
	}
	return taterTMDBHTTPClient.Do(request)
}

func findTaterRemoteVideoArtwork(ctx context.Context, cfg *config.Config, video taterLocalVideoIndex) (taterVideoArtworkCandidate, []byte, string, error) {
	candidate, err := findTaterRemoteVideoCandidate(ctx, cfg, video)
	if err != nil {
		return taterVideoArtworkCandidate{}, nil, "", err
	}
	return downloadTaterVideoArtworkCandidate(ctx, candidate)
}

func findTaterRemoteVideoCandidate(ctx context.Context, cfg *config.Config, video taterLocalVideoIndex) (taterVideoArtworkCandidate, error) {
	if !taterTMDBConfigured(cfg) {
		return taterVideoArtworkCandidate{}, fmt.Errorf("add a TMDB API key in Local Media before finding movie or TV artwork and metadata")
	}
	params := url.Values{}
	params.Set("query", strings.TrimSpace(video.Title))
	endpoint := "search/movie"
	if video.LibraryType == "tv" || video.MediaType == "show" {
		endpoint = "search/tv"
		if video.Year != "" {
			params.Set("first_air_date_year", video.Year)
		}
	} else if video.Year != "" {
		params.Set("primary_release_year", video.Year)
	}
	response, err := taterTMDBRequest(ctx, cfg, endpoint, params)
	if err != nil {
		return taterVideoArtworkCandidate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return taterVideoArtworkCandidate{}, fmt.Errorf("TMDB returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, taterTMDBResponseMaximumBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > taterTMDBResponseMaximumBytes {
		return taterVideoArtworkCandidate{}, fmt.Errorf("TMDB search response is invalid")
	}
	var payload taterTMDBSearchResponse
	if err := json.Unmarshal(raw, &payload); err != nil {
		return taterVideoArtworkCandidate{}, err
	}
	wanted := normalizeTaterMusicMatchText(video.Title)
	matches := []taterVideoArtworkCandidate{}
	for _, row := range payload.Results {
		if row.ID <= 0 {
			continue
		}
		titles := []string{row.Title, row.OriginalTitle, row.Name, row.OriginalName}
		matchedTitle := ""
		for _, title := range titles {
			if normalizeTaterMusicMatchText(title) == wanted {
				matchedTitle = strings.TrimSpace(title)
				break
			}
		}
		if matchedTitle == "" {
			continue
		}
		date := row.ReleaseDate
		if date == "" {
			date = row.FirstAirDate
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		if video.Year != "" && year != video.Year {
			continue
		}
		matches = append(matches, taterVideoArtworkCandidate{
			TMDBID: row.ID, Title: matchedTitle, Year: year,
			PosterPath: strings.TrimSpace(row.PosterPath), Popularity: row.Popularity,
		})
	}
	if len(matches) == 0 || (video.Year == "" && len(matches) > 1) {
		return taterVideoArtworkCandidate{}, os.ErrNotExist
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Popularity > matches[j].Popularity })
	return matches[0], nil
}

func findTaterRemoteVideoArtworkByExternalID(
	ctx context.Context,
	cfg *config.Config,
	video taterLocalVideoIndex,
	externalID string,
) (taterVideoArtworkCandidate, []byte, string, error) {
	candidate, err := findTaterRemoteVideoCandidateByExternalID(ctx, cfg, video, externalID)
	if err != nil {
		return taterVideoArtworkCandidate{}, nil, "", err
	}
	return downloadTaterVideoArtworkCandidate(ctx, candidate)
}

func findTaterRemoteVideoCandidateByExternalID(
	ctx context.Context,
	cfg *config.Config,
	video taterLocalVideoIndex,
	externalID string,
) (taterVideoArtworkCandidate, error) {
	externalID = strings.ToLower(strings.TrimSpace(externalID))
	if len(externalID) < 1 || len(externalID) > 24 {
		return taterVideoArtworkCandidate{}, os.ErrNotExist
	}
	externalSource := "tvdb_id"
	digits := externalID
	if strings.HasPrefix(externalID, "tt") {
		externalSource = "imdb_id"
		digits = externalID[2:]
	} else if video.LibraryType != "tv" && video.MediaType != "show" {
		return taterVideoArtworkCandidate{}, os.ErrNotExist
	}
	if digits == "" {
		return taterVideoArtworkCandidate{}, os.ErrNotExist
	}
	for _, character := range digits {
		if character < '0' || character > '9' {
			return taterVideoArtworkCandidate{}, os.ErrNotExist
		}
	}
	params := url.Values{"external_source": []string{externalSource}}
	response, err := taterTMDBRequest(ctx, cfg, "find/"+url.PathEscape(externalID), params)
	if err != nil {
		return taterVideoArtworkCandidate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return taterVideoArtworkCandidate{}, fmt.Errorf("TMDB returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, taterTMDBResponseMaximumBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > taterTMDBResponseMaximumBytes {
		return taterVideoArtworkCandidate{}, fmt.Errorf("TMDB find response is invalid")
	}
	payload := taterTMDBFindResponse{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return taterVideoArtworkCandidate{}, err
	}
	results := payload.MovieResults
	if video.LibraryType == "tv" || video.MediaType == "show" {
		results = payload.TVResults
	}
	candidates := make([]taterVideoArtworkCandidate, 0, len(results))
	for _, row := range results {
		if row.ID <= 0 {
			continue
		}
		title := strings.TrimSpace(row.Title)
		if title == "" {
			title = strings.TrimSpace(row.Name)
		}
		date := row.ReleaseDate
		if date == "" {
			date = row.FirstAirDate
		}
		year := ""
		if len(date) >= 4 {
			year = date[:4]
		}
		candidates = append(candidates, taterVideoArtworkCandidate{
			TMDBID: row.ID, Title: title, Year: year,
			PosterPath: strings.TrimSpace(row.PosterPath), Popularity: row.Popularity,
		})
	}
	if len(candidates) == 0 {
		return taterVideoArtworkCandidate{}, os.ErrNotExist
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Popularity > candidates[j].Popularity })
	return candidates[0], nil
}

func downloadTaterVideoArtworkCandidate(
	ctx context.Context,
	candidate taterVideoArtworkCandidate,
) (taterVideoArtworkCandidate, []byte, string, error) {
	imageURL := strings.TrimRight(taterTMDBImageBaseURL, "/") + "/" + strings.TrimLeft(candidate.PosterPath, "/")
	raw, contentType, err := downloadTaterVideoArtwork(ctx, imageURL)
	if err != nil {
		return taterVideoArtworkCandidate{}, nil, "", err
	}
	return candidate, raw, contentType, nil
}

func downloadTaterVideoArtwork(ctx context.Context, imageURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", os.ErrNotExist
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "image/jpeg,image/png,image/webp")
	request.Header.Set("User-Agent", "TaterTubeServer/1.4 (https://github.com/TaterTotterson/tater-tube-server)")
	response, err := taterTMDBHTTPClient.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("artwork download returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, taterMusicArtworkDownloadMaximumBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > taterMusicArtworkDownloadMaximumBytes {
		return nil, "", os.ErrNotExist
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType == "" || !strings.HasPrefix(contentType, "image/") {
		contentType = strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(raw), ";")[0]))
	}
	if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
		return nil, "", os.ErrNotExist
	}
	return raw, contentType, nil
}

func taterArtworkExtension(contentType string) string {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}

func writeTaterArtworkFile(path string, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("artwork is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tater-artwork-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmp.Write(raw); err != nil {
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

func writeTaterVideoArtworkSidecar(cfg *config.Config, video taterLocalVideoIndex, raw []byte, contentType string) (string, error) {
	cat, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return "", fmt.Errorf("local media category is unavailable")
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return "", fmt.Errorf("local media source is unavailable")
	}
	target, err := safeLocalPath(roots[video.SourceIndex], video.Path)
	if err != nil {
		return "", err
	}
	ext := taterArtworkExtension(contentType)
	artworkPath := ""
	if video.MediaType == "show" {
		artworkPath = filepath.Join(target, "poster"+ext)
	} else {
		dir := filepath.Dir(target)
		mediaFiles := 0
		if entries, readErr := os.ReadDir(dir); readErr == nil {
			for _, entry := range entries {
				if !entry.IsDir() && isMediaExtension(filepath.Ext(entry.Name())) {
					mediaFiles++
				}
			}
		}
		if mediaFiles <= 1 {
			artworkPath = filepath.Join(dir, "poster"+ext)
		} else {
			base := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
			artworkPath = filepath.Join(dir, base+"-poster"+ext)
		}
	}
	if err := writeTaterArtworkFile(artworkPath, raw); err != nil {
		return "", fmt.Errorf("save artwork beside media: %w", err)
	}
	rel, err := filepath.Rel(roots[video.SourceIndex], artworkPath)
	if err != nil {
		return "", err
	}
	return cleanLocalRelativePath(filepath.ToSlash(rel)), nil
}

func writeTaterMusicArtworkSidecar(
	cfg *config.Config,
	album taterLocalMusicAlbumIndex,
	raw []byte,
	contentType string,
) (string, bool, error) {
	cat, ok := taterLocalMediaCategory(cfg, album.CategoryID)
	if !ok {
		return "", false, nil
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if album.SourceIndex < 0 || album.SourceIndex >= len(roots) {
		return "", false, nil
	}
	albumDirectory, err := safeLocalPath(roots[album.SourceIndex], album.Path)
	if err != nil {
		return "", true, err
	}
	artworkPath := filepath.Join(albumDirectory, "cover"+taterArtworkExtension(contentType))
	if err := writeTaterArtworkFile(artworkPath, raw); err != nil {
		return "", true, fmt.Errorf("save cover beside album: %w", err)
	}
	rel, err := filepath.Rel(roots[album.SourceIndex], artworkPath)
	if err != nil {
		return "", true, err
	}
	return cleanLocalRelativePath(filepath.ToSlash(rel)), true, nil
}

func refreshTaterVideoArtwork(ctx context.Context, cfg *config.Config, index *taterLocalLibraryIndex, video *taterLocalVideoIndex, force bool) error {
	if video == nil {
		return fmt.Errorf("movie or TV show is unavailable")
	}
	if !force && !taterVideoArtworkNeedsRefresh(cfg, index, *video) {
		return nil
	}
	candidate, details, err := resolveTaterTMDBVideoMetadata(ctx, cfg, *video)
	if err != nil {
		return err
	}
	video.TMDBID = candidate.TMDBID
	if !video.HasMetadata {
		if _, err := writeTaterVideoNFO(cfg, video, details); err != nil {
			return err
		}
		updateTaterIndexedVideoMetadata(index, *video)
	}
	var posterErr error
	if (!video.HasArtwork || force) && (!video.ArtworkLocked || force) {
		if strings.TrimSpace(candidate.PosterPath) == "" {
			candidate.PosterPath = strings.TrimSpace(details.PosterPath)
		}
		if strings.TrimSpace(candidate.PosterPath) == "" {
			posterErr = fmt.Errorf("TMDB metadata did not include a poster")
		} else {
			_, raw, contentType, err := downloadTaterVideoArtworkCandidate(ctx, candidate)
			if err != nil {
				posterErr = err
			} else {
				ref, writeErr := writeTaterVideoArtworkSidecar(cfg, *video, raw, contentType)
				if writeErr != nil {
					posterErr = writeErr
				} else {
					updatedAt := time.Now().UTC()
					store := readTaterVideoArtworkStore(cfg)
					store.Items[video.ID] = taterVideoArtworkOverride{
						MediaID: video.ID, Source: "scraped", Ref: ref, TMDBID: candidate.TMDBID,
						Locked: false, UpdatedAt: updatedAt,
					}
					if writeErr = writeTaterVideoArtworkStore(cfg, store); writeErr != nil {
						posterErr = writeErr
					} else {
						video.HasArtwork = true
						video.ArtworkSource = "scraped"
						video.ArtworkRef = ref
						video.ArtworkUpdated = updatedAt.Unix()
						video.ArtworkURL = taterLocalVideoAdminArtworkURL(*video)
					}
				}
			}
		}
	}
	supplementalErr := refreshTaterTVSupplementalArtwork(ctx, cfg, index, *video, details, force)
	refreshTaterLibraryArtworkStats(index)
	if posterErr != nil {
		return posterErr
	}
	return supplementalErr
}

func scrapeTaterMissingVideoArtwork(ctx context.Context, cfg *config.Config, index *taterLocalLibraryIndex, libraryType string, progress func(taterVideoArtworkProgress)) error {
	if index == nil || !taterTMDBConfigured(cfg) {
		return nil
	}
	wantedType := strings.ToLower(strings.TrimSpace(libraryType))
	totalVideos := 0
	for i := range index.Videos {
		video := &index.Videos[i]
		if (wantedType == "movies" || wantedType == "tv") && video.LibraryType != wantedType {
			continue
		}
		if !taterVideoArtworkNeedsRefresh(cfg, index, *video) {
			continue
		}
		totalVideos++
	}
	status := taterVideoArtworkProgress{VideosTotal: totalVideos}
	if progress != nil {
		progress(status)
	}
	for i := range index.Videos {
		if err := ctx.Err(); err != nil {
			return err
		}
		video := &index.Videos[i]
		if (wantedType == "movies" || wantedType == "tv") && video.LibraryType != wantedType {
			continue
		}
		if !taterVideoArtworkNeedsRefresh(cfg, index, *video) {
			continue
		}
		status.VideosProcessed++
		status.Message = "Finding artwork for " + video.Title
		if progress != nil {
			progress(status)
		}
		hadArtwork := video.HasArtwork
		hadMetadata := video.HasMetadata
		err := refreshTaterVideoArtwork(ctx, cfg, index, video, false)
		if !hadArtwork && video.HasArtwork {
			status.ArtworkFound++
		}
		if !hadMetadata && video.HasMetadata {
			status.MetadataFound++
		}
		if err != nil {
			slog.Debug("Video artwork enrichment did not find a confident match", "title", video.Title, "year", video.Year, "error", err)
		}
		if progress != nil {
			progress(status)
		}
	}
	refreshTaterLibraryArtworkStats(index)
	return nil
}

func decorateTaterDiscoveryArtwork(cfg *config.Config, baseURL, playerToken string, items []taterUsenetItem) {
	if !taterTMDBConfigured(cfg) {
		return
	}
	for index := range items {
		item := &items[index]
		// Cinemeta remains the primary Discovery artwork source. The server fallback
		// is only advertised for catalog entries that arrived without a poster.
		if strings.TrimSpace(item.Poster) != "" || strings.TrimSpace(item.Title) == "" {
			continue
		}
		item.Poster = taterDiscoveryArtworkURL(
			baseURL,
			playerToken,
			item.MediaType,
			item.Title,
			taterDiscoveryYear(item.Date),
			item.GUID,
		)
		item.HasArtwork = item.Poster != ""
	}
}

func taterDiscoveryYear(value string) string {
	value = strings.TrimSpace(value)
	for index := 0; index+4 <= len(value); index++ {
		candidate := value[index : index+4]
		year, err := strconv.Atoi(candidate)
		if err == nil && year >= 1800 && year <= 2200 {
			return candidate
		}
	}
	return ""
}

func taterDiscoveryArtworkURL(baseURL, playerToken, mediaType, title, year, externalID string) string {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/api/v1/player/artwork/discovery")
	if err != nil {
		return ""
	}
	query := u.Query()
	query.Set("media_type", strings.ToLower(strings.TrimSpace(mediaType)))
	query.Set("title", cleanTaterText(title))
	if year != "" {
		query.Set("year", year)
	}
	if externalID = strings.TrimSpace(externalID); externalID != "" {
		query.Set("external_id", externalID)
	}
	query.Set("player_token", playerToken)
	u.RawQuery = query.Encode()
	return u.String()
}

func taterDiscoveryArtworkCacheDir(cfg *config.Config) string {
	return filepath.Join(taterLocalLibraryDataDir(cfg), "discovery-artwork")
}

func taterDiscoveryArtworkCacheKey(mediaType, title, year, externalID string) string {
	value := strings.ToLower(strings.TrimSpace(mediaType)) + "\x00" +
		normalizeTaterMusicMatchText(title) + "\x00" + strings.TrimSpace(year) + "\x00" +
		strings.ToLower(strings.TrimSpace(externalID))
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:16])
}

func findTaterDiscoveryArtworkCache(cfg *config.Config, key string) (string, bool) {
	for _, extension := range []string{".jpg", ".png", ".webp"} {
		path := filepath.Join(taterDiscoveryArtworkCacheDir(cfg), key+extension)
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= taterPlayerArtworkMaximumBytes {
			return path, true
		}
	}
	return "", false
}

func taterDiscoveryArtworkMissIsFresh(cfg *config.Config, key string) bool {
	path := filepath.Join(taterDiscoveryArtworkCacheDir(cfg), key+".missing")
	info, err := os.Stat(path)
	return err == nil && time.Since(info.ModTime()) < taterDiscoveryArtworkMissTTL
}

func markTaterDiscoveryArtworkMissing(cfg *config.Config, key string) {
	directory := taterDiscoveryArtworkCacheDir(cfg)
	if err := os.MkdirAll(directory, 0755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(directory, key+".missing"), []byte(time.Now().UTC().Format(time.RFC3339)), 0644)
}

func sendTaterDiscoveryArtwork(c *fiber.Ctx, path string) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		c.Set(fiber.HeaderContentType, "image/png")
	case ".webp":
		c.Set(fiber.HeaderContentType, "image/webp")
	default:
		c.Set(fiber.HeaderContentType, "image/jpeg")
	}
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.SendFile(path, false)
}

func (s *Server) handleTaterDiscoveryArtwork(c *fiber.Ctx) error {
	if playerToken := strings.TrimSpace(c.Query("player_token")); playerToken != "" && strings.TrimSpace(c.Get(fiber.HeaderAuthorization)) == "" {
		c.Request().Header.Set(fiber.HeaderAuthorization, "Bearer "+playerToken)
	}
	cfg, _, ok := s.taterAuthorizedConfig(c)
	if !ok {
		return nil
	}
	if !taterTMDBConfigured(cfg) {
		return RespondNotFound(c, "Discovery artwork", "TMDB is not configured")
	}
	title := cleanTaterText(c.Query("title"))
	if title == "" || len(title) > 240 {
		return RespondValidationError(c, "Discovery title is invalid", "")
	}
	mediaType := strings.ToLower(strings.TrimSpace(c.Query("media_type")))
	if mediaType != "movie" && mediaType != "series" && mediaType != "tv" && mediaType != "show" {
		return RespondValidationError(c, "Discovery media type is invalid", "")
	}
	year := taterDiscoveryYear(c.Query("year"))
	externalID := strings.ToLower(strings.TrimSpace(c.Query("external_id")))
	key := taterDiscoveryArtworkCacheKey(mediaType, title, year, externalID)
	if path, found := findTaterDiscoveryArtworkCache(cfg, key); found {
		return sendTaterDiscoveryArtwork(c, path)
	}
	if taterDiscoveryArtworkMissIsFresh(cfg, key) {
		return RespondNotFound(c, "Discovery artwork", title)
	}

	video := taterLocalVideoIndex{Title: title, Year: year, MediaType: "movie", LibraryType: "movies"}
	if mediaType == "series" || mediaType == "tv" || mediaType == "show" {
		video.MediaType = "show"
		video.LibraryType = "tv"
	}
	_, raw, contentType, err := findTaterRemoteVideoArtworkByExternalID(c.Context(), cfg, video, externalID)
	if err != nil {
		_, raw, contentType, err = findTaterRemoteVideoArtwork(c.Context(), cfg, video)
	}
	if err != nil {
		markTaterDiscoveryArtworkMissing(cfg, key)
		return RespondNotFound(c, "Discovery artwork", title)
	}
	path := filepath.Join(taterDiscoveryArtworkCacheDir(cfg), key+taterArtworkExtension(contentType))
	if err := writeTaterArtworkFile(path, raw); err != nil {
		return RespondInternalError(c, "Failed to cache Discovery artwork", err.Error())
	}
	_ = os.Remove(filepath.Join(taterDiscoveryArtworkCacheDir(cfg), key+".missing"))
	return sendTaterDiscoveryArtwork(c, path)
}

func (s *Server) handleLocalMediaVideoArtworkRefresh(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	request := taterLocalVideoArtworkRequest{}
	if err := c.BodyParser(&request); err != nil {
		return RespondValidationError(c, "Invalid artwork request", err.Error())
	}
	request.MediaID = strings.TrimSpace(request.MediaID)
	if request.MediaID == "" {
		return RespondValidationError(c, "Movie or TV show is required", "media_id is empty")
	}
	cfg := s.configManager.GetConfig()
	index, err := readTaterLocalLibraryIndex(cfg)
	if err != nil {
		return RespondValidationError(c, "Scan the local library before finding artwork", err.Error())
	}
	video, ok := findTaterLocalVideo(&index, request.MediaID)
	if !ok {
		return RespondNotFound(c, "Movie or TV show", request.MediaID)
	}
	if err := refreshTaterVideoArtwork(c.Context(), cfg, &index, video, request.Force); err != nil {
		return RespondValidationError(c, "No confident media match was found", err.Error())
	}
	if err := writeTaterJSON(taterLocalLibraryIndexPath(cfg), index); err != nil {
		return RespondInternalError(c, "Failed to save video artwork and metadata", err.Error())
	}
	return RespondSuccess(c, video)
}

func (s *Server) handleLocalMediaVideoArtwork(c *fiber.Ctx) error {
	if s.configManager == nil || s.configManager.GetConfig() == nil {
		return RespondServiceUnavailable(c, "Configuration management not available", "CONFIG_UNAVAILABLE")
	}
	mediaID := strings.TrimSpace(c.Query("media_id"))
	index, err := readTaterLocalLibraryIndex(s.configManager.GetConfig())
	if err != nil {
		return RespondNotFound(c, "Movie or TV artwork", mediaID)
	}
	video, ok := findTaterLocalVideo(&index, mediaID)
	if !ok || !video.HasArtwork || !taterVideoArtworkRefExists(s.configManager.GetConfig(), *video, video.ArtworkRef) {
		return RespondNotFound(c, "Movie or TV artwork", mediaID)
	}
	cat, _ := taterLocalMediaCategory(s.configManager.GetConfig(), video.CategoryID)
	roots := taterLocalMediaCategoryPaths(cat)
	path, err := safeLocalPath(roots[video.SourceIndex], video.ArtworkRef)
	if err != nil {
		return RespondNotFound(c, "Movie or TV artwork", mediaID)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		c.Set(fiber.HeaderContentType, "image/png")
	case ".webp":
		c.Set(fiber.HeaderContentType, "image/webp")
	default:
		c.Set(fiber.HeaderContentType, "image/jpeg")
	}
	c.Set(fiber.HeaderCacheControl, "private, max-age=86400")
	return c.SendFile(path, false)
}
