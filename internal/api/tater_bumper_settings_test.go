package api

import (
	"testing"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestTaterBumperSettingsMapDefaultsOnAndPreservesOptOuts(t *testing.T) {
	assert.Equal(t, true, taterBumperSettingsMap(nil)["live_tv"])

	enabled := true
	disabled := false
	cfg := &config.Config{
		TaterBumpers: config.TaterBumpersConfig{
			LiveTV:      &disabled,
			LocalMovies: &enabled,
			LocalSeries: &disabled,
		},
	}

	settings := taterBumperSettingsMap(cfg)
	assert.Equal(t, false, settings["live_tv"])
	assert.Equal(t, true, settings["local_movies"])
	assert.Equal(t, false, settings["local_series"])
	assert.Equal(t, true, settings["nzb_movies"])
}
