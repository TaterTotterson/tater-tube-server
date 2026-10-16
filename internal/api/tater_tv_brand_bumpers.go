package api

import (
	"embed"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/TaterTotterson/tater-tube-server/internal/config"
)

const (
	taterTVBrandBumperKind     = "tater_bumper"
	taterTVBrandBumperDuration = 10.005
	taterTVBrandBumperGroupID  = "tater-tube"
	taterTVCustomBumperGroupID = "custom-tater-bumpers"
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

// taterTVBrandBumpers returns the single active Tater Bumper rotation. A valid
// custom library is intentionally exclusive: built-ins return only when the
// last custom bumper is removed.
func taterTVBrandBumpers(cfg *config.Config) []taterTVBumper {
	if custom := taterTVCustomBrandBumpers(cfg); len(custom) > 0 {
		return custom
	}
	return taterTVBuiltInBumpers(cfg)
}

func taterTVCustomBrandBumpers(cfg *config.Config) []taterTVBumper {
	root := taterTVCustomBumperRoot(cfg)
	entries, err := os.ReadDir(root)
	if err != nil {
		return []taterTVBumper{}
	}

	bumpers := make([]taterTVBumper, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || strings.HasPrefix(entry.Name(), ".") || !isMediaExtension(filepath.Ext(entry.Name())) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		duration := taterLocalDurationSeconds(cfg, path)
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		title := cleanMovieTitleFromName(strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name())))
		if title == "" {
			title = "Custom Tater Bumper"
		}
		bumpers = append(bumpers, taterTVBumper{
			Title:          title,
			GroupID:        taterTVCustomBumperGroupID,
			Group:          "Custom Tater Bumpers",
			Placement:      "brand",
			PlacementLabel: "Tater Tube",
			Name:           entry.Name(),
			Kind:           taterTVBrandBumperKind,
			Local:          true,
			Duration:       duration,
			FullDuration:   duration,
			DurationKnown:  true,
			Size:           info.Size(),
		})
	}
	sort.SliceStable(bumpers, func(i, j int) bool {
		return strings.ToLower(bumpers[i].Title) < strings.ToLower(bumpers[j].Title)
	})
	return bumpers
}

func taterTVCustomBumperRoot(cfg *config.Config) string {
	if cfg == nil || strings.TrimSpace(cfg.Metadata.RootPath) == "" {
		return filepath.Join(os.TempDir(), "tater-tube-custom-bumpers")
	}
	return filepath.Join(filepath.Clean(cfg.Metadata.RootPath), "tater-bumpers")
}

func taterTVResolveBrandBumper(cfg *config.Config, rawName string) (taterTVBumper, string, error) {
	name := taterTVSafeFileName(rawName)
	custom := taterTVCustomBrandBumpers(cfg)
	if len(custom) > 0 {
		for _, bumper := range custom {
			if bumper.Name == name {
				return bumper, filepath.Join(taterTVCustomBumperRoot(cfg), bumper.Name), nil
			}
		}
		return taterTVBumper{}, "", fmt.Errorf("custom Tater bumper %q not found", rawName)
	}

	definition, ok := taterBumperDefinitionByName(name)
	if !ok {
		return taterTVBumper{}, "", fmt.Errorf("built-in Tater bumper %q not found", rawName)
	}
	path, err := taterTVEnsureBuiltInBumperFile(cfg, definition.Name)
	if err != nil {
		return taterTVBumper{}, "", err
	}
	duration := definition.Duration
	if duration <= 0 {
		duration = taterTVBrandBumperDuration
	}
	return taterTVBumper{
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
	}, path, nil
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
