package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const taterMusicArtworkDownloadMaximumBytes = 16 * 1024 * 1024
const taterMusicBrainzMaximumAlbumGenres = 12

var (
	taterMusicBrainzBaseURL       = "https://musicbrainz.org/ws/2"
	taterCoverArtArchiveBaseURL   = "https://coverartarchive.org"
	taterMusicArtworkHTTPClient   = &http.Client{Timeout: 30 * time.Second}
	taterMusicBrainzRequestPacing = time.Second
	taterMusicBrainzPacer         = struct {
		sync.Mutex
		LastRequest time.Time
	}{}
)

type taterMusicArtworkCandidate struct {
	MusicBrainzID string
	Title         string
	Artist        string
	Score         int
	ImageURL      string
}

type taterMusicBrainzSearchResponse struct {
	ReleaseGroups []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Score        int    `json:"score"`
		ArtistCredit []struct {
			Name   string `json:"name"`
			Artist struct {
				Name     string `json:"name"`
				SortName string `json:"sort-name"`
			} `json:"artist"`
		} `json:"artist-credit"`
	} `json:"release-groups"`
}

type taterMusicBrainzReleaseGroupResponse struct {
	ID     string `json:"id"`
	Genres []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"genres"`
}

type taterCoverArtResponse struct {
	Images []struct {
		Image      string            `json:"image"`
		Thumbnails map[string]string `json:"thumbnails"`
		Front      bool              `json:"front"`
		Approved   bool              `json:"approved"`
	} `json:"images"`
}

func normalizeTaterMusicMatchText(value string) string {
	var builder strings.Builder
	space := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			builder.WriteRune(char)
			space = false
			continue
		}
		if !space && builder.Len() > 0 {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func taterMusicMatchArtist(credit []struct {
	Name   string `json:"name"`
	Artist struct {
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	} `json:"artist"`
}) string {
	parts := make([]string, 0, len(credit))
	for _, row := range credit {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = strings.TrimSpace(row.Artist.Name)
		}
		if name != "" {
			parts = append(parts, name)
		}
	}
	return strings.Join(parts, " & ")
}

func taterMusicArtworkSearchQuery(album taterLocalMusicAlbumIndex) string {
	escape := func(value string) string {
		value = strings.ReplaceAll(value, `\`, `\\`)
		return strings.ReplaceAll(value, `"`, `\"`)
	}
	title := escape(album.Title)
	artist := escape(album.Artist)
	if artist == "" {
		return fmt.Sprintf(`releasegroup:"%s"`, title)
	}
	return fmt.Sprintf(`releasegroup:"%s" AND artist:"%s"`, title, artist)
}

func waitForTaterMusicBrainz(ctx context.Context) error {
	taterMusicBrainzPacer.Lock()
	defer taterMusicBrainzPacer.Unlock()
	delay := taterMusicBrainzRequestPacing - time.Since(taterMusicBrainzPacer.LastRequest)
	if delay > 0 && taterMusicBrainzPacerRequestNeedsDelay() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	taterMusicBrainzPacer.LastRequest = time.Now()
	return nil
}

func taterMusicBrainzPacerRequestNeedsDelay() bool {
	return !taterMusicBrainzPacer.LastRequest.IsZero() && taterMusicBrainzRequestPacing > 0
}

func taterMusicArtworkRequest(ctx context.Context, rawURL string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "TaterTubeServer/1.4 (https://github.com/TaterTotterson/tater-tube-server)")
	return taterMusicArtworkHTTPClient.Do(request)
}

func searchTaterMusicArtworkCandidates(
	ctx context.Context,
	album taterLocalMusicAlbumIndex,
) ([]taterMusicArtworkCandidate, error) {
	if strings.TrimSpace(album.Title) == "" {
		return nil, fmt.Errorf("album title is empty")
	}
	if err := waitForTaterMusicBrainz(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", taterMusicArtworkSearchQuery(album))
	params.Set("fmt", "json")
	params.Set("limit", "8")
	response, err := taterMusicArtworkRequest(
		ctx,
		strings.TrimRight(taterMusicBrainzBaseURL, "/")+"/release-group/?"+params.Encode(),
	)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
	}
	var payload taterMusicBrainzSearchResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	wantedTitle := normalizeTaterMusicMatchText(album.Title)
	wantedArtist := normalizeTaterMusicMatchText(album.Artist)
	candidates := []taterMusicArtworkCandidate{}
	for _, group := range payload.ReleaseGroups {
		candidateTitle := normalizeTaterMusicMatchText(group.Title)
		artist := taterMusicMatchArtist(group.ArtistCredit)
		candidateArtist := normalizeTaterMusicMatchText(artist)
		if candidateTitle == "" || candidateTitle != wantedTitle {
			continue
		}
		if wantedArtist != "" && candidateArtist != wantedArtist &&
			!strings.Contains(candidateArtist, wantedArtist) && !strings.Contains(wantedArtist, candidateArtist) {
			continue
		}
		if group.Score < 80 {
			continue
		}
		candidates = append(candidates, taterMusicArtworkCandidate{
			MusicBrainzID: strings.TrimSpace(group.ID),
			Title:         strings.TrimSpace(group.Title),
			Artist:        artist,
			Score:         group.Score,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
	return candidates, nil
}

func fetchTaterMusicBrainzReleaseGroupGenres(ctx context.Context, musicBrainzID string) ([]string, error) {
	musicBrainzID = strings.TrimSpace(musicBrainzID)
	if musicBrainzID == "" {
		return nil, fmt.Errorf("MusicBrainz release group is empty")
	}
	if err := waitForTaterMusicBrainz(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("inc", "genres")
	params.Set("fmt", "json")
	response, err := taterMusicArtworkRequest(
		ctx,
		strings.TrimRight(taterMusicBrainzBaseURL, "/")+"/release-group/"+
			url.PathEscape(musicBrainzID)+"?"+params.Encode(),
	)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
	}
	var payload taterMusicBrainzReleaseGroupResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	sort.SliceStable(payload.Genres, func(i, j int) bool {
		if payload.Genres[i].Count != payload.Genres[j].Count {
			return payload.Genres[i].Count > payload.Genres[j].Count
		}
		return strings.ToLower(payload.Genres[i].Name) < strings.ToLower(payload.Genres[j].Name)
	})
	genres := []string{}
	for _, row := range payload.Genres {
		genres = mergeTaterMusicGenres(genres, []string{row.Name})
		if len(genres) >= taterMusicBrainzMaximumAlbumGenres {
			break
		}
	}
	return genres, nil
}

func resolveTaterCoverArtURL(ctx context.Context, musicBrainzID string) (string, error) {
	musicBrainzID = strings.TrimSpace(musicBrainzID)
	if musicBrainzID == "" {
		return "", fmt.Errorf("MusicBrainz release group is empty")
	}
	response, err := taterMusicArtworkRequest(
		ctx,
		strings.TrimRight(taterCoverArtArchiveBaseURL, "/")+"/release-group/"+url.PathEscape(musicBrainzID),
	)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return "", os.ErrNotExist
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Cover Art Archive returned HTTP %d", response.StatusCode)
	}
	var payload taterCoverArtResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&payload); err != nil {
		return "", err
	}
	for pass := 0; pass < 3; pass++ {
		for _, image := range payload.Images {
			if pass == 0 && (!image.Front || !image.Approved) {
				continue
			}
			if pass == 1 && !image.Front {
				continue
			}
			imageURL := strings.TrimSpace(image.Thumbnails["500"])
			if imageURL == "" {
				imageURL = strings.TrimSpace(image.Thumbnails["1200"])
			}
			if imageURL == "" {
				imageURL = strings.TrimSpace(image.Image)
			}
			if imageURL != "" {
				return imageURL, nil
			}
		}
	}
	return "", os.ErrNotExist
}

func findTaterRemoteAlbumArtwork(
	ctx context.Context,
	album taterLocalMusicAlbumIndex,
) (taterMusicArtworkCandidate, []byte, string, error) {
	candidates, err := searchTaterMusicArtworkCandidates(ctx, album)
	if err != nil {
		return taterMusicArtworkCandidate{}, nil, "", err
	}
	for _, candidate := range candidates {
		imageURL, err := resolveTaterCoverArtURL(ctx, candidate.MusicBrainzID)
		if err != nil {
			continue
		}
		candidate.ImageURL = imageURL
		response, err := taterMusicArtworkRequest(ctx, imageURL)
		if err != nil {
			continue
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, taterMusicArtworkDownloadMaximumBytes+1))
		response.Body.Close()
		if readErr != nil || len(raw) == 0 || len(raw) > taterMusicArtworkDownloadMaximumBytes {
			continue
		}
		contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
		if contentType == "" || !strings.HasPrefix(contentType, "image/") {
			contentType = strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(raw), ";")[0]))
		}
		if contentType != "image/jpeg" && contentType != "image/png" && contentType != "image/webp" {
			continue
		}
		return candidate, raw, contentType, nil
	}
	return taterMusicArtworkCandidate{}, nil, "", os.ErrNotExist
}

func writeTaterMusicArtworkCache(
	cfg *config.Config,
	albumID string,
	raw []byte,
	contentType string,
) (string, error) {
	ext := ".jpg"
	switch contentType {
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	}
	digest := sha256.Sum256([]byte(albumID))
	name := hex.EncodeToString(digest[:16]) + ext
	dir := taterMusicArtworkCacheDir(cfg)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tater-artwork-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, name)); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return name, nil
}

func refreshTaterLibraryArtworkStats(index *taterLocalLibraryIndex) {
	if index == nil {
		return
	}
	for i := range index.Categories {
		index.Categories[i].Stats.Artwork = 0
		index.Categories[i].Stats.MissingArtwork = 0
	}
	for _, album := range index.Albums {
		for i := range index.Categories {
			if index.Categories[i].ID != album.CategoryID {
				continue
			}
			if album.HasArtwork {
				index.Categories[i].Stats.Artwork++
			} else {
				index.Categories[i].Stats.MissingArtwork++
			}
			break
		}
	}
}

func refreshTaterAlbumArtwork(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	album *taterLocalMusicAlbumIndex,
	force bool,
) error {
	if album == nil {
		return fmt.Errorf("album is unavailable")
	}
	if album.HasArtwork && !force {
		return nil
	}
	candidate, raw, contentType, err := findTaterRemoteAlbumArtwork(ctx, *album)
	if err != nil {
		return err
	}
	genres, _ := fetchTaterMusicBrainzReleaseGroupGenres(ctx, candidate.MusicBrainzID)
	ref, err := writeTaterMusicArtworkCache(cfg, album.ID, raw, contentType)
	if err != nil {
		return err
	}
	store := readTaterMusicArtworkStore(cfg)
	updatedAt := time.Now().UTC()
	override := store.Items[album.ID]
	override.AlbumID = album.ID
	override.Source = "scraped"
	override.Ref = ref
	override.ContentType = contentType
	override.MusicBrainzID = candidate.MusicBrainzID
	override.Genres = mergeTaterMusicGenres(override.Genres, genres)
	override.Locked = force
	override.UpdatedAt = updatedAt
	store.Items[album.ID] = override
	if err := writeTaterMusicArtworkStore(cfg, store); err != nil {
		return err
	}
	album.Genres = mergeTaterMusicGenres(album.Genres, override.Genres)
	setTaterMusicAlbumArtwork(album, "scraped", ref, force, candidate.MusicBrainzID, updatedAt)
	album.ArtworkURL = taterLocalMusicAdminArtworkURL(*album)
	refreshTaterLibraryArtworkStats(index)
	return nil
}

func refreshTaterAlbumGenres(
	ctx context.Context,
	cfg *config.Config,
	album *taterLocalMusicAlbumIndex,
) error {
	if album == nil {
		return fmt.Errorf("album is unavailable")
	}
	candidates, err := searchTaterMusicArtworkCandidates(ctx, *album)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		genres, genreErr := fetchTaterMusicBrainzReleaseGroupGenres(ctx, candidate.MusicBrainzID)
		if genreErr != nil || len(genres) == 0 {
			continue
		}
		store := readTaterMusicArtworkStore(cfg)
		override := store.Items[album.ID]
		override.AlbumID = album.ID
		override.MusicBrainzID = candidate.MusicBrainzID
		override.Genres = mergeTaterMusicGenres(override.Genres, genres)
		if override.UpdatedAt.IsZero() {
			override.UpdatedAt = time.Now().UTC()
		}
		store.Items[album.ID] = override
		if err := writeTaterMusicArtworkStore(cfg, store); err != nil {
			return err
		}
		album.MusicBrainzID = candidate.MusicBrainzID
		album.Genres = mergeTaterMusicGenres(album.Genres, override.Genres)
		return nil
	}
	return os.ErrNotExist
}

func scrapeTaterMissingAlbumArtwork(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	progress func(processed, found int, message string),
) error {
	if index == nil {
		return nil
	}
	processed := 0
	found := 0
	for i := range index.Albums {
		if err := ctx.Err(); err != nil {
			return err
		}
		album := &index.Albums[i]
		needsArtwork := !album.HasArtwork && !album.ArtworkLocked
		needsGenres := len(album.Genres) == 0
		if !needsArtwork && !needsGenres {
			continue
		}
		processed++
		message := fmt.Sprintf("Finding artwork and genres for %s", album.Title)
		if progress != nil {
			progress(processed, found, message)
		}
		if needsArtwork {
			artworkErr := refreshTaterAlbumArtwork(ctx, cfg, index, album, false)
			if artworkErr == nil && album.HasArtwork {
				found++
			} else if needsGenres {
				// Genre metadata can still be available for releases that do not
				// have a usable Cover Art Archive image.
				_ = refreshTaterAlbumGenres(ctx, cfg, album)
			}
		} else if needsGenres {
			_ = refreshTaterAlbumGenres(ctx, cfg, album)
		}
		if progress != nil {
			progress(processed, found, message)
		}
	}
	refreshTaterLibraryArtworkStats(index)
	return nil
}
