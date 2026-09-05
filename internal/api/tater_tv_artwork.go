package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

type taterTMDBSeasonEpisode struct {
	EpisodeNumber int    `json:"episode_number"`
	StillPath     string `json:"still_path"`
}

type taterTMDBSeasonDetails struct {
	ID           int64                    `json:"id"`
	SeasonNumber int                      `json:"season_number"`
	PosterPath   string                   `json:"poster_path"`
	Episodes     []taterTMDBSeasonEpisode `json:"episodes"`
}

type taterLocalTVEpisodeArtwork struct {
	EpisodeNumber int
	Path          string
}

type taterLocalTVSeasonArtwork struct {
	SeasonNumber int
	Directory    string
	SamplePath   string
	Episodes     []taterLocalTVEpisodeArtwork
}

func taterTVArtworkNeedsRefresh(cfg *config.Config, index *taterLocalLibraryIndex, video taterLocalVideoIndex) bool {
	if video.LibraryType != "tv" && video.MediaType != "show" {
		return false
	}
	if _, found := taterPlayerLocalArtworkPathForKind(
		cfg, video.CategoryID, video.SourceIndex, video.Path, "backdrop",
	); !found {
		return true
	}
	for _, season := range taterLocalTVArtworkSeasons(index, video) {
		if season.Directory != cleanLocalRelativePath(video.Path) {
			if _, found := taterPlayerLocalArtworkPathForKind(
				cfg, video.CategoryID, video.SourceIndex, season.SamplePath, "season-poster",
			); !found {
				return true
			}
		}
		for _, episode := range season.Episodes {
			if _, found := taterPlayerLocalArtworkPathForKind(
				cfg, video.CategoryID, video.SourceIndex, episode.Path, "episode-still",
			); !found {
				return true
			}
		}
	}
	return false
}

func taterVideoArtworkNeedsRefresh(cfg *config.Config, index *taterLocalLibraryIndex, video taterLocalVideoIndex) bool {
	if !video.HasMetadata {
		return true
	}
	if !video.HasArtwork && !video.ArtworkLocked {
		return true
	}
	return taterTVArtworkNeedsRefresh(cfg, index, video)
}

func taterLocalTVArtworkSeasons(index *taterLocalLibraryIndex, video taterLocalVideoIndex) []taterLocalTVSeasonArtwork {
	if index == nil {
		return nil
	}
	showPath := cleanLocalRelativePath(video.Path)
	prefix := showPath + "/"
	seasons := map[int]*taterLocalTVSeasonArtwork{}
	for _, file := range index.Files {
		filePath := cleanLocalRelativePath(file.Path)
		if file.CategoryID != video.CategoryID || file.SourceIndex != video.SourceIndex ||
			!strings.EqualFold(file.LibraryType, "tv") || !strings.HasPrefix(filePath, prefix) ||
			!isMediaExtension(filepath.Ext(filePath)) {
			continue
		}
		match := localEpisodePattern.FindStringSubmatch(strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath)))
		if len(match) < 3 {
			continue
		}
		seasonNumber, seasonErr := strconv.Atoi(match[1])
		episodeNumber, episodeErr := strconv.Atoi(match[2])
		if seasonErr != nil || episodeErr != nil {
			continue
		}
		season := seasons[seasonNumber]
		if season == nil {
			season = &taterLocalTVSeasonArtwork{
				SeasonNumber: seasonNumber,
				Directory:    cleanLocalRelativePath(filepath.ToSlash(filepath.Dir(filePath))),
				SamplePath:   filePath,
			}
			seasons[seasonNumber] = season
		}
		season.Episodes = append(season.Episodes, taterLocalTVEpisodeArtwork{
			EpisodeNumber: episodeNumber,
			Path:          filePath,
		})
	}
	result := make([]taterLocalTVSeasonArtwork, 0, len(seasons))
	for _, season := range seasons {
		sort.SliceStable(season.Episodes, func(i, j int) bool {
			return season.Episodes[i].EpisodeNumber < season.Episodes[j].EpisodeNumber
		})
		result = append(result, *season)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].SeasonNumber < result[j].SeasonNumber })
	return result
}

func fetchTaterTMDBSeasonDetails(
	ctx context.Context,
	cfg *config.Config,
	tmdbID int64,
	seasonNumber int,
) (taterTMDBSeasonDetails, error) {
	response, err := taterTMDBRequest(
		ctx,
		cfg,
		"tv/"+strconv.FormatInt(tmdbID, 10)+"/season/"+strconv.Itoa(seasonNumber),
		nil,
	)
	if err != nil {
		return taterTMDBSeasonDetails{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return taterTMDBSeasonDetails{}, fmt.Errorf("TMDB season details returned HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, taterTMDBResponseMaximumBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > taterTMDBResponseMaximumBytes {
		return taterTMDBSeasonDetails{}, fmt.Errorf("TMDB season details response is invalid")
	}
	details := taterTMDBSeasonDetails{}
	if err := json.Unmarshal(raw, &details); err != nil {
		return taterTMDBSeasonDetails{}, err
	}
	return details, nil
}

func taterTMDBArtworkURL(imagePath, size string) string {
	base := strings.TrimRight(taterTMDBImageBaseURL, "/")
	parsed, err := url.Parse(base)
	if err == nil && strings.Contains(parsed.Path, "/t/p/") {
		parts := strings.Split(strings.TrimRight(parsed.Path, "/"), "/")
		if len(parts) > 0 {
			parts[len(parts)-1] = strings.TrimSpace(size)
			parsed.Path = strings.Join(parts, "/")
			base = strings.TrimRight(parsed.String(), "/")
		}
	}
	return base + "/" + strings.TrimLeft(imagePath, "/")
}

func writeTaterTVArtworkSidecar(
	cfg *config.Config,
	video taterLocalVideoIndex,
	relativePath string,
	raw []byte,
) error {
	category, ok := taterLocalMediaCategory(cfg, video.CategoryID)
	if !ok {
		return fmt.Errorf("local TV category is unavailable")
	}
	roots := taterLocalMediaCategoryPaths(category)
	if video.SourceIndex < 0 || video.SourceIndex >= len(roots) {
		return fmt.Errorf("local TV source is unavailable")
	}
	target, err := safeLocalPath(roots[video.SourceIndex], relativePath)
	if err != nil {
		return err
	}
	return writeTaterArtworkFile(target, raw)
}

func refreshTaterTVSupplementalArtwork(
	ctx context.Context,
	cfg *config.Config,
	index *taterLocalLibraryIndex,
	video taterLocalVideoIndex,
	details taterTMDBVideoDetails,
	force bool,
) error {
	if video.LibraryType != "tv" && video.MediaType != "show" {
		return nil
	}
	var firstErr error
	showPath := cleanLocalRelativePath(video.Path)
	if strings.TrimSpace(details.BackdropPath) != "" {
		_, found := taterPlayerLocalArtworkPathForKind(cfg, video.CategoryID, video.SourceIndex, showPath, "backdrop")
		if force || !found {
			raw, contentType, err := downloadTaterVideoArtwork(ctx, taterTMDBArtworkURL(details.BackdropPath, "w1280"))
			if err == nil {
				err = writeTaterTVArtworkSidecar(
					cfg, video, filepath.ToSlash(filepath.Join(showPath, "backdrop"+taterArtworkExtension(contentType))), raw,
				)
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}

	for _, season := range taterLocalTVArtworkSeasons(index, video) {
		seasonPosterMissing := false
		if season.Directory != showPath {
			_, seasonPosterFound := taterPlayerLocalArtworkPathForKind(
				cfg, video.CategoryID, video.SourceIndex, season.SamplePath, "season-poster",
			)
			seasonPosterMissing = !seasonPosterFound
		}
		missingEpisodes := map[int]string{}
		for _, episode := range season.Episodes {
			_, stillFound := taterPlayerLocalArtworkPathForKind(
				cfg, video.CategoryID, video.SourceIndex, episode.Path, "episode-still",
			)
			if force || !stillFound {
				missingEpisodes[episode.EpisodeNumber] = episode.Path
			}
		}
		if !force && !seasonPosterMissing && len(missingEpisodes) == 0 {
			continue
		}
		seasonDetails, err := fetchTaterTMDBSeasonDetails(ctx, cfg, details.ID, season.SeasonNumber)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if season.Directory != showPath && (force || seasonPosterMissing) && strings.TrimSpace(seasonDetails.PosterPath) != "" {
			raw, contentType, downloadErr := downloadTaterVideoArtwork(
				ctx, taterTMDBArtworkURL(seasonDetails.PosterPath, "w500"),
			)
			if downloadErr == nil {
				downloadErr = writeTaterTVArtworkSidecar(
					cfg, video,
					filepath.ToSlash(filepath.Join(season.Directory, "poster"+taterArtworkExtension(contentType))),
					raw,
				)
			}
			if downloadErr != nil && firstErr == nil {
				firstErr = downloadErr
			}
		}
		for _, remoteEpisode := range seasonDetails.Episodes {
			episodePath, wanted := missingEpisodes[remoteEpisode.EpisodeNumber]
			if !wanted || strings.TrimSpace(remoteEpisode.StillPath) == "" {
				continue
			}
			raw, contentType, downloadErr := downloadTaterVideoArtwork(
				ctx, taterTMDBArtworkURL(remoteEpisode.StillPath, "w780"),
			)
			if downloadErr == nil {
				extension := filepath.Ext(episodePath)
				stem := strings.TrimSuffix(episodePath, extension) + "-thumb" + taterArtworkExtension(contentType)
				downloadErr = writeTaterTVArtworkSidecar(cfg, video, stem, raw)
			}
			if downloadErr != nil && firstErr == nil {
				firstErr = downloadErr
			}
		}
	}
	return firstErr
}
