package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

const (
	taterMusicCatalogDefaultLimit = 2000
	taterMusicCatalogMaximumLimit = 20000
)

func (s *Server) handleTaterMusicCatalog(c *fiber.Ctx) error {
	cfg, playerToken, ok := s.taterUsenetAuthorizedConfig(c)
	if !ok {
		return nil
	}

	baseURL := resolveBaseURL(c, "")
	tracks := []taterUsenetItem{}
	libraryNames := map[string]string{}
	for _, library := range taterLocalMusicLibraries(cfg) {
		libraryID := strings.TrimSpace(library.RatingKey)
		if libraryID == "" {
			continue
		}
		libraryNames[libraryID] = library.Title
		albums, err := taterLocalMusicAlbums(cfg, baseURL, playerToken, libraryID)
		if err != nil {
			continue
		}
		for _, album := range albums {
			albumTracks, err := taterLocalMusicTracks(cfg, baseURL, playerToken, album.RatingKey)
			if err != nil {
				continue
			}
			for i := range albumTracks {
				applyTaterMusicAlbumCatalogDetails(&albumTracks[i], album)
				tracks = append(tracks, albumTracks[i])
			}
		}
	}

	query := strings.ToLower(cleanTaterText(c.Query("q")))
	artistFilter := strings.ToLower(cleanTaterText(c.Query("artist")))
	albumFilter := strings.ToLower(cleanTaterText(c.Query("album")))
	genreFilter := strings.ToLower(cleanTaterText(c.Query("genre")))
	filtered := make([]taterUsenetItem, 0, len(tracks))
	for _, track := range tracks {
		if query != "" && !taterMusicTrackContains(track, query) {
			continue
		}
		if artistFilter != "" &&
			!strings.Contains(strings.ToLower(track.Artist+" "+track.AlbumArtist), artistFilter) {
			continue
		}
		if albumFilter != "" && !strings.Contains(strings.ToLower(track.Album), albumFilter) {
			continue
		}
		if genreFilter != "" && !taterMusicGenresContain(track.Genres, track.Genre, genreFilter) {
			continue
		}
		filtered = append(filtered, track)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		left := filtered[i]
		right := filtered[j]
		if order := strings.Compare(
			strings.ToLower(left.AlbumArtist+" "+left.Artist),
			strings.ToLower(right.AlbumArtist+" "+right.Artist),
		); order != 0 {
			return order < 0
		}
		if order := strings.Compare(strings.ToLower(left.Album), strings.ToLower(right.Album)); order != 0 {
			return order < 0
		}
		if left.Index != right.Index {
			return left.Index < right.Index
		}
		return strings.ToLower(left.Title) < strings.ToLower(right.Title)
	})

	artists, albums, genres := taterMusicFacets(filtered)
	total := len(filtered)
	offset := queryInt(c, "offset", 0, 0, max(total, 1))
	limit := queryInt(
		c,
		"limit",
		taterMusicCatalogDefaultLimit,
		1,
		taterMusicCatalogMaximumLimit,
	)
	end := min(total, offset+limit)
	page := []taterUsenetItem{}
	if offset < total {
		page = filtered[offset:end]
	}
	return RespondSuccess(c, fiber.Map{
		"catalog_id":   taterMusicCatalogID(tracks),
		"tracks":       page,
		"total":        total,
		"offset":       offset,
		"limit":        limit,
		"artists":      artists,
		"albums":       albums,
		"genres":       genres,
		"libraries":    libraryNames,
		"generated_at": time.Now().UTC(),
	})
}

func applyTaterMusicAlbumCatalogDetails(track *taterUsenetItem, album taterUsenetItem) {
	if track.Album == "" {
		track.Album = album.Title
	}
	if track.AlbumArtist == "" {
		track.AlbumArtist = album.AlbumArtist
	}
	if track.Artist == "" {
		track.Artist = album.Artist
	}
	track.Genres = mergeTaterMusicGenres(track.Genres, album.Genres)
	if len(track.Genres) > 0 {
		track.Genre = strings.Join(track.Genres, ", ")
	}
	if strings.TrimSpace(album.Poster) != "" && album.HasArtwork {
		// The album index resolves manual, embedded, local, and scraped artwork in
		// priority order. Publish that one resolved image for every track so all
		// downstream clients display a consistent album cover.
		track.Poster = album.Poster
		track.HasArtwork = true
	}
}

func taterMusicTrackContains(track taterUsenetItem, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		track.Title,
		track.Artist,
		track.AlbumArtist,
		track.Album,
		track.Genre,
		strings.Join(track.Genres, " "),
		track.Date,
	}, " "))
	for _, token := range strings.Fields(query) {
		if !strings.Contains(haystack, token) {
			return false
		}
	}
	return true
}

func taterMusicGenresContain(genres []string, fallback string, query string) bool {
	for _, genre := range append(append([]string(nil), genres...), fallback) {
		if strings.Contains(strings.ToLower(genre), query) {
			return true
		}
	}
	return false
}

func taterMusicFacets(tracks []taterUsenetItem) ([]string, []string, []string) {
	artistSet := map[string]string{}
	albumSet := map[string]string{}
	genreSet := map[string]string{}
	for _, track := range tracks {
		artist := cleanTaterText(track.AlbumArtist)
		if artist == "" {
			artist = cleanTaterText(track.Artist)
		}
		if artist != "" {
			artistSet[strings.ToLower(artist)] = artist
		}
		if album := cleanTaterText(track.Album); album != "" {
			albumSet[strings.ToLower(album)] = album
		}
		for _, genre := range mergeTaterMusicGenres(track.Genres, splitTaterMusicGenres(track.Genre)) {
			genreSet[strings.ToLower(genre)] = genre
		}
	}
	return sortedTaterMusicFacet(artistSet), sortedTaterMusicFacet(albumSet), sortedTaterMusicFacet(genreSet)
}

func sortedTaterMusicFacet(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return strings.ToLower(result[i]) < strings.ToLower(result[j])
	})
	return result
}

func taterMusicCatalogID(tracks []taterUsenetItem) string {
	hash := sha256.New()
	for _, track := range tracks {
		_, _ = fmt.Fprintf(
			hash,
			"%s\x00%d\x00%d\x00%s\x00%s\x00%s\n",
			track.RatingKey,
			track.SizeBytes,
			track.ModifiedUnix,
			track.Title,
			track.Artist,
			strconv.Itoa(track.Index),
		)
	}
	return hex.EncodeToString(hash.Sum(nil)[:12])
}
