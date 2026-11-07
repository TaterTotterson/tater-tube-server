package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"
)

const (
	taterDisplayDiscoveryPort   = 8930
	taterDisplaySatelliteTTL    = 20 * time.Second
	taterDisplayActivityPoll    = 5 * time.Second
	taterDisplayPauseThreshold  = 20 * time.Second
	taterDisplayPositionEpsilon = 250 * time.Millisecond
	taterDisplayRenewal         = 30 * time.Second
	taterDisplayLeaseMS         = 90000
	taterDisplayMaxArtwork      = 4 << 20
)

var taterDisplayEpisodePattern = regexp.MustCompile(`(?i)(?:^|[ ._/-])S(\d{1,3})[ ._-]*E(\d{1,4})(?:$|[ ._/-])`)

// taterDisplaySatellite is a generic Companion Display v1 endpoint discovered
// directly on the LAN. No Tater or Home Assistant connection is involved.
type taterDisplaySatellite struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Room            string    `json:"room,omitempty"`
	Board           string    `json:"board,omitempty"`
	FirmwareVersion string    `json:"firmware_version,omitempty"`
	ProtocolVersion int       `json:"protocol_version"`
	Endpoint        string    `json:"endpoint,omitempty"`
	Online          bool      `json:"online"`
	Compatible      bool      `json:"compatible"`
	LinkedPlayerID  string    `json:"linked_player_id,omitempty"`
	LastSeenAt      time.Time `json:"last_seen_at"`
}

type taterDisplayDiscoveryMessage struct {
	Type            string         `json:"type"`
	ProtocolVersion int            `json:"protocol_version"`
	RequestID       string         `json:"request_id,omitempty"`
	DeviceID        string         `json:"device_id,omitempty"`
	Name            string         `json:"name,omitempty"`
	Room            string         `json:"room,omitempty"`
	Board           string         `json:"board,omitempty"`
	FirmwareVersion string         `json:"firmware_version,omitempty"`
	Port            int            `json:"port,omitempty"`
	Path            string         `json:"path,omitempty"`
	Capabilities    map[string]any `json:"capabilities,omitempty"`
}

type taterDisplayRuntime struct {
	ctx    context.Context
	cancel context.CancelFunc
	conn   *net.UDPConn
	client *http.Client
	once   sync.Once
}

type taterDisplayWork struct {
	PlayerID      string
	ClearDisplay  string
	ClearSourceID string
}

type taterDisplayDeliveryState struct {
	EventID         string
	LastAt          time.Time
	LastPositionMS  int64
	PositionChanged time.Time
	PlaybackState   string
}

type taterCompanionNowPlaying struct {
	Active                  bool   `json:"active"`
	SourceID                string `json:"source_id"`
	SourceName              string `json:"source_name,omitempty"`
	EventID                 string `json:"event_id,omitempty"`
	PlaybackState           string `json:"playback_state,omitempty"`
	MediaType               string `json:"media_type,omitempty"`
	Title                   string `json:"title,omitempty"`
	SeriesTitle             string `json:"series_title,omitempty"`
	Season                  int    `json:"season,omitempty"`
	Episode                 int    `json:"episode,omitempty"`
	Subtitle                string `json:"subtitle,omitempty"`
	ProgressMS              int64  `json:"progress_ms,omitempty"`
	DurationMS              int64  `json:"duration_ms,omitempty"`
	PlaybackSpeed           int    `json:"playback_speed,omitempty"`
	ProgressUpdatedAtUnixMS int64  `json:"progress_updated_at_unix_ms,omitempty"`
	LeaseMS                 int64  `json:"lease_ms,omitempty"`
	Revision                int64  `json:"revision,omitempty"`
	ImageDataBase64         string `json:"image_data_b64,omitempty"`
	ImageContentType        string `json:"image_content_type,omitempty"`
}

func taterDisplayCapabilityVersion(capabilities map[string]any) int {
	value := capabilities["now_playing"]
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(typed))
		return parsed
	case bool:
		if typed {
			return 1
		}
	}
	return 0
}

func (s *Server) taterDisplaySatellite(id string, now time.Time) (taterDisplaySatellite, bool) {
	value, found := s.taterDisplaySatellites.Load(strings.TrimSpace(id))
	if !found {
		return taterDisplaySatellite{}, false
	}
	display, ok := value.(taterDisplaySatellite)
	if !ok {
		return taterDisplaySatellite{}, false
	}
	display.Online = display.Online && now.Sub(display.LastSeenAt) <= taterDisplaySatelliteTTL
	return display, true
}

func (s *Server) availableTaterDisplaySatellites(now time.Time) []taterDisplaySatellite {
	linked := map[string]string{}
	if s.configManager != nil {
		if cfg := s.configManager.GetConfig(); cfg != nil {
			for _, player := range cfg.Players.Paired {
				if player.LinkedDisplayID != "" && player.RevokedAt == "" {
					linked[player.LinkedDisplayID] = player.ID
				}
			}
		}
	}
	rows := []taterDisplaySatellite{}
	s.taterDisplaySatellites.Range(func(key, value any) bool {
		display, ok := value.(taterDisplaySatellite)
		if !ok || now.Sub(display.LastSeenAt) > taterDisplaySatelliteTTL {
			s.taterDisplaySatellites.Delete(key)
			return true
		}
		display.Online = display.Online && now.Sub(display.LastSeenAt) <= taterDisplaySatelliteTTL
		display.LinkedPlayerID = linked[display.ID]
		rows = append(rows, display)
		return true
	})
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Online != rows[j].Online {
			return rows[i].Online
		}
		if rows[i].Room != rows[j].Room {
			return strings.ToLower(rows[i].Room) < strings.ToLower(rows[j].Room)
		}
		return strings.ToLower(rows[i].Name) < strings.ToLower(rows[j].Name)
	})
	return rows
}

// StartTaterDisplayDiscovery starts LAN discovery and delivery. It is called
// only by the real server setup, keeping unit-test Server values inert.
func (s *Server) StartTaterDisplayDiscovery() error {
	s.taterDisplayStartOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			cancel()
			s.taterDisplayStartErr = err
			return
		}
		if err := enableUDPBroadcast(conn); err != nil {
			conn.Close()
			cancel()
			s.taterDisplayStartErr = err
			return
		}
		runtime := &taterDisplayRuntime{
			ctx: ctx, cancel: cancel, conn: conn,
			client: &http.Client{Timeout: 8 * time.Second},
		}
		s.taterDisplayRuntime = runtime
		go s.runTaterDisplayDiscovery(runtime)
	})
	return s.taterDisplayStartErr
}

func (s *Server) stopTaterDisplayDiscovery() {
	if runtime := s.taterDisplayRuntime; runtime != nil {
		runtime.once.Do(func() {
			runtime.cancel()
			_ = runtime.conn.Close()
		})
	}
}

func (s *Server) runTaterDisplayDiscovery(runtime *taterDisplayRuntime) {
	go s.readTaterDisplayAnnouncements(runtime)
	discoveryTicker := time.NewTicker(taterDisplayActivityPoll)
	defer discoveryTicker.Stop()
	s.broadcastTaterDisplayProbe(runtime)
	s.pushAllTaterDisplays(runtime)
	for {
		select {
		case <-runtime.ctx.Done():
			return
		case <-discoveryTicker.C:
			s.broadcastTaterDisplayProbe(runtime)
			s.pushAllTaterDisplays(runtime)
		case work := <-s.taterDisplayWake:
			if work.ClearDisplay != "" {
				s.clearTaterDisplay(runtime, work.ClearDisplay, work.ClearSourceID)
			}
			if work.PlayerID != "" {
				s.pushTaterDisplayForPlayer(runtime, work.PlayerID)
			}
		}
	}
}

func (s *Server) broadcastTaterDisplayProbe(runtime *taterDisplayRuntime) {
	requestID := strconv.FormatInt(time.Now().UnixNano(), 36)
	payload, _ := json.Marshal(taterDisplayDiscoveryMessage{
		Type: "tater.display.discover", ProtocolVersion: 1, RequestID: requestID,
	})
	addresses := []*net.UDPAddr{
		{IP: net.IPv4bcast, Port: taterDisplayDiscoveryPort},
		{IP: net.IPv4(239, 255, 255, 250), Port: taterDisplayDiscoveryPort},
	}
	if interfaces, err := net.Interfaces(); err == nil {
		for _, iface := range interfaces {
			if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			interfaceAddresses, _ := iface.Addrs()
			for _, address := range interfaceAddresses {
				network, ok := address.(*net.IPNet)
				if !ok || network.IP.To4() == nil || len(network.Mask) != net.IPv4len {
					continue
				}
				ip := network.IP.To4()
				broadcast := make(net.IP, net.IPv4len)
				for index := range net.IPv4len {
					broadcast[index] = ip[index] | ^network.Mask[index]
				}
				addresses = append(addresses, &net.UDPAddr{IP: broadcast, Port: taterDisplayDiscoveryPort})
			}
		}
	}
	seen := map[string]bool{}
	for _, address := range addresses {
		if seen[address.String()] {
			continue
		}
		seen[address.String()] = true
		_, _ = runtime.conn.WriteToUDP(payload, address)
	}
}

func (s *Server) readTaterDisplayAnnouncements(runtime *taterDisplayRuntime) {
	buffer := make([]byte, 8192)
	for {
		n, remote, err := runtime.conn.ReadFromUDP(buffer)
		if err != nil {
			if runtime.ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		var announce taterDisplayDiscoveryMessage
		if json.Unmarshal(buffer[:n], &announce) != nil || announce.Type != "tater.display.announce" {
			continue
		}
		s.acceptTaterDisplayAnnouncement(announce, remote, time.Now().UTC())
	}
}

func (s *Server) acceptTaterDisplayAnnouncement(announce taterDisplayDiscoveryMessage, remote *net.UDPAddr, now time.Time) bool {
	id := cleanTaterText(announce.DeviceID)
	if id == "" || len(id) > 200 || announce.Port < 1 || announce.Port > 65535 || remote == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(remote.IP)
	if !ok || (!address.Unmap().IsPrivate() && !address.Unmap().IsLinkLocalUnicast() && !address.Unmap().IsLoopback()) {
		return false
	}
	host := remote.IP.String()
	endpoint := "http://" + net.JoinHostPort(host, strconv.Itoa(announce.Port)) + "/v1/now-playing"
	display := taterDisplaySatellite{
		ID: id, Name: cleanTaterText(announce.Name), Room: cleanTaterText(announce.Room),
		Board: cleanTaterSlug(announce.Board, "unknown"), FirmwareVersion: strings.TrimSpace(announce.FirmwareVersion),
		ProtocolVersion: announce.ProtocolVersion, Endpoint: endpoint, Online: true,
		Compatible: announce.ProtocolVersion == 1 && taterDisplayCapabilityVersion(announce.Capabilities) >= 1,
		LastSeenAt: now,
	}
	if display.Name == "" {
		display.Name = id
	}
	s.taterDisplaySatellites.Store(id, display)
	if s.configManager != nil {
		if cfg := s.configManager.GetConfig(); cfg != nil {
			for _, player := range cfg.Players.Paired {
				if player.RevokedAt == "" && player.LinkedDisplayID == id {
					s.wakeTaterDisplay(player.ID)
					break
				}
			}
		}
	}
	return true
}

func (s *Server) wakeTaterDisplay(playerID string) {
	if strings.TrimSpace(playerID) == "" {
		return
	}
	select {
	case s.taterDisplayWake <- taterDisplayWork{PlayerID: playerID}:
	default:
	}
}

func (s *Server) queueTaterDisplayClear(displayID, sourceID string) {
	if displayID == "" || sourceID == "" {
		return
	}
	select {
	case s.taterDisplayWake <- taterDisplayWork{ClearDisplay: displayID, ClearSourceID: sourceID}:
	default:
	}
}

func (s *Server) pushAllTaterDisplays(runtime *taterDisplayRuntime) {
	if s.configManager == nil {
		return
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return
	}
	for _, player := range cfg.Players.Paired {
		if player.RevokedAt == "" && player.LinkedDisplayID != "" {
			s.pushTaterDisplayForPlayer(runtime, player.ID)
		}
	}
}

func latestTaterDisplayActivity(activities []nzbfilesystem.ActiveStream, player config.PlayerConfig) (nzbfilesystem.ActiveStream, bool) {
	for _, activity := range activities {
		if strings.EqualFold(strings.TrimSpace(activity.PlayerID), strings.TrimSpace(player.ID)) {
			return activity, true
		}
	}
	// Old activity records may predate stable player IDs. Keep the same
	// unambiguous display-name fallback used by the Activity dashboard.
	for _, activity := range activities {
		if strings.TrimSpace(activity.PlayerID) == "" &&
			strings.EqualFold(strings.TrimSpace(activity.UserName), strings.TrimSpace(player.Name)) {
			return activity, true
		}
	}
	return nzbfilesystem.ActiveStream{}, false
}

func taterDisplayActivityTitle(activity nzbfilesystem.ActiveStream) string {
	if title := cleanTaterText(activity.MediaTitle); title != "" {
		return title
	}
	value := strings.TrimSpace(activity.FilePath)
	if strings.EqualFold(strings.TrimSpace(activity.Source), "Tube TV") {
		if _, title, found := strings.Cut(value, " - "); found {
			value = title
		}
	}
	value = strings.TrimSuffix(filepath.Base(value), filepath.Ext(value))
	if title := cleanMovieTitleFromName(value); title != "" {
		return title
	}
	return "Now Playing"
}

func taterDisplayEpisodeNumbers(activity nzbfilesystem.ActiveStream) (int, int) {
	match := taterDisplayEpisodePattern.FindStringSubmatch(strings.Join([]string{
		activity.MediaTitle, activity.MediaPath, activity.FilePath,
	}, " "))
	if len(match) != 3 {
		return 0, 0
	}
	season, _ := strconv.Atoi(match[1])
	episode, _ := strconv.Atoi(match[2])
	return max(season, 0), max(episode, 0)
}

func taterDisplayMediaType(activity nzbfilesystem.ActiveStream, season, episode int) string {
	if strings.EqualFold(strings.TrimSpace(activity.Source), "Tube TV") {
		return "channel"
	}
	mediaType := cleanTaterSlug(activity.MediaType, "")
	if season > 0 || episode > 0 || mediaType == "show" || mediaType == "series" || mediaType == "tv" || mediaType == "tvshow" {
		return "episode"
	}
	if mediaType == "" || mediaType == "video" {
		return "movie"
	}
	return mediaType
}

func taterDisplaySubtitle(activity nzbfilesystem.ActiveStream, mediaType string, season, episode int) string {
	parts := []string{}
	if strings.TrimSpace(activity.SeriesTitle) != "" {
		parts = append(parts, strings.TrimSpace(activity.SeriesTitle))
	}
	if season > 0 || episode > 0 {
		parts = append(parts, fmt.Sprintf("S%d E%d", season, episode))
	}
	if len(parts) == 0 {
		label := strings.TrimSpace(strings.ReplaceAll(mediaType, "_", " "))
		if label != "" {
			parts = append(parts, strings.ToUpper(label[:1])+label[1:])
		}
	}
	return strings.Join(parts, " · ")
}

func taterDisplayActivityEventID(playerID string, activity nzbfilesystem.ActiveStream) string {
	identity := strings.TrimSpace(activity.MediaID)
	if identity == "" {
		identity = strings.TrimSpace(activity.MediaPath)
	}
	if identity == "" {
		identity = strings.TrimSpace(activity.FilePath)
	}
	return "activity:" + taterDisplayPayloadRevision([]string{
		strings.TrimSpace(playerID), identity, activity.StartedAt.UTC().Format(time.RFC3339Nano),
	})
}

func taterDisplayMilliseconds(seconds float64) int64 {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0
	}
	return int64(math.Round(seconds * 1000))
}

func observeTaterDisplayPlayback(previous taterDisplayDeliveryState, eventID string, positionMS int64, now time.Time) taterDisplayDeliveryState {
	next := previous
	next.EventID = eventID
	next.LastPositionMS = positionMS
	next.PlaybackState = "playing"
	positionDelta := positionMS - previous.LastPositionMS
	if positionDelta < 0 {
		positionDelta = -positionDelta
	}
	if previous.EventID != eventID || previous.PositionChanged.IsZero() ||
		time.Duration(positionDelta)*time.Millisecond >= taterDisplayPositionEpsilon {
		next.PositionChanged = now
	} else if now.Sub(previous.PositionChanged) >= taterDisplayPauseThreshold {
		next.PlaybackState = "paused"
	}
	return next
}

func shouldSendTaterDisplay(previous, next taterDisplayDeliveryState, active bool, now time.Time) bool {
	if !active {
		// Send one clear when the server starts idle or playback ends. A failed
		// clear retains the active event and is retried on the next poll.
		return previous.EventID != "" || previous.LastAt.IsZero()
	}
	if previous.LastAt.IsZero() || previous.EventID != next.EventID ||
		previous.PlaybackState != next.PlaybackState ||
		previous.LastPositionMS != next.LastPositionMS {
		return true
	}
	return now.Sub(previous.LastAt) >= taterDisplayRenewal
}

func taterDisplayPayloadRevision(payload any) string {
	encoded, _ := json.Marshal(payload)
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:12])
}

func taterDisplayNumericRevision(revision string) int64 {
	decoded, err := hex.DecodeString(revision)
	if err != nil || len(decoded) < 8 {
		return time.Now().UnixMilli()
	}
	return int64(binary.BigEndian.Uint64(decoded[:8]) & uint64(^uint64(0)>>1))
}

func (s *Server) pushTaterDisplayForPlayer(runtime *taterDisplayRuntime, playerID string) {
	if s.configManager == nil || s.streamTracker == nil {
		return
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return
	}
	var player *config.PlayerConfig
	for index := range cfg.Players.Paired {
		if cfg.Players.Paired[index].ID == playerID && cfg.Players.Paired[index].RevokedAt == "" {
			player = &cfg.Players.Paired[index]
			break
		}
	}
	if player == nil || player.LinkedDisplayID == "" {
		return
	}
	display, found := s.taterDisplaySatellite(player.LinkedDisplayID, time.Now().UTC())
	if !found || !display.Online || !display.Compatible {
		return
	}
	state := taterDisplayDeliveryState{}
	if value, ok := s.taterDisplayDeliveries.Load(display.ID); ok {
		state, _ = value.(taterDisplayDeliveryState)
	}
	now := time.Now().UTC()
	payload, activity, nextState, active := taterDisplayPayloadForPlayer(*player, s.streamTracker.GetActive(), state, now)
	if !shouldSendTaterDisplay(state, nextState, active, now) {
		return
	}
	if active && nextState.EventID != "" && state.EventID != nextState.EventID {
		if artwork, contentType := s.taterDisplayArtwork(runtime.ctx, cfg, activity); len(artwork) > 0 {
			payload.ImageDataBase64 = base64.StdEncoding.EncodeToString(artwork)
			payload.ImageContentType = contentType
		}
	}
	if s.sendTaterDisplay(runtime, display.Endpoint, payload) {
		nextState.LastAt = now
		s.taterDisplayDeliveries.Store(display.ID, nextState)
	}
}

func taterDisplayPayloadForPlayer(player config.PlayerConfig, activities []nzbfilesystem.ActiveStream, previous taterDisplayDeliveryState, now time.Time) (taterCompanionNowPlaying, nzbfilesystem.ActiveStream, taterDisplayDeliveryState, bool) {
	sourceID := "tater-tube:" + player.ID
	payload := taterCompanionNowPlaying{SourceID: sourceID, SourceName: player.Name}
	activity, found := latestTaterDisplayActivity(activities, player)
	if !found {
		return payload, activity, taterDisplayDeliveryState{}, false
	}
	position := taterDisplayMilliseconds(activity.PlaybackPosition)
	duration := taterDisplayMilliseconds(activity.MediaDuration)
	if duration > 0 {
		position = min(position, duration)
	}
	eventID := taterDisplayActivityEventID(player.ID, activity)
	nextState := observeTaterDisplayPlayback(previous, eventID, position, now)
	season, episode := taterDisplayEpisodeNumbers(activity)
	mediaType := taterDisplayMediaType(activity, season, episode)
	speed := 1
	if nextState.PlaybackState == "paused" {
		speed = 0
	}
	payload = taterCompanionNowPlaying{
		Active: true, SourceID: sourceID, SourceName: player.Name, EventID: eventID,
		PlaybackState: nextState.PlaybackState, MediaType: mediaType, Title: taterDisplayActivityTitle(activity),
		SeriesTitle: activity.SeriesTitle, Season: season, Episode: episode,
		Subtitle: taterDisplaySubtitle(activity, mediaType, season, episode), ProgressMS: position,
		DurationMS: duration, PlaybackSpeed: speed,
		ProgressUpdatedAtUnixMS: now.UnixMilli(), LeaseMS: taterDisplayLeaseMS,
	}
	revision := taterDisplayPayloadRevision(payload)
	payload.Revision = taterDisplayNumericRevision(revision)
	return payload, activity, nextState, true
}

func (s *Server) sendTaterDisplay(runtime *taterDisplayRuntime, endpoint string, payload taterCompanionNowPlaying) bool {
	body, err := json.Marshal(payload)
	if err != nil || len(body) > taterDisplayMaxArtwork*4/3+128*1024 {
		return false
	}
	request, err := http.NewRequestWithContext(runtime.ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "Tater-Tube-Server/Companion-Display-v1")
	response, err := runtime.client.Do(request)
	if err != nil {
		slog.DebugContext(runtime.ctx, "Companion Display update failed", "endpoint", endpoint, "error", err)
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func (s *Server) clearTaterDisplay(runtime *taterDisplayRuntime, displayID, sourceID string) {
	display, found := s.taterDisplaySatellite(displayID, time.Now().UTC())
	if !found || !display.Online {
		return
	}
	payload := taterCompanionNowPlaying{SourceID: "tater-tube:" + sourceID}
	if s.sendTaterDisplay(runtime, display.Endpoint, payload) {
		s.taterDisplayDeliveries.Delete(displayID)
	}
}

func (s *Server) taterDisplayArtwork(ctx context.Context, cfg *config.Config, activity nzbfilesystem.ActiveStream) ([]byte, string) {
	item, found := s.taterDisplayArtworkItem(ctx, cfg, activity)
	if !found {
		return nil, ""
	}
	artworkPath := ""
	if category, categoryFound := taterLocalMediaCategory(cfg, taterRawLocalCategoryID(item.CategoryID)); categoryFound {
		artworkPath, found = taterStoredVideoArtworkPath(cfg, category, item.SourceIndex, item.Path)
	}
	if !found {
		artworkPath, found = taterPlayerLocalArtworkPathForKind(cfg, item.CategoryID, item.SourceIndex, item.Path, "poster")
	}
	if !found {
		return nil, ""
	}
	thumbnail, err := taterArtworkThumbnailSized(artworkPath, taterArtworkPosterWidth, taterArtworkPosterHeight)
	if err != nil || len(thumbnail) == 0 || len(thumbnail) > taterDisplayMaxArtwork {
		return nil, ""
	}
	return thumbnail, "image/jpeg"
}

func (s *Server) taterDisplayArtworkItem(ctx context.Context, cfg *config.Config, activity nzbfilesystem.ActiveStream) (taterUsenetItem, bool) {
	if strings.TrimSpace(activity.MediaPath) != "" {
		return taterUsenetItem{
			CategoryID:  activity.CategoryID,
			SourceIndex: activity.SourceIndex,
			Path:        activity.MediaPath,
			Title:       taterDisplayActivityTitle(activity),
		}, true
	}
	candidates := s.taterRecommendationCandidates(ctx, cfg, "", "household")
	title := cleanTaterPlaybackQuery(taterDisplayActivityTitle(activity))
	series := cleanTaterPlaybackQuery(activity.SeriesTitle)
	for _, candidate := range candidates {
		candidateTitle := cleanTaterPlaybackQuery(candidate.Title)
		if strings.TrimSpace(candidate.Launch.Path) != "" && (candidateTitle == title || (series != "" && candidateTitle == series)) {
			return candidate.Launch, true
		}
	}
	return taterUsenetItem{}, false
}
