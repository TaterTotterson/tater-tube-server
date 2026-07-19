package api

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterTVBrandBumperKind     = "tater_bumper"
	taterTVBrandBumperDuration = 10.005
	taterTVBrandBumperGroupID  = "tater-tube"
)

type taterTVBrandBumperDefinition struct {
	Name     string
	Title    string
	Duration float64
}

var taterTVBrandBumperDefinitions = []taterTVBrandBumperDefinition{
	{Name: "etches-tater-tube-logo.mp4", Title: "Tater Tube Etches", Duration: 10.005},
	{Name: "flying-and-landing.mp4", Title: "Tater Tube Flying and Landing", Duration: 10.005},
	{Name: "juggling-potatoes.mp4", Title: "Tater Tube Juggling Potatoes", Duration: 10.005},
	{Name: "archives-fun-tater-tube.mp4", Title: "Tater Tube Archives Fun", Duration: 10.005},
	{Name: "bicycle-kick-tater-tube.mp4", Title: "Tater Tube Bicycle Kick", Duration: 10.005},
	{Name: "launches-fireworks-tater-tube.mp4", Title: "Tater Tube Launches Fireworks", Duration: 10.005},
	{Name: "mixes-music-tater-tube.mp4", Title: "Tater Tube Mixes Music", Duration: 10.005},
	{Name: "parkour-tater-tube-logo.mp4", Title: "Tater Tube Parkour", Duration: 10.005},
	{Name: "racing-on-tater-tube.mp4", Title: "Tater Tube Racing", Duration: 10.005},
	{Name: "rakes-sand-tater-tube.mp4", Title: "Tater Tube Rakes Sand", Duration: 10.005},
	{Name: "submersible-reveals-tater-tube-logo.mp4", Title: "Tater Tube Submersible Reveal", Duration: 10.005},
	{Name: "cyberpunk-gaming-tater-tube.mp4", Title: "Tater Tube Cyberpunk Gaming", Duration: 10.005},
	{Name: "motion-graphics-tater-tube.mp4", Title: "Tater Tube Motion Graphics", Duration: 8},
	{Name: "construction-tater-tube.mp4", Title: "Tater Tube Construction", Duration: 10.005},
	{Name: "logo-formation-tater-tube.mp4", Title: "Tater Tube Logo Formation", Duration: 10.005},
	{Name: "logo-inspection-tater-tube.mp4", Title: "Tater Tube Logo Inspection", Duration: 10.005},
}

//go:embed assets/tater-bumpers/*.mp4
var taterTVBrandBumperAssets embed.FS

func taterTVBuiltInBumpers(cfg *config.Config) []taterTVBumper {
	bumpers := make([]taterTVBumper, 0, len(taterTVBrandBumperDefinitions))
	for _, definition := range taterTVBrandBumperDefinitions {
		if _, err := taterTVEnsureBuiltInBumperFile(cfg, definition.Name); err != nil {
			slog.Warn("Unable to prepare built-in Tater Tube bumper",
				"name", definition.Name,
				"error", err)
			continue
		}
		duration := definition.Duration
		if duration <= 0 {
			duration = taterTVBrandBumperDuration
		}
		bumpers = append(bumpers, taterTVBumper{
			Title:          definition.Title,
			GroupID:        taterTVBrandBumperGroupID,
			Group:          "Tater Tube",
			Placement:      "brand",
			PlacementLabel: "Tater Tube",
			Name:           definition.Name,
			Kind:           taterTVBrandBumperKind,
			Local:          true,
			Duration:       duration,
			FullDuration:   duration,
			DurationKnown:  true,
		})
	}
	return bumpers
}

func taterTVBuiltInBumperRoot(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Metadata.RootPath) == "" {
		return filepath.Join(os.TempDir(), "tater-tube-brand-bumpers")
	}
	return filepath.Join(filepath.Clean(cfg.Metadata.RootPath), "tube-tv-tater-bumpers")
}

func taterTVEnsureBuiltInBumperFile(cfg *config.Config, rawName string) (string, error) {
	name := taterTVSafeFileName(rawName)
	if name == "" || !taterTVIsBuiltInBumperName(name) {
		return "", fmt.Errorf("unknown built-in bumper %q", rawName)
	}

	data, err := taterTVBrandBumperAssets.ReadFile("assets/tater-bumpers/" + name)
	if err != nil {
		return "", fmt.Errorf("read embedded bumper: %w", err)
	}

	root := taterTVBuiltInBumperRoot(cfg)
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", fmt.Errorf("create bumper cache: %w", err)
	}
	target := filepath.Join(root, name)
	if info, statErr := os.Stat(target); statErr == nil && !info.IsDir() && info.Size() == int64(len(data)) {
		return target, nil
	}

	temp, err := os.CreateTemp(root, ".tater-bumper-*")
	if err != nil {
		return "", fmt.Errorf("create temporary bumper: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("write bumper: %w", err)
	}
	if err := temp.Chmod(0644); err != nil {
		_ = temp.Close()
		return "", fmt.Errorf("set bumper permissions: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("finish bumper: %w", err)
	}
	if err := os.Rename(tempName, target); err != nil {
		return "", fmt.Errorf("install bumper: %w", err)
	}
	return target, nil
}

func taterTVIsBuiltInBumperName(name string) bool {
	for _, definition := range taterTVBrandBumperDefinitions {
		if definition.Name == name {
			return true
		}
	}
	return false
}
