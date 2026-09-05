package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
const taterAudioDBPublicAPIKey = "123"
const taterArtistConsensusMaximumGenres = 3

var (
	taterMusicBrainzBaseURL       = "https://musicbrainz.org/ws/2"
	taterCoverArtArchiveBaseURL   = "https://coverartarchive.org"
	taterAudioDBBaseURL           = "https://www.theaudiodb.com/api/v1/json"
	taterMusicArtworkHTTPClient   = &http.Client{Timeout: 30 * time.Second}
	taterMusicBrainzRequestPacing = time.Second
	taterAudioDBRequestPacing     = 2 * time.Second
	taterMusicBrainzPacer         = struct {
		sync.Mutex
		LastRequest time.Time
	}{}
	taterAudioDBPacer = struct {
		sync.Mutex
		LastRequest time.Time
	}{}
)

type taterMusicArtworkCandidate struct {
	MusicBrainzID string
	ArtistID      string
	Title         string
	Artist        string
	Score         int
	ImageURL      string
	Genres        []string
}

type taterMusicEnrichmentProgress struct {
	AlbumsProcessed int
	ArtworkFound    int
	GenreMatches    int
	GenreUnmatched  int
	Message         string
}

type taterMusicArtistGenreProfile struct {
	Albums int
	Counts map[string]int
	Names  map[string]string
}

type taterMusicBrainzArtistCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
	} `json:"artist"`
}

type taterMusicBrainzGenre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type taterMusicBrainzSearchResponse struct {
	ReleaseGroups []struct {
		ID           string                         `json:"id"`
		Title        string                         `json:"title"`
		Score        int                            `json:"score"`
		ArtistCredit []taterMusicBrainzArtistCredit `json:"artist-credit"`
	} `json:"release-groups"`
}

type taterMusicBrainzReleaseGroupResponse struct {
	ID     string                  `json:"id"`
	Genres []taterMusicBrainzGenre `json:"genres"`
}

type taterMusicBrainzArtistSearchResponse struct {
	Artists []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		SortName string `json:"sort-name"`
		Score    int    `json:"score"`
	} `json:"artists"`
}

type taterMusicBrainzArtistResponse struct {
	ID     string                  `json:"id"`
	Genres []taterMusicBrainzGenre `json:"genres"`
}

type taterAudioDBAlbum struct {
	ID                    string `json:"idAlbum"`
	Title                 string `json:"strAlbum"`
	Artist                string `json:"strArtist"`
	Genre                 string `json:"strGenre"`
	Style                 string `json:"strStyle"`
	ArtworkURL            string `json:"strAlbumThumb"`
	ArtworkHighQualityURL string `json:"strAlbumThumbHQ"`
	MusicBrainzID         string `json:"strMusicBrainzID"`
}

type taterAudioDBAlbumResponse struct {
	Albums []taterAudioDBAlbum `json:"album"`
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

func taterMusicMatchArtist(credit []taterMusicBrainzArtistCredit) string {
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

func taterMusicMatchArtistID(credit []taterMusicBrainzArtistCredit, wantedArtist string) string {
	wanted := normalizeTaterMusicMatchText(wantedArtist)
	for _, row := range credit {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			name = strings.TrimSpace(row.Artist.Name)
		}
		candidate := normalizeTaterMusicMatchText(name)
		if candidate != "" && (wanted == "" || candidate == wanted ||
			strings.Contains(candidate, wanted) || strings.Contains(wanted, candidate)) {
			return strings.TrimSpace(row.Artist.ID)
		}
	}
	if len(credit) == 1 {
		return strings.TrimSpace(credit[0].Artist.ID)
	}
	return ""
}

func taterMusicSimplifiedAlbumTitle(value string) string {
	title := strings.TrimSpace(value)
	for {
		if len(title) < 3 {
			return title
		}
		closing := title[len(title)-1]
		opening := byte(0)
		switch closing {
		case ')':
			opening = '('
		case ']':
			opening = '['
		default:
			return title
		}
		start := strings.LastIndexByte(title, opening)
		if start <= 0 {
			return title
		}
		suffix := normalizeTaterMusicMatchText(title[start+1 : len(title)-1])
		isEdition := false
		for _, marker := range []string{
			"acoustic", "anniversary", "bonus", "deluxe", "edition", "expanded",
			"instrumental", "instrumentals", "live", "remaster", "remastered", "remix", "remixes",
			"sessions", "version", "versions",
		} {
			if strings.Contains(" "+suffix+" ", " "+marker+" ") {
				isEdition = true
				break
			}
		}
		if !isEdition {
			return title
		}
		title = strings.TrimSpace(title[:start])
	}
}

func taterMusicArtworkSearchQuery(album taterLocalMusicAlbumIndex, titleOverride ...string) string {
	escape := func(value string) string {
		value = strings.ReplaceAll(value, `\`, `\\`)
		return strings.ReplaceAll(value, `"`, `\"`)
	}
	title := strings.TrimSpace(album.Title)
	if len(titleOverride) > 0 && strings.TrimSpace(titleOverride[0]) != "" {
		title = strings.TrimSpace(titleOverride[0])
	}
	title = escape(title)
	artist := escape(album.Artist)
	if artist == "" {
		return fmt.Sprintf(`releasegroup:"%s"`, title)
	}
	return fmt.Sprintf(`releasegroup:"%s" AND artist:"%s"`, title, artist)
}

func taterMusicArtistSearchQuery(artist string) string {
	escaped := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(artist), `\`, `\\`), `"`, `\"`)
	return fmt.Sprintf(`artist:"%s"`, escaped)
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

func taterAudioDBEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.LocalMedia.AudioDBEnabled != nil && *cfg.LocalMedia.AudioDBEnabled
}

func taterAudioDBAPIKey(cfg *config.Config) string {
	if cfg != nil {
		if key := strings.TrimSpace(cfg.LocalMedia.AudioDBAPIKey); key != "" {
			return key
		}
	}
	return taterAudioDBPublicAPIKey
}

func waitForTaterAudioDB(ctx context.Context) error {
	taterAudioDBPacer.Lock()
	defer taterAudioDBPacer.Unlock()
	delay := taterAudioDBRequestPacing - time.Since(taterAudioDBPacer.LastRequest)
	if delay > 0 && !taterAudioDBPacer.LastRequest.IsZero() && taterAudioDBRequestPacing > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	taterAudioDBPacer.LastRequest = time.Now()
	return nil
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

func fetchTaterAudioDBAlbum(
	ctx context.Context,
	cfg *config.Config,
	musicBrainzReleaseGroupID string,
) (taterAudioDBAlbum, error) {
	if !taterAudioDBEnabled(cfg) {
		return taterAudioDBAlbum{}, os.ErrNotExist
	}
	musicBrainzReleaseGroupID = strings.TrimSpace(musicBrainzReleaseGroupID)
	if musicBrainzReleaseGroupID == "" {
		return taterAudioDBAlbum{}, os.ErrNotExist
	}
	if err := waitForTaterAudioDB(ctx); err != nil {
		return taterAudioDBAlbum{}, err
	}
	params := url.Values{}
	params.Set("i", musicBrainzReleaseGroupID)
	rawURL := strings.TrimRight(taterAudioDBBaseURL, "/") + "/" +
		url.PathEscape(taterAudioDBAPIKey(cfg)) + "/album-mb.php?" + params.Encode()
	response, err := taterMusicArtworkRequest(ctx, rawURL)
	if err != nil {
		return taterAudioDBAlbum{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return taterAudioDBAlbum{}, os.ErrNotExist
	}
	if response.StatusCode != http.StatusOK {
		return taterAudioDBAlbum{}, fmt.Errorf("TheAudioDB returned HTTP %d", response.StatusCode)
	}
	payload := taterAudioDBAlbumResponse{}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&payload); err != nil {
		return taterAudioDBAlbum{}, err
	}
	wantedID := strings.ToLower(musicBrainzReleaseGroupID)
	for _, album := range payload.Albums {
		candidateID := strings.ToLower(strings.TrimSpace(album.MusicBrainzID))
		if candidateID != "" && candidateID != wantedID {
			continue
		}
		return album, nil
	}
	return taterAudioDBAlbum{}, os.ErrNotExist
}

func taterAudioDBAlbumGenres(album taterAudioDBAlbum) []string {
	genres := splitTaterMusicGenres(album.Genre)
	styleParts := strings.FieldsFunc(album.Style, func(char rune) bool {
		return char == ';' || char == ',' || char == '|' || char == '/'
	})
	return mergeTaterMusicGenres(genres, taterMusicBroadGenres(styleParts))
}

func taterAudioDBAlbumArtworkURL(album taterAudioDBAlbum) string {
	if value := strings.TrimSpace(album.ArtworkHighQualityURL); value != "" {
		return value
	}
	return strings.TrimSpace(album.ArtworkURL)
}

func searchTaterMusicReleaseCandidates(
	ctx context.Context,
	album taterLocalMusicAlbumIndex,
	includeSimplifiedTitle bool,
) ([]taterMusicArtworkCandidate, error) {
	if strings.TrimSpace(album.Title) == "" {
		return nil, fmt.Errorf("album title is empty")
	}
	wantedArtist := normalizeTaterMusicMatchText(album.Artist)
	searchTitles := []string{strings.TrimSpace(album.Title)}
	if simplified := taterMusicSimplifiedAlbumTitle(album.Title); includeSimplifiedTitle && simplified != "" &&
		normalizeTaterMusicMatchText(simplified) != normalizeTaterMusicMatchText(album.Title) {
		searchTitles = append(searchTitles, simplified)
	}
	for _, searchTitle := range searchTitles {
		if err := waitForTaterMusicBrainz(ctx); err != nil {
			return nil, err
		}
		params := url.Values{}
		params.Set("query", taterMusicArtworkSearchQuery(album, searchTitle))
		params.Set("fmt", "json")
		params.Set("limit", "8")
		response, err := taterMusicArtworkRequest(
			ctx,
			strings.TrimRight(taterMusicBrainzBaseURL, "/")+"/release-group/?"+params.Encode(),
		)
		if err != nil {
			return nil, err
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return nil, fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
		}
		var payload taterMusicBrainzSearchResponse
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&payload)
		response.Body.Close()
		if decodeErr != nil {
			return nil, decodeErr
		}
		wantedTitle := normalizeTaterMusicMatchText(searchTitle)
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
				ArtistID:      taterMusicMatchArtistID(group.ArtistCredit, album.Artist),
				Title:         strings.TrimSpace(group.Title),
				Artist:        artist,
				Score:         group.Score,
			})
		}
		sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })
		if len(candidates) > 0 {
			return candidates, nil
		}
	}
	return []taterMusicArtworkCandidate{}, nil
}

func searchTaterMusicArtworkCandidates(
	ctx context.Context,
	album taterLocalMusicAlbumIndex,
) ([]taterMusicArtworkCandidate, error) {
	return searchTaterMusicReleaseCandidates(ctx, album, false)
}

func searchTaterMusicGenreCandidates(
	ctx context.Context,
	album taterLocalMusicAlbumIndex,
) ([]taterMusicArtworkCandidate, error) {
	return searchTaterMusicReleaseCandidates(ctx, album, true)
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
	return taterMusicBrainzGenres(payload.Genres), nil
}

func taterMusicBrainzGenres(values []taterMusicBrainzGenre) []string {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Count != values[j].Count {
			return values[i].Count > values[j].Count
		}
		return strings.ToLower(values[i].Name) < strings.ToLower(values[j].Name)
	})
	genres := []string{}
	for _, row := range values {
		genres = mergeTaterMusicGenres(genres, []string{row.Name})
		if len(genres) >= taterMusicBrainzMaximumAlbumGenres {
			break
		}
	}
	return genres
}

func fetchTaterMusicBrainzArtistGenres(ctx context.Context, artistID string) ([]string, error) {
	artistID = strings.TrimSpace(artistID)
	if artistID == "" {
		return nil, fmt.Errorf("MusicBrainz artist is empty")
	}
	if err := waitForTaterMusicBrainz(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("inc", "genres")
	params.Set("fmt", "json")
	response, err := taterMusicArtworkRequest(
		ctx,
		strings.TrimRight(taterMusicBrainzBaseURL, "/")+"/artist/"+
			url.PathEscape(artistID)+"?"+params.Encode(),
	)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
	}
	var payload taterMusicBrainzArtistResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	return taterMusicBrainzGenres(payload.Genres), nil
}

func taterMusicArtistGenreFallbackAllowed(artist string) bool {
	key := normalizeTaterMusicMatchText(artist)
	if key == "" {
		return false
	}
	switch key {
	case "artist", "instrumental", "music", "original soundtrack", "soundtrack",
		"unknown", "unknown artist", "various", "various artist", "various artists":
		return false
	default:
		return true
	}
}

func taterMusicAlbumArtistKey(album taterLocalMusicAlbumIndex) string {
	artist := strings.TrimSpace(album.AlbumArtist)
	if artist == "" {
		artist = strings.TrimSpace(album.Artist)
	}
	if !taterMusicArtistGenreFallbackAllowed(artist) {
		return ""
	}
	return normalizeTaterMusicMatchText(artist)
}

func buildTaterMusicArtistGenreProfiles(index *taterLocalLibraryIndex) map[string]taterMusicArtistGenreProfile {
	profiles := map[string]taterMusicArtistGenreProfile{}
	if index == nil {
		return profiles
	}
	for _, album := range index.Albums {
		artistKey := taterMusicAlbumArtistKey(album)
		genres := taterMusicBroadGenres(album.Genres)
		if artistKey == "" || len(genres) == 0 {
			continue
		}
		profile := profiles[artistKey]
		if profile.Counts == nil {
			profile.Counts = map[string]int{}
			profile.Names = map[string]string{}
		}
		profile.Albums++
		for _, genre := range genres {
			key := normalizeTaterMusicGenreKey(genre)
			if key == "" {
				continue
			}
			profile.Counts[key]++
			profile.Names[key] = genre
		}
		profiles[artistKey] = profile
	}
	return profiles
}

func taterMusicArtistConsensusGenres(
	profiles map[string]taterMusicArtistGenreProfile,
	album taterLocalMusicAlbumIndex,
) []string {
	profile, ok := profiles[taterMusicAlbumArtistKey(album)]
	if !ok || profile.Albums == 0 {
		return nil
	}
	minimum := 1
	if profile.Albums == 2 {
		minimum = 2
	} else if profile.Albums > 2 {
		minimum = (profile.Albums + 1) / 2
	}
	type genreCount struct {
		Name  string
		Count int
	}
	matches := []genreCount{}
	for key, count := range profile.Counts {
		if count >= minimum {
			matches = append(matches, genreCount{Name: profile.Names[key], Count: count})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Count != matches[j].Count {
			return matches[i].Count > matches[j].Count
		}
		return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
	})
	genres := make([]string, 0, min(len(matches), taterArtistConsensusMaximumGenres))
	for _, match := range matches {
		genres = append(genres, match.Name)
		if len(genres) >= taterArtistConsensusMaximumGenres {
			break
		}
	}
	return genres
}

func searchTaterMusicArtistIDs(ctx context.Context, artist string) ([]string, error) {
	if !taterMusicArtistGenreFallbackAllowed(artist) {
		return nil, os.ErrNotExist
	}
	if err := waitForTaterMusicBrainz(ctx); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("query", taterMusicArtistSearchQuery(artist))
	params.Set("fmt", "json")
	params.Set("limit", "8")
	response, err := taterMusicArtworkRequest(
		ctx,
		strings.TrimRight(taterMusicBrainzBaseURL, "/")+"/artist/?"+params.Encode(),
	)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MusicBrainz returned HTTP %d", response.StatusCode)
	}
	var payload taterMusicBrainzArtistSearchResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 4*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	wanted := normalizeTaterMusicMatchText(artist)
	type artistMatch struct {
		ID    string
		Score int
	}
	matches := []artistMatch{}
	for _, candidate := range payload.Artists {
		if candidate.Score < 90 || normalizeTaterMusicMatchText(candidate.Name) != wanted {
			continue
		}
		if id := strings.TrimSpace(candidate.ID); id != "" {
			matches = append(matches, artistMatch{ID: id, Score: candidate.Score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Score > matches[j].Score })
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.ID)
	}
	return result, nil
}

func findTaterMusicArtistGenres(
	ctx context.Context,
	artist string,
	preferredArtistID string,
) ([]string, error) {
	if !taterMusicArtistGenreFallbackAllowed(artist) {
		return nil, os.ErrNotExist
	}
	seen := map[string]bool{}
	if artistID := strings.TrimSpace(preferredArtistID); artistID != "" {
		seen[artistID] = true
		genres, err := fetchTaterMusicBrainzArtistGenres(ctx, artistID)
		if err == nil && len(genres) > 0 {
			return genres, nil
		}
	}
	artistIDs, err := searchTaterMusicArtistIDs(ctx, artist)
	if err != nil {
		return nil, err
	}
	for _, artistID := range artistIDs {
		if artistID == "" || seen[artistID] {
			continue
		}
		seen[artistID] = true
		genres, err := fetchTaterMusicBrainzArtistGenres(ctx, artistID)
		if err == nil && len(genres) > 0 {
			return genres, nil
		}
	}
	return nil, os.ErrNotExist
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
	cfg *config.Config,
	album taterLocalMusicAlbumIndex,
) (taterMusicArtworkCandidate, []byte, string, error) {
	candidates, err := searchTaterMusicArtworkCandidates(ctx, album)
	if err != nil {
		return taterMusicArtworkCandidate{}, nil, "", err
	}
	for _, candidate := range candidates {
		imageURL, coverArtErr := resolveTaterCoverArtURL(ctx, candidate.MusicBrainzID)
		if coverArtErr == nil {
			candidate.ImageURL = imageURL
			raw, contentType, downloadErr := downloadTaterRemoteArtwork(ctx, imageURL)
			if downloadErr == nil {
				return candidate, raw, contentType, nil
			}
		}
		audioDBAlbum, audioDBErr := fetchTaterAudioDBAlbum(ctx, cfg, candidate.MusicBrainzID)
		if audioDBErr != nil {
			continue
		}
		candidate.Genres = taterAudioDBAlbumGenres(audioDBAlbum)
		imageURL = taterAudioDBAlbumArtworkURL(audioDBAlbum)
		if imageURL == "" {
			continue
		}
		candidate.ImageURL = imageURL
		raw, contentType, err := downloadTaterRemoteArtwork(ctx, imageURL)
		if err != nil {
			continue
		}
		return candidate, raw, contentType, nil
	}
	return taterMusicArtworkCandidate{}, nil, "", os.ErrNotExist
}

func downloadTaterRemoteArtwork(ctx context.Context, imageURL string) ([]byte, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, "", os.ErrNotExist
	}
	response, err := taterMusicArtworkRequest(ctx, parsed.String())
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
	for _, video := range index.Videos {
		for i := range index.Categories {
			if index.Categories[i].ID != video.CategoryID {
				continue
			}
			if video.HasArtwork {
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
	candidate, raw, contentType, err := findTaterRemoteAlbumArtwork(ctx, cfg, *album)
	if err != nil {
		return err
	}
	genres, _ := fetchTaterMusicBrainzReleaseGroupGenres(ctx, candidate.MusicBrainzID)
	if len(genres) == 0 {
		genres = candidate.Genres
	}
	if len(genres) == 0 {
		audioDBAlbum, audioDBErr := fetchTaterAudioDBAlbum(ctx, cfg, candidate.MusicBrainzID)
		if audioDBErr == nil {
			genres = taterAudioDBAlbumGenres(audioDBAlbum)
		}
	}
	if len(genres) == 0 {
		genres, _ = findTaterMusicArtistGenres(ctx, album.Artist, candidate.ArtistID)
	}
	ref, sidecarAvailable, err := writeTaterMusicArtworkSidecar(cfg, *album, raw, contentType)
	storage := "library"
	if err != nil {
		return err
	}
	if !sidecarAvailable {
		storage = "cache"
		ref, err = writeTaterMusicArtworkCache(cfg, album.ID, raw, contentType)
		if err != nil {
			return err
		}
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
	override.Storage = storage
	override.Locked = force
	override.UpdatedAt = updatedAt
	store.Items[album.ID] = override
	if err := writeTaterMusicArtworkStore(cfg, store); err != nil {
		return err
	}
	album.Genres = mergeTaterMusicGenres(album.Genres, override.Genres)
	setTaterMusicAlbumArtwork(album, "scraped", ref, force, candidate.MusicBrainzID, updatedAt)
	album.ArtworkStorage = storage
	album.ArtworkURL = taterLocalMusicAdminArtworkURL(*album)
	refreshTaterLibraryArtworkStats(index)
	return nil
}

func persistTaterAlbumGenres(
	cfg *config.Config,
	album *taterLocalMusicAlbumIndex,
	genres []string,
	releaseGroupID string,
) error {
	if album == nil || len(genres) == 0 {
		return os.ErrNotExist
	}
	store := readTaterMusicArtworkStore(cfg)
	override := store.Items[album.ID]
	override.AlbumID = album.ID
	if releaseGroupID = strings.TrimSpace(releaseGroupID); releaseGroupID != "" {
		override.MusicBrainzID = releaseGroupID
		album.MusicBrainzID = releaseGroupID
	}
	override.Genres = mergeTaterMusicGenres(override.Genres, genres)
	override.UpdatedAt = time.Now().UTC()
	store.Items[album.ID] = override
	if err := writeTaterMusicArtworkStore(cfg, store); err != nil {
		return err
	}
	album.Genres = mergeTaterMusicGenres(album.Genres, override.Genres)
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
	candidates, err := searchTaterMusicGenreCandidates(ctx, *album)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		genres, genreErr := fetchTaterMusicBrainzReleaseGroupGenres(ctx, candidate.MusicBrainzID)
		if genreErr != nil || len(genres) == 0 {
			audioDBAlbum, audioDBErr := fetchTaterAudioDBAlbum(ctx, cfg, candidate.MusicBrainzID)
			if audioDBErr == nil {
				genres = taterAudioDBAlbumGenres(audioDBAlbum)
				genreErr = nil
			}
		}
		if genreErr != nil || len(genres) == 0 {
			genres, genreErr = findTaterMusicArtistGenres(ctx, album.Artist, candidate.ArtistID)
		}
		if genreErr != nil || len(genres) == 0 {
			continue
		}
		if err := persistTaterAlbumGenres(cfg, album, genres, candidate.MusicBrainzID); err != nil {
			return err
		}
		return nil
	}
	genres, artistErr := findTaterMusicArtistGenres(ctx, album.Artist, "")
	if artistErr == nil && len(genres) > 0 {
		return persistTaterAlbumGenres(cfg, album, genres, "")
	}
	return os.ErrNotExist
}

func scrapeTaterMissingAlbumArtwork(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	progress func(taterMusicEnrichmentProgress),
) error {
	if index == nil {
		return nil
	}
	status := taterMusicEnrichmentProgress{}
	unmatchedAlbumIndexes := []int{}
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
		status.AlbumsProcessed++
		message := fmt.Sprintf("Finding artwork and genres for %s", album.Title)
		status.Message = message
		if progress != nil {
			progress(status)
		}
		var genreErr error
		if needsArtwork {
			artworkErr := refreshTaterAlbumArtwork(ctx, cfg, index, album, false)
			if artworkErr == nil && album.HasArtwork {
				status.ArtworkFound++
			} else if needsGenres {
				// Genre metadata can still be available for releases that do not
				// have a usable Cover Art Archive image.
				genreErr = refreshTaterAlbumGenres(ctx, cfg, album)
			}
		} else if needsGenres {
			genreErr = refreshTaterAlbumGenres(ctx, cfg, album)
		}
		if needsGenres {
			if len(album.Genres) > 0 {
				status.GenreMatches++
			} else {
				status.GenreUnmatched++
				unmatchedAlbumIndexes = append(unmatchedAlbumIndexes, i)
				slog.Debug(
					"Music genre enrichment did not find a confident match",
					"album", album.Title,
					"artist", album.Artist,
					"error", genreErr,
				)
			}
		}
		if progress != nil {
			progress(status)
		}
	}
	profiles := buildTaterMusicArtistGenreProfiles(index)
	for _, albumIndex := range unmatchedAlbumIndexes {
		if err := ctx.Err(); err != nil {
			return err
		}
		album := &index.Albums[albumIndex]
		genres := taterMusicArtistConsensusGenres(profiles, *album)
		if len(genres) == 0 {
			continue
		}
		if err := persistTaterAlbumGenres(cfg, album, genres, album.MusicBrainzID); err != nil {
			continue
		}
		status.GenreMatches++
		status.GenreUnmatched--
		status.Message = fmt.Sprintf("Applied %s's known genres to %s", album.Artist, album.Title)
		if progress != nil {
			progress(status)
		}
	}
	refreshTaterLibraryArtworkStats(index)
	if status.GenreUnmatched > 0 {
		slog.Warn(
			"Music genre enrichment completed with unmatched albums",
			"albums_processed", status.AlbumsProcessed,
			"genre_matches", status.GenreMatches,
			"genre_unmatched", status.GenreUnmatched,
			"artwork_found", status.ArtworkFound,
		)
	} else if status.AlbumsProcessed > 0 {
		slog.Info(
			"Music genre enrichment completed",
			"albums_processed", status.AlbumsProcessed,
			"genre_matches", status.GenreMatches,
			"artwork_found", status.ArtworkFound,
		)
	}
	return nil
}
