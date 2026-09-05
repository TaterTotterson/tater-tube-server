package api

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type taterTMDBNamedValue struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type taterTMDBCountry struct {
	ISOCode string `json:"iso_3166_1"`
	Name    string `json:"name"`
}

type taterTMDBCastMember struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Character string `json:"character"`
	Order     int    `json:"order"`
}

type taterTMDBCrewMember struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Job  string `json:"job"`
}

type taterTMDBCredits struct {
	Cast []taterTMDBCastMember `json:"cast"`
	Crew []taterTMDBCrewMember `json:"crew"`
}

type taterTMDBExternalIDs struct {
	IMDbID string `json:"imdb_id"`
	TVDBID int64  `json:"tvdb_id"`
}

type taterTMDBReleaseDates struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Dates   []struct {
			Certification string `json:"certification"`
			Type          int    `json:"type"`
		} `json:"release_dates"`
	} `json:"results"`
}

type taterTMDBContentRatings struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Rating  string `json:"rating"`
	} `json:"results"`
}

type taterTMDBVideoDetails struct {
	ID                  int64                   `json:"id"`
	Title               string                  `json:"title"`
	OriginalTitle       string                  `json:"original_title"`
	Name                string                  `json:"name"`
	OriginalName        string                  `json:"original_name"`
	Overview            string                  `json:"overview"`
	Tagline             string                  `json:"tagline"`
	ReleaseDate         string                  `json:"release_date"`
	FirstAirDate        string                  `json:"first_air_date"`
	Runtime             int                     `json:"runtime"`
	EpisodeRunTime      []int                   `json:"episode_run_time"`
	VoteAverage         float64                 `json:"vote_average"`
	PosterPath          string                  `json:"poster_path"`
	IMDbID              string                  `json:"imdb_id"`
	Genres              []taterTMDBNamedValue   `json:"genres"`
	ProductionCompanies []taterTMDBNamedValue   `json:"production_companies"`
	ProductionCountries []taterTMDBCountry      `json:"production_countries"`
	Credits             taterTMDBCredits        `json:"credits"`
	ExternalIDs         taterTMDBExternalIDs    `json:"external_ids"`
	ReleaseDates        taterTMDBReleaseDates   `json:"release_dates"`
	ContentRatings      taterTMDBContentRatings `json:"content_ratings"`
}

type taterNFOActor struct {
	Name   string `xml:"name"`
	Role   string `xml:"role,omitempty"`
	Type   string `xml:"type,omitempty"`
	TMDBID int64  `xml:"tmdbid,omitempty"`
}

type taterNFOPerson struct {
	Name   string `xml:",chardata"`
	TMDBID int64  `xml:"tmdbid,attr,omitempty"`
}

type taterNFOUniqueIDOutput struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type taterVideoNFOOutput struct {
	XMLName        xml.Name
	Plot           string                   `xml:"plot,omitempty"`
	Outline        string                   `xml:"outline,omitempty"`
	LockData       bool                     `xml:"lockdata"`
	DateAdded      string                   `xml:"dateadded,omitempty"`
	Title          string                   `xml:"title"`
	OriginalTitle  string                   `xml:"originaltitle,omitempty"`
	Actors         []taterNFOActor          `xml:"actor,omitempty"`
	Directors      []taterNFOPerson         `xml:"director,omitempty"`
	Writers        []taterNFOPerson         `xml:"writer,omitempty"`
	Rating         string                   `xml:"rating,omitempty"`
	Year           int                      `xml:"year,omitempty"`
	SortTitle      string                   `xml:"sorttitle,omitempty"`
	MPAA           string                   `xml:"mpaa,omitempty"`
	IMDbID         string                   `xml:"imdbid,omitempty"`
	IMDbUnderscore string                   `xml:"imdb_id,omitempty"`
	TVDBID         int64                    `xml:"tvdbid,omitempty"`
	TMDBID         int64                    `xml:"tmdbid,omitempty"`
	Premiered      string                   `xml:"premiered,omitempty"`
	ReleaseDate    string                   `xml:"releasedate,omitempty"`
	Runtime        int                      `xml:"runtime,omitempty"`
	Tagline        string                   `xml:"tagline,omitempty"`
	Countries      []string                 `xml:"country,omitempty"`
	Genres         []string                 `xml:"genre,omitempty"`
	Studios        []string                 `xml:"studio,omitempty"`
	UniqueIDs      []taterNFOUniqueIDOutput `xml:"uniqueid,omitempty"`
	ID             string                   `xml:"id,omitempty"`
}

func fetchTaterTMDBVideoDetails(
	ctx context.Context,
	cfg *config.Config,
	video taterLocalVideoIndex,
	tmdbID int64,
) (taterTMDBVideoDetails, error) {
	if tmdbID <= 0 {
		return taterTMDBVideoDetails{}, os.ErrNotExist
	}
	mediaType := "movie"
	appendParts := "credits,external_ids,release_dates"
	if video.LibraryType == "tv" || video.MediaType == "show" {
		mediaType = "tv"
		appendParts = "credits,external_ids,content_ratings"
	}
	params := url.Values{"append_to_response": []string{appendParts}}
	response, err := taterTMDBRequest(ctx, cfg, mediaType+"/"+strconv.FormatInt(tmdbID, 10), params)
	if err != nil {
		return taterTMDBVideoDetails{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return taterTMDBVideoDetails{}, fmt.Errorf("TMDB details returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, taterTMDBResponseMaximumBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > taterTMDBResponseMaximumBytes {
		return taterTMDBVideoDetails{}, fmt.Errorf("TMDB details response is invalid")
	}
	details := taterTMDBVideoDetails{}
	if err := json.Unmarshal(raw, &details); err != nil {
		return taterTMDBVideoDetails{}, err
	}
	if details.ID <= 0 {
		return taterTMDBVideoDetails{}, os.ErrNotExist
	}
	return details, nil
}

func resolveTaterTMDBVideoMetadata(
	ctx context.Context,
	cfg *config.Config,
	video taterLocalVideoIndex,
) (taterVideoArtworkCandidate, taterTMDBVideoDetails, error) {
	candidate := taterVideoArtworkCandidate{TMDBID: video.TMDBID}
	var err error
	if candidate.TMDBID <= 0 && strings.TrimSpace(video.IMDbID) != "" {
		candidate, err = findTaterRemoteVideoCandidateByExternalID(ctx, cfg, video, video.IMDbID)
	}
	if candidate.TMDBID <= 0 && video.TVDBID > 0 {
		candidate, err = findTaterRemoteVideoCandidateByExternalID(
			ctx,
			cfg,
			video,
			strconv.FormatInt(video.TVDBID, 10),
		)
	}
	if candidate.TMDBID <= 0 {
		candidate, err = findTaterRemoteVideoCandidate(ctx, cfg, video)
	}
	if err != nil || candidate.TMDBID <= 0 {
		if err == nil {
			err = os.ErrNotExist
		}
		return taterVideoArtworkCandidate{}, taterTMDBVideoDetails{}, err
	}
	details, err := fetchTaterTMDBVideoDetails(ctx, cfg, video, candidate.TMDBID)
	if err != nil {
		return taterVideoArtworkCandidate{}, taterTMDBVideoDetails{}, err
	}
	candidate.Title = taterTMDBVideoTitle(details)
	candidate.Year = taterDiscoveryYear(taterTMDBVideoDate(details))
	candidate.PosterPath = strings.TrimSpace(details.PosterPath)
	candidate.Popularity = details.VoteAverage
	return candidate, details, nil
}

func taterTMDBVideoTitle(details taterTMDBVideoDetails) string {
	if title := cleanTaterText(details.Title); title != "" {
		return title
	}
	return cleanTaterText(details.Name)
}

func taterTMDBVideoOriginalTitle(details taterTMDBVideoDetails) string {
	if title := cleanTaterText(details.OriginalTitle); title != "" {
		return title
	}
	return cleanTaterText(details.OriginalName)
}

func taterTMDBVideoDate(details taterTMDBVideoDetails) string {
	if value := strings.TrimSpace(details.ReleaseDate); value != "" {
		return value
	}
	return strings.TrimSpace(details.FirstAirDate)
}

func taterTMDBVideoRuntime(details taterTMDBVideoDetails) int {
	if details.Runtime > 0 {
		return details.Runtime
	}
	for _, runtime := range details.EpisodeRunTime {
		if runtime > 0 {
			return runtime
		}
	}
	return 0
}

func taterTMDBVideoCertification(details taterTMDBVideoDetails, mediaType string) string {
	if mediaType == "show" || mediaType == "tvshow" {
		for _, country := range []string{"US", "GB", "CA", "AU"} {
			for _, rating := range details.ContentRatings.Results {
				if strings.EqualFold(rating.Country, country) && strings.TrimSpace(rating.Rating) != "" {
					return strings.TrimSpace(rating.Rating)
				}
			}
		}
		return ""
	}
	type certification struct {
		Value string
		Type  int
	}
	for _, country := range []string{"US", "GB", "CA", "AU"} {
		values := []certification{}
		for _, result := range details.ReleaseDates.Results {
			if !strings.EqualFold(result.Country, country) {
				continue
			}
			for _, release := range result.Dates {
				if value := strings.TrimSpace(release.Certification); value != "" {
					values = append(values, certification{Value: value, Type: release.Type})
				}
			}
		}
		sort.SliceStable(values, func(i, j int) bool {
			preferred := func(value int) int {
				switch value {
				case 3:
					return 0
				case 4:
					return 1
				case 5:
					return 2
				case 6:
					return 3
				default:
					return 4
				}
			}
			return preferred(values[i].Type) < preferred(values[j].Type)
		})
		if len(values) > 0 {
			return values[0].Value
		}
	}
	return ""
}

func taterTMDBVideoIMDbID(details taterTMDBVideoDetails) string {
	if value := strings.TrimSpace(details.IMDbID); value != "" {
		return value
	}
	return strings.TrimSpace(details.ExternalIDs.IMDbID)
}

func taterTMDBVideoNFO(details taterTMDBVideoDetails, mediaType string, now time.Time) taterVideoNFOOutput {
	title := taterTMDBVideoTitle(details)
	originalTitle := taterTMDBVideoOriginalTitle(details)
	if originalTitle == title {
		originalTitle = ""
	}
	releaseDate := taterTMDBVideoDate(details)
	year, _ := strconv.Atoi(taterDiscoveryYear(releaseDate))
	imdbID := taterTMDBVideoIMDbID(details)
	nfo := taterVideoNFOOutput{
		XMLName:       xml.Name{Local: mediaType},
		Plot:          cleanTaterText(details.Overview),
		Outline:       cleanTaterText(details.Tagline),
		LockData:      false,
		DateAdded:     now.UTC().Format("2006-01-02 15:04:05"),
		Title:         title,
		OriginalTitle: originalTitle,
		Rating:        strconv.FormatFloat(details.VoteAverage, 'f', 3, 64),
		Year:          year,
		SortTitle:     title,
		MPAA:          taterTMDBVideoCertification(details, mediaType),
		IMDbID:        imdbID,
		TVDBID:        details.ExternalIDs.TVDBID,
		TMDBID:        details.ID,
		Premiered:     releaseDate,
		ReleaseDate:   releaseDate,
		Runtime:       taterTMDBVideoRuntime(details),
		Tagline:       cleanTaterText(details.Tagline),
	}
	if mediaType == "tvshow" {
		nfo.XMLName.Local = "tvshow"
		nfo.IMDbUnderscore = imdbID
	}
	for _, genre := range details.Genres {
		if value := cleanTaterText(genre.Name); value != "" {
			nfo.Genres = append(nfo.Genres, value)
		}
	}
	for _, company := range details.ProductionCompanies {
		if value := cleanTaterText(company.Name); value != "" {
			nfo.Studios = append(nfo.Studios, value)
		}
	}
	for _, country := range details.ProductionCountries {
		if value := cleanTaterText(country.Name); value != "" {
			nfo.Countries = append(nfo.Countries, value)
		}
	}
	cast := append([]taterTMDBCastMember(nil), details.Credits.Cast...)
	sort.SliceStable(cast, func(i, j int) bool { return cast[i].Order < cast[j].Order })
	if len(cast) > 40 {
		cast = cast[:40]
	}
	for _, actor := range cast {
		if name := cleanTaterText(actor.Name); name != "" {
			nfo.Actors = append(nfo.Actors, taterNFOActor{
				Name: name, Role: cleanTaterText(actor.Character), Type: "Actor", TMDBID: actor.ID,
			})
		}
	}
	directorSeen := map[string]bool{}
	writerSeen := map[string]bool{}
	for _, crew := range details.Credits.Crew {
		name := cleanTaterText(crew.Name)
		key := strings.ToLower(name)
		if name == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(crew.Job)) {
		case "director", "series director":
			if !directorSeen[key] {
				directorSeen[key] = true
				nfo.Directors = append(nfo.Directors, taterNFOPerson{Name: name, TMDBID: crew.ID})
			}
		case "writer", "screenplay", "teleplay", "story", "original story", "novel":
			if !writerSeen[key] {
				writerSeen[key] = true
				nfo.Writers = append(nfo.Writers, taterNFOPerson{Name: name, TMDBID: crew.ID})
			}
		}
	}
	if imdbID != "" {
		nfo.UniqueIDs = append(nfo.UniqueIDs, taterNFOUniqueIDOutput{Type: "imdb", Default: true, Value: imdbID})
	}
	nfo.UniqueIDs = append(nfo.UniqueIDs, taterNFOUniqueIDOutput{
		Type: "tmdb", Default: imdbID == "", Value: strconv.FormatInt(details.ID, 10),
	})
	if details.ExternalIDs.TVDBID > 0 {
		nfo.UniqueIDs = append(nfo.UniqueIDs, taterNFOUniqueIDOutput{
			Type: "tvdb", Value: strconv.FormatInt(details.ExternalIDs.TVDBID, 10),
		})
	}
	if mediaType == "tvshow" && details.ExternalIDs.TVDBID > 0 {
		nfo.ID = strconv.FormatInt(details.ExternalIDs.TVDBID, 10)
	} else if imdbID != "" {
		nfo.ID = imdbID
	} else {
		nfo.ID = strconv.FormatInt(details.ID, 10)
	}
	return nfo
}

func taterVideoNFOPath(cfg *config.Config, video taterLocalVideoIndex) (string, string, error) {
	cat, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return "", "", fmt.Errorf("local media category is unavailable")
	}
	roots := taterLocalMediaCategoryPaths(cat)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return "", "", fmt.Errorf("local media source is unavailable")
	}
	target, err := safeLocalPath(roots[video.SourceIndex], video.Path)
	if err != nil {
		return "", "", err
	}
	nfoPath := ""
	if video.MediaType == "show" {
		nfoPath = filepath.Join(target, "tvshow.nfo")
	} else {
		nfoPath = strings.TrimSuffix(target, filepath.Ext(target)) + ".nfo"
	}
	rel, err := filepath.Rel(roots[video.SourceIndex], nfoPath)
	if err != nil {
		return "", "", err
	}
	return nfoPath, cleanLocalRelativePath(filepath.ToSlash(rel)), nil
}

func writeTaterVideoNFO(
	cfg *config.Config,
	video *taterLocalVideoIndex,
	details taterTMDBVideoDetails,
) (bool, error) {
	if video == nil {
		return false, fmt.Errorf("movie or TV show is unavailable")
	}
	if meta, ref, found := taterLocalVideoMetadataFile(cfg, *video); found {
		applyTaterLocalNFOMetadata(video, meta, ref)
		return false, nil
	}
	path, ref, err := taterVideoNFOPath(cfg, *video)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	mediaType := "movie"
	if video.MediaType == "show" {
		mediaType = "tvshow"
	}
	nfo := taterTMDBVideoNFO(details, mediaType, time.Now())
	raw, err := xml.MarshalIndent(nfo, "", "  ")
	if err != nil {
		return false, err
	}
	raw = append([]byte(xml.Header), raw...)
	raw = append(raw, '\n')
	if err := writeTaterArtworkFile(path, raw); err != nil {
		return false, fmt.Errorf("save metadata beside media: %w", err)
	}
	meta, _, found := taterReadLocalMetadataFile(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	if !found {
		return false, fmt.Errorf("saved metadata could not be read")
	}
	applyTaterLocalNFOMetadata(video, meta, ref)
	return true, nil
}

func updateTaterIndexedVideoMetadata(index *taterLocalLibraryIndex, video taterLocalVideoIndex) {
	if index == nil || video.MediaType != "movie" {
		return
	}
	for fileIndex := range index.Files {
		file := &index.Files[fileIndex]
		if file.CategoryID != video.CategoryID || file.SourceIndex != video.SourceIndex ||
			cleanLocalRelativePath(file.Path) != cleanLocalRelativePath(video.Path) {
			continue
		}
		file.Title = video.Title
		file.Year = video.Year
		file.Description = video.Description
		file.OriginalTitle = video.OriginalTitle
		file.Tagline = video.Tagline
		file.ContentRating = video.ContentRating
		file.CommunityRating = video.CommunityRating
		file.Genres = append([]string(nil), video.Genres...)
		file.Studios = append([]string(nil), video.Studios...)
		file.Countries = append([]string(nil), video.Countries...)
		file.Actors = append([]string(nil), video.Actors...)
		file.Directors = append([]string(nil), video.Directors...)
		file.Writers = append([]string(nil), video.Writers...)
		file.IMDbID = video.IMDbID
		file.TMDBID = video.TMDBID
		file.TVDBID = video.TVDBID
		break
	}
}
