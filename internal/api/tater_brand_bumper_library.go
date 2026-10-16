package api

import (
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
)

func (s *Server) handleTaterBumperLibrary(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	custom := taterTVCustomBrandBumpers(cfg)
	mode := "built_in"
	if len(custom) > 0 {
		mode = "custom"
	}
	return RespondSuccess(c, fiber.Map{
		"root":         taterTVCustomBumperRoot(cfg),
		"mode":         mode,
		"customActive": len(custom) > 0,
		"builtInCount": len(taterTVBrandBumperDefinitions),
		"customCount":  len(custom),
		"videos":       custom,
	})
}

func (s *Server) handleTaterBumperUpload(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	form, err := c.MultipartForm()
	if err != nil {
		return RespondValidationError(c, "Tater Bumper upload invalid", err.Error())
	}
	files := form.File["files"]
	if len(files) == 0 {
		return RespondValidationError(c, "No Tater Bumper files uploaded", "files field is empty")
	}

	root := taterTVCustomBumperRoot(cfg)
	if err := os.MkdirAll(root, 0755); err != nil {
		return RespondInternalError(c, "Failed to create the custom Tater Bumper library", err.Error())
	}
	saved := 0
	for _, header := range files {
		name := taterTVSafeFileName(header.Filename)
		if !isMediaExtension(filepath.Ext(name)) {
			continue
		}
		src, err := header.Open()
		if err != nil {
			return RespondInternalError(c, "Failed to open uploaded Tater Bumper", err.Error())
		}
		temp, err := os.CreateTemp(root, ".tater-bumper-upload-*")
		if err != nil {
			_ = src.Close()
			return RespondInternalError(c, "Failed to save Tater Bumper", err.Error())
		}
		tempName := temp.Name()
		_, copyErr := io.Copy(temp, src)
		closeErr := temp.Close()
		_ = src.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(tempName)
			if copyErr != nil {
				return RespondInternalError(c, "Failed to save Tater Bumper", copyErr.Error())
			}
			return RespondInternalError(c, "Failed to finish Tater Bumper upload", closeErr.Error())
		}
		if err := os.Chmod(tempName, 0644); err != nil {
			_ = os.Remove(tempName)
			return RespondInternalError(c, "Failed to set Tater Bumper permissions", err.Error())
		}
		if err := os.Rename(tempName, filepath.Join(root, name)); err != nil {
			_ = os.Remove(tempName)
			return RespondInternalError(c, "Failed to install Tater Bumper", err.Error())
		}
		target := filepath.Join(root, name)
		duration := taterLocalDurationSeconds(cfg, target)
		if duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			_ = os.Remove(target)
			continue
		}
		saved++
	}
	if saved == 0 {
		return RespondValidationError(c, "No supported Tater Bumper videos were uploaded", "Choose one or more video files")
	}

	custom := taterTVCustomBrandBumpers(cfg)
	if len(custom) == 0 {
		return RespondValidationError(c, "The uploaded videos could not be used as Tater Bumpers", "Verify the files are valid videos and try again")
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTaterBumperLibrary(c)
}

func (s *Server) handleTaterBumperDeleteFile(c *fiber.Ctx) error {
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return RespondInternalError(c, "Configuration not available", "CONFIG_NOT_FOUND")
	}
	rawName := strings.TrimSpace(c.Query("name"))
	name := taterTVSafeFileName(rawName)
	if rawName == "" || name == "" {
		return RespondValidationError(c, "Tater Bumper file is required", "name is required")
	}
	if err := os.Remove(filepath.Join(taterTVCustomBumperRoot(cfg), name)); err != nil && !os.IsNotExist(err) {
		return RespondInternalError(c, "Failed to delete Tater Bumper", err.Error())
	}
	taterTVResetGuideForConfig(cfg)
	return s.handleTaterBumperLibrary(c)
}
