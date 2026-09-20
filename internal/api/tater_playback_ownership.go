package api

import "github.com/TaterTotterson/tater-tube-server/internal/nzbfilesystem"

// stopTaterFilePlaybackForPlayer releases local and Discovery playback while
// leaving Tube TV ownership untouched. Tube TV uses this before binding the
// requested channel so repeated playlist reads do not cancel their own session.
func stopTaterFilePlaybackForPlayer(playerID, playerName string, tracker *StreamTracker) int {
	stopped := globalTaterLocalHLS.stopForPlayer(playerID)
	if tracker == nil {
		return stopped
	}
	tracker.SetPlayerPlaybackPresence(nzbfilesystem.ActiveStream{PlayerID: playerID}, false)
	return stopped + tracker.KillStreamsForPlayer(playerID, playerName)
}

// stopTaterPlaybackForPlayer releases every server-side playback resource
// owned by one paired player. It is used when the player requests a replacement
// plan or explicitly reports that playback stopped/completed.
func stopTaterPlaybackForPlayer(playerID, playerName string, tracker *StreamTracker) int {
	stopped := stopTaterFilePlaybackForPlayer(playerID, playerName, tracker)
	globalTaterTVHLS.unbindPlayer(playerID)
	return stopped
}
