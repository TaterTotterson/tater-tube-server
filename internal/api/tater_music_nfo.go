package api

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type taterMusicLocalNFO struct {
	Title                     string   `xml:"title"`
	Album                     string   `xml:"album"`
	Artist                    string   `xml:"artist"`
	AlbumArtist               string   `xml:"albumartist"`
	SortTitle                 string   `xml:"sorttitle"`
	SortName                  string   `xml:"sortname"`
	Plot                      string   `xml:"plot"`
	Biography                 string   `xml:"biography"`
	Review                    string   `xml:"review"`
	Year                      string   `xml:"year"`
	Premiered                 string   `xml:"premiered"`
	ReleaseDate               string   `xml:"releasedate"`
	Genres                    []string `xml:"genre"`
	Styles                    []string `xml:"style"`
	MusicBrainzAlbumID        string   `xml:"musicbrainzalbumid"`
	MusicBrainzReleaseGroupID string   `xml:"musicbrainzreleasegroupid"`
	MusicBrainzAlbumArtistID  string   `xml:"musicbrainzalbumartistid"`
	MusicBrainzArtistID       string   `xml:"musicbrainzartistid"`
}

type taterMusicNFOOutput struct {
	XMLName                   xml.Name
	Title                     string   `xml:"title"`
	Artist                    string   `xml:"artist,omitempty"`
	AlbumArtist               string   `xml:"albumartist,omitempty"`
	SortTitle                 string   `xml:"sorttitle,omitempty"`
	Plot                      string   `xml:"plot,omitempty"`
	Biography                 string   `xml:"biography,omitempty"`
	Year                      string   `xml:"year,omitempty"`
	Premiered                 string   `xml:"premiered,omitempty"`
	DateAdded                 string   `xml:"dateadded,omitempty"`
	LockData                  bool     `xml:"lockdata"`
	Genres                    []string `xml:"genre,omitempty"`
	Styles                    []string `xml:"style,omitempty"`
	MusicBrainzReleaseGroupID string   `xml:"musicbrainzreleasegroupid,omitempty"`
	MusicBrainzAlbumArtistID  string   `xml:"musicbrainzalbumartistid,omitempty"`
	MusicBrainzArtistID       string   `xml:"musicbrainzartistid,omitempty"`
}

type taterMusicNFOPaths struct {
	AlbumPath               string
	AlbumRef                string
	ArtistPath              string
	ArtistRef               string
	ArtistMetadataAvailable bool
}

func taterMusicAlbumArtist(album taterLocalMusicAlbumIndex) string {
	if value := cleanTaterText(album.AlbumArtist); value != "" {
		return value
	}
	return cleanTaterText(album.Artist)
}

func taterMusicNFOFilePaths(
	cfg *config.Config,
	album taterLocalMusicAlbumIndex,
) (taterMusicNFOPaths, error) {
	cat, ok := taterLocalMediaCategory(cfg, album.CategoryID)
	if !ok {
		return taterMusicNFOPaths{}, fmt.Errorf("music library is unavailable")
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if album.SourceIndex < 0 || album.SourceIndex >= len(roots) {
		return taterMusicNFOPaths{}, fmt.Errorf("music library source is unavailable")
	}
	albumRel := cleanLocalRelativePath(album.Path)
	if albumRel == "" {
		return taterMusicNFOPaths{}, fmt.Errorf("album folder is unavailable")
	}
	albumDirectory, err := safeLocalPath(roots[album.SourceIndex], albumRel)
	if err != nil {
		return taterMusicNFOPaths{}, err
	}
	paths := taterMusicNFOPaths{
		AlbumPath: filepath.Join(albumDirectory, "album.nfo"),
		AlbumRef:  cleanLocalRelativePath(filepath.ToSlash(filepath.Join(albumRel, "album.nfo"))),
	}

	artistRel := cleanLocalRelativePath(filepath.ToSlash(filepath.Dir(albumRel)))
	if artistRel == "" {
		return paths, nil
	}
	artistName := normalizeTaterMusicMatchText(taterMusicAlbumArtist(album))
	artistFolder := normalizeTaterMusicMatchText(filepath.Base(filepath.FromSlash(artistRel)))
	if artistName == "" || artistFolder == "" ||
		(artistName != artistFolder && !strings.Contains(artistName, artistFolder) &&
			!strings.Contains(artistFolder, artistName)) {
		return paths, nil
	}
	artistDirectory, err := safeLocalPath(roots[album.SourceIndex], artistRel)
	if err != nil {
		return taterMusicNFOPaths{}, err
	}
	paths.ArtistMetadataAvailable = true
	paths.ArtistPath = filepath.Join(artistDirectory, "artist.nfo")
	paths.ArtistRef = cleanLocalRelativePath(filepath.ToSlash(filepath.Join(artistRel, "artist.nfo")))
	return paths, nil
}

func readTaterMusicNFO(path string) (taterMusicLocalNFO, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return taterMusicLocalNFO{}, false
	}
	meta := taterMusicLocalNFO{}
	if err := xml.Unmarshal(raw, &meta); err != nil {
		return taterMusicLocalNFO{}, false
	}
	if cleanTaterText(meta.Title) == "" && cleanTaterText(meta.Album) == "" &&
		cleanTaterText(meta.Artist) == "" && cleanTaterText(meta.Plot) == "" &&
		cleanTaterText(meta.Biography) == "" && len(meta.Genres) == 0 && len(meta.Styles) == 0 &&
		strings.TrimSpace(meta.MusicBrainzAlbumID) == "" &&
		strings.TrimSpace(meta.MusicBrainzReleaseGroupID) == "" &&
		strings.TrimSpace(meta.MusicBrainzArtistID) == "" {
		return taterMusicLocalNFO{}, false
	}
	return meta, true
}

func taterMusicNFODescription(meta taterMusicLocalNFO) string {
	for _, value := range []string{meta.Plot, meta.Biography, meta.Review} {
		if value = cleanTaterText(value); value != "" {
			return value
		}
	}
	return ""
}

func taterMusicNFOYear(meta taterMusicLocalNFO) string {
	for _, value := range []string{meta.Year, meta.Premiered, meta.ReleaseDate} {
		if match := localYearPattern.FindStringSubmatch(value); len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

func applyTaterLocalMusicNFO(cfg *config.Config, album *taterLocalMusicAlbumIndex) {
	if album == nil {
		return
	}
	paths, err := taterMusicNFOFilePaths(cfg, *album)
	if err != nil {
		return
	}
	album.MetadataAvailable = true
	album.ArtistMetadataAvailable = paths.ArtistMetadataAvailable
	if meta, found := readTaterMusicNFO(paths.AlbumPath); found {
		album.HasMetadata = true
		album.MetadataSource = "nfo"
		album.NFORef = paths.AlbumRef
		if album.Title == "" {
			album.Title = cleanTaterText(meta.Title)
			if album.Title == "" {
				album.Title = cleanTaterText(meta.Album)
			}
		}
		if album.Artist == "" {
			album.Artist = cleanTaterText(meta.Artist)
		}
		if album.AlbumArtist == "" {
			album.AlbumArtist = cleanTaterText(meta.AlbumArtist)
		}
		if album.Description == "" {
			album.Description = taterMusicNFODescription(meta)
		}
		if album.Year == "" {
			album.Year = taterMusicNFOYear(meta)
		}
		album.Genres = mergeTaterMusicGenres(album.Genres, meta.Genres)
		album.Styles = mergeTaterMusicGenres(album.Styles, meta.Styles)
		album.Genres = mergeTaterMusicGenres(album.Genres, album.Styles)
		if album.MusicBrainzID == "" {
			album.MusicBrainzID = strings.TrimSpace(meta.MusicBrainzReleaseGroupID)
		}
		if album.MusicBrainzArtistID == "" {
			album.MusicBrainzArtistID = strings.TrimSpace(meta.MusicBrainzAlbumArtistID)
			if album.MusicBrainzArtistID == "" {
				album.MusicBrainzArtistID = strings.TrimSpace(meta.MusicBrainzArtistID)
			}
		}
	}
	if !paths.ArtistMetadataAvailable {
		return
	}
	if meta, found := readTaterMusicNFO(paths.ArtistPath); found {
		album.HasArtistMetadata = true
		album.ArtistNFORef = paths.ArtistRef
		if album.Artist == "" {
			album.Artist = cleanTaterText(meta.Title)
		}
		if album.MusicBrainzArtistID == "" {
			album.MusicBrainzArtistID = strings.TrimSpace(meta.MusicBrainzArtistID)
		}
		album.Genres = mergeTaterMusicGenres(album.Genres, meta.Genres)
		album.Styles = mergeTaterMusicGenres(album.Styles, meta.Styles)
		album.Genres = mergeTaterMusicGenres(album.Genres, album.Styles)
	}
}

func persistTaterMusicProviderIDs(cfg *config.Config, album *taterLocalMusicAlbumIndex) error {
	if album == nil {
		return nil
	}
	store := readTaterMusicArtworkStore(cfg)
	override := store.Items[album.ID]
	override.AlbumID = album.ID
	override.MusicBrainzID = strings.TrimSpace(album.MusicBrainzID)
	override.MusicBrainzArtistID = strings.TrimSpace(album.MusicBrainzArtistID)
	override.Genres = mergeTaterMusicGenres(override.Genres, album.Genres)
	override.UpdatedAt = time.Now().UTC()
	store.Items[album.ID] = override
	return writeTaterMusicArtworkStore(cfg, store)
}

func resolveTaterMusicNFOIDs(
	ctx context.Context,
	cfg *config.Config,
	album *taterLocalMusicAlbumIndex,
) error {
	if album == nil {
		return fmt.Errorf("album is unavailable")
	}
	if strings.TrimSpace(album.MusicBrainzID) != "" &&
		(strings.TrimSpace(album.MusicBrainzArtistID) != "" || !album.ArtistMetadataAvailable) {
		return nil
	}
	candidates, err := searchTaterMusicGenreCandidates(ctx, *album)
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		if strings.TrimSpace(album.MusicBrainzID) != "" {
			return nil
		}
		return os.ErrNotExist
	}
	candidate := candidates[0]
	if album.MusicBrainzID == "" {
		album.MusicBrainzID = strings.TrimSpace(candidate.MusicBrainzID)
	}
	if album.MusicBrainzArtistID == "" {
		album.MusicBrainzArtistID = strings.TrimSpace(candidate.ArtistID)
	}
	return persistTaterMusicProviderIDs(cfg, album)
}

func taterMusicAlbumNFOOutput(album taterLocalMusicAlbumIndex) taterMusicNFOOutput {
	styles := mergeTaterMusicGenres(album.Styles, album.Genres)
	return taterMusicNFOOutput{
		XMLName:                   xml.Name{Local: "album"},
		Title:                     cleanTaterText(album.Title),
		Artist:                    cleanTaterText(album.Artist),
		AlbumArtist:               cleanTaterText(album.AlbumArtist),
		SortTitle:                 cleanTaterText(album.Title),
		Plot:                      cleanTaterText(album.Description),
		Year:                      strings.TrimSpace(album.Year),
		DateAdded:                 time.Now().UTC().Format("2006-01-02 15:04:05"),
		LockData:                  false,
		Genres:                    append([]string(nil), album.Genres...),
		Styles:                    styles,
		MusicBrainzReleaseGroupID: strings.TrimSpace(album.MusicBrainzID),
		MusicBrainzAlbumArtistID:  strings.TrimSpace(album.MusicBrainzArtistID),
		MusicBrainzArtistID:       strings.TrimSpace(album.MusicBrainzArtistID),
	}
}

func taterMusicArtistNFOOutput(album taterLocalMusicAlbumIndex) taterMusicNFOOutput {
	artist := taterMusicAlbumArtist(album)
	styles := mergeTaterMusicGenres(album.Styles, album.Genres)
	return taterMusicNFOOutput{
		XMLName:             xml.Name{Local: "artist"},
		Title:               artist,
		SortTitle:           artist,
		Biography:           "",
		DateAdded:           time.Now().UTC().Format("2006-01-02 15:04:05"),
		LockData:            false,
		Genres:              append([]string(nil), album.Genres...),
		Styles:              styles,
		MusicBrainzArtistID: strings.TrimSpace(album.MusicBrainzArtistID),
	}
}

func writeTaterMusicNFO(path string, value taterMusicNFOOutput) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	raw, err := xml.MarshalIndent(value, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append([]byte(xml.Header), raw...)
	raw = append(raw, '\n')
	if err := writeTaterArtworkFile(path, raw); err != nil {
		return false, err
	}
	return true, nil
}

func ensureTaterMusicNFO(
	ctx context.Context,
	cfg *config.Config,
	album *taterLocalMusicAlbumIndex,
) (int, error) {
	if album == nil {
		return 0, fmt.Errorf("album is unavailable")
	}
	applyTaterLocalMusicNFO(cfg, album)
	needsAlbum := !album.HasMetadata
	needsArtist := album.ArtistMetadataAvailable && !album.HasArtistMetadata
	if !needsAlbum && !needsArtist {
		return 0, nil
	}
	if err := resolveTaterMusicNFOIDs(ctx, cfg, album); err != nil {
		return 0, err
	}
	paths, err := taterMusicNFOFilePaths(cfg, *album)
	if err != nil {
		return 0, err
	}
	created := 0
	if needsAlbum {
		written, writeErr := writeTaterMusicNFO(paths.AlbumPath, taterMusicAlbumNFOOutput(*album))
		if writeErr != nil {
			return created, fmt.Errorf("save album metadata beside music: %w", writeErr)
		}
		if written {
			created++
		}
	}
	if needsArtist && strings.TrimSpace(album.MusicBrainzArtistID) != "" {
		written, writeErr := writeTaterMusicNFO(paths.ArtistPath, taterMusicArtistNFOOutput(*album))
		if writeErr != nil {
			return created, fmt.Errorf("save artist metadata beside music: %w", writeErr)
		}
		if written {
			created++
		}
	}
	applyTaterLocalMusicNFO(cfg, album)
	return created, nil
}
