package api

import (
	"log/slog"

	"github.com/TaterTotterson/tater-tube-server/internal/auth"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
)

// AuthResponse represents authentication response data
type AuthResponse struct {
	User        *UserResponse `json:"user,omitempty"`
	RedirectURL string        `json:"redirect_url,omitempty"`
	Message     string        `json:"message,omitempty"`
}

// UserResponse represents user data for API responses
type UserResponse struct {
	ID        string `json:"id"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatar_url,omitempty"`
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key,omitempty"`
	IsAdmin   bool   `json:"is_admin"`
	LastLogin string `json:"last_login,omitempty"`
}

// LoginRequest represents direct authentication login request
type LoginRequest struct {
	Password string `json:"password"`
}

// RegisterRequest represents user registration request
type RegisterRequest struct {
	Password string `json:"password"`
}

// handleDirectLogin handles server password authentication
//
//	@Summary		Login
//	@Description	Authenticates with the server password and sets a session cookie. Rate-limited to 10/min per IP.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		LoginRequest	true	"Login credentials"
//	@Success		200		{object}	APIResponse{data=AuthResponse}
//	@Failure		400		{object}	APIResponse
//	@Failure		401		{object}	APIResponse
//	@Failure		429		{object}	APIResponse
//	@Router			/auth/login [post]
func (s *Server) handleDirectLogin(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondBadRequest(c, "Invalid request body", err.Error())
	}

	if req.Password == "" {
		return RespondBadRequest(c, "Password is required", "")
	}

	if !compareServerPassword(s.getServerPasswordHash(), req.Password) {
		return RespondUnauthorized(c, "Invalid credentials", "")
	}

	if err := s.setPasswordSessionCookie(c); err != nil {
		return RespondInternalError(c, "Failed to set cookie", err.Error())
	}

	response := AuthResponse{
		User:    s.mapUserToResponse(passwordAdminUser()),
		Message: "Login successful",
	}
	return RespondSuccess(c, response)
}

// handleRegister handles user registration (first user only)
//
//	@Summary		Register
//	@Description	Creates the first admin user account. Only allowed when no users exist yet.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		RegisterRequest	true	"Registration details"
//	@Success		201		{object}	APIResponse{data=AuthResponse}
//	@Failure		400		{object}	APIResponse
//	@Failure		409		{object}	APIResponse
//	@Router			/auth/register [post]
func (s *Server) handleRegister(c *fiber.Ctx) error {
	var req RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondBadRequest(c, "Invalid request body", err.Error())
	}

	if req.Password == "" {
		return RespondBadRequest(c, "Password is required", "")
	}
	if len(req.Password) < 12 {
		return RespondValidationError(c, "Password must be at least 12 characters", "")
	}

	if s.isServerPasswordConfigured() {
		return RespondForbidden(c, "Server password is already configured", "")
	}

	passwordHash, err := hashServerPassword(req.Password)
	if err != nil {
		return RespondInternalError(c, "Failed to hash password", err.Error())
	}
	if err := s.setServerPasswordHash(passwordHash); err != nil {
		return RespondInternalError(c, "Failed to save password", err.Error())
	}

	if err := s.setPasswordSessionCookie(c); err != nil {
		return RespondInternalError(c, "Failed to set cookie", err.Error())
	}

	response := AuthResponse{
		User:    s.mapUserToResponse(passwordAdminUser()),
		Message: "Password saved successfully.",
	}

	return RespondSuccess(c, response)
}

// handleCheckRegistration checks if registration is allowed
//
//	@Summary		Check registration status
//	@Description	Returns whether registration is currently open (i.e. no users exist yet).
//	@Tags			Auth
//	@Produce		json
//	@Success		200	{object}	APIResponse
//	@Router			/auth/registration-status [get]
func (s *Server) handleCheckRegistration(c *fiber.Ctx) error {
	passwordConfigured := s.isServerPasswordConfigured()
	configuredCount := 0
	if passwordConfigured {
		configuredCount = 1
	}
	response := fiber.Map{
		"registration_enabled": !passwordConfigured,
		"setup_required":       false,
		"password_configured":  passwordConfigured,
		"user_count":           configuredCount,
	}
	return RespondSuccess(c, response)
}

// handleGetAuthConfig returns authentication configuration (public endpoint)
//
//	@Summary		Get auth config
//	@Description	Returns authentication configuration (login required flag, available providers). Public endpoint.
//	@Tags			Auth
//	@Produce		json
//	@Success		200	{object}	APIResponse
//	@Router			/auth/config [get]
func (s *Server) handleGetAuthConfig(c *fiber.Ctx) error {
	passwordConfigured := s.isServerPasswordConfigured()

	response := fiber.Map{
		"login_required":      passwordConfigured,
		"password_configured": passwordConfigured,
	}
	return RespondSuccess(c, response)
}

// handleAuthUser returns current authenticated user information
//
//	@Summary		Get current user
//	@Description	Returns information about the currently authenticated user.
//	@Tags			User
//	@Produce		json
//	@Success		200	{object}	APIResponse{data=UserResponse}
//	@Failure		401	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/user [get]
func (s *Server) handleAuthUser(c *fiber.Ctx) error {
	user := auth.GetUserFromContext(c)
	if user == nil {
		if s.isAdminOrLoginDisabled(nil) {
			return RespondSuccess(c, UserResponse{
				ID:       "anonymous",
				Name:     "Admin",
				Provider: "none",
				IsAdmin:  true,
			})
		}
		return RespondUnauthorized(c, "Not authenticated", "")
	}

	response := s.mapUserToResponse(user)
	return RespondSuccess(c, *response)
}

// handleAuthLogout logs out the current user
//
//	@Summary		Logout
//	@Description	Invalidates the current session.
//	@Tags			User
//	@Produce		json
//	@Success		200	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/user/logout [post]
func (s *Server) handleAuthLogout(c *fiber.Ctx) error {
	clearPasswordSessionCookie(c)

	response := AuthResponse{
		Message: "Logged out successfully",
	}
	return RespondSuccess(c, response)
}

// handleClearServerPassword removes the server password and disables login.
//
//	@Summary		Clear server password
//	@Description	Removes the server password. When no password is saved, the web UI opens without login.
//	@Tags			Auth
//	@Produce		json
//	@Success		200	{object}	APIResponse
//	@Failure		401	{object}	APIResponse
//	@Failure		500	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/auth/password [delete]
func (s *Server) handleClearServerPassword(c *fiber.Ctx) error {
	if auth.GetUserFromContext(c) == nil && !s.isAdminOrLoginDisabled(nil) {
		return RespondUnauthorized(c, "Not authenticated", "")
	}
	if err := s.clearServerPasswordHash(); err != nil {
		return RespondInternalError(c, "Failed to remove password", err.Error())
	}
	clearPasswordSessionCookie(c)

	return RespondMessage(c, "Password removed. Login is disabled.")
}

// handleAuthRefresh refreshes the current password session
//
//	@Summary		Refresh token
//	@Description	Renews the current password session cookie.
//	@Tags			User
//	@Produce		json
//	@Success		200	{object}	APIResponse{data=AuthResponse}
//	@Failure		401	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/user/refresh [post]
func (s *Server) handleAuthRefresh(c *fiber.Ctx) error {
	if auth.GetUserFromContext(c) == nil {
		return RespondUnauthorized(c, "Not authenticated", "")
	}
	if err := s.setPasswordSessionCookie(c); err != nil {
		return RespondInternalError(c, "Failed to set cookie", err.Error())
	}

	response := AuthResponse{
		Message: "Token refreshed successfully",
	}
	return RespondSuccess(c, response)
}

// isAdminOrLoginDisabled returns true if the user is an admin or login is disabled
func (s *Server) isAdminOrLoginDisabled(user *database.User) bool {
	if user != nil && user.IsAdmin {
		return true
	}
	if !s.isServerPasswordConfigured() {
		return true
	}
	return false
}

// handleChangeOwnPassword allows the authenticated user to change their own password
//
//	@Summary		Change password
//	@Description	Changes the password for the currently authenticated user.
//	@Tags			User
//	@Accept			json
//	@Produce		json
//	@Param			body	body		object{current_password=string,new_password=string}	true	"Password change request"
//	@Success		200		{object}	APIResponse
//	@Failure		400		{object}	APIResponse
//	@Failure		401		{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/user/password [put]
func (s *Server) handleChangeOwnPassword(c *fiber.Ctx) error {
	user := auth.GetUserFromContext(c)
	if user == nil {
		return RespondUnauthorized(c, "Not authenticated", "")
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return RespondBadRequest(c, "Invalid request body", err.Error())
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return RespondBadRequest(c, "Current password and new password are required", "")
	}
	if len(req.NewPassword) < 12 {
		return RespondValidationError(c, "Password must be at least 12 characters", "")
	}

	if !compareServerPassword(s.getServerPasswordHash(), req.CurrentPassword) {
		return RespondUnauthorized(c, "Current password is incorrect", "")
	}

	hash, err := hashServerPassword(req.NewPassword)
	if err != nil {
		return RespondInternalError(c, "Failed to hash password", err.Error())
	}
	if err := s.setServerPasswordHash(hash); err != nil {
		return RespondInternalError(c, "Failed to update password", err.Error())
	}
	if err := s.setPasswordSessionCookie(c); err != nil {
		return RespondInternalError(c, "Failed to set cookie", err.Error())
	}
	return RespondMessage(c, "Password updated successfully")
}

// handleRegenerateAPIKey regenerates API key for the authenticated user
//
//	@Summary		Regenerate API key
//	@Description	Generates a new API key for the authenticated user, invalidating the old one.
//	@Tags			User
//	@Produce		json
//	@Success		200	{object}	APIResponse{data=UserResponse}
//	@Failure		401	{object}	APIResponse
//	@Security		BearerAuth
//	@Router			/user/api-key/regenerate [post]
func (s *Server) handleRegenerateAPIKey(c *fiber.Ctx) error {
	// Try to get user from context (auth enabled case)
	user := auth.GetUserFromContext(c)

	// If no user in context, and authentication is disabled, let's create a default admin user
	if user == nil && s.userRepo != nil {
		if !s.isPasswordLoginRequired() {
			// Auto-bootstrap a default admin user when auth is disabled
			user = &database.User{
				UserID:   "admin",
				Provider: "direct",
				IsAdmin:  true,
			}
			err := s.userRepo.CreateUser(c.Context(), user)
			if err != nil {
				return RespondInternalError(c, "Failed to bootstrap default admin user", err.Error())
			}
			slog.InfoContext(c.Context(), "Bootstrapped default admin user for API key generation")
		}
	}

	// If still no user, return error
	if user == nil {
		return RespondUnauthorized(c, "No user found to regenerate API key for. Please register first.", "")
	}

	// Regenerate API key
	apiKey, err := s.userRepo.RegenerateAPIKey(c.Context(), user.UserID)
	if err != nil {
		return RespondInternalError(c, "Failed to regenerate API key", err.Error())
	}

	// If key_override is configured (has a value with 33 chars), update it with the new key
	if s.configManager != nil {
		cfg := s.configManager.GetConfig()
		if cfg.API.KeyOverride != "" && len(cfg.API.KeyOverride) == 33 {
			// Update the key_override in config to match the new key
			newConfig := cfg.DeepCopy()
			newConfig.API.KeyOverride = apiKey

			if err := s.configManager.UpdateConfig(newConfig); err != nil {
				slog.WarnContext(c.Context(), "Failed to update key_override in config", "error", err)
				// Don't fail the request, just log the warning
			} else {
				if err := s.configManager.SaveConfig(); err != nil {
					slog.WarnContext(c.Context(), "Failed to save config after updating key_override", "error", err)
				} else {
					slog.InfoContext(c.Context(), "Updated key_override in config with new API key")
				}
			}
		}
	}

	response := fiber.Map{
		"api_key": apiKey,
		"message": "API key regenerated successfully",
	}
	return RespondSuccess(c, response)
}

// mapUserToResponse converts database User to API UserResponse
func (s *Server) mapUserToResponse(user *database.User) *UserResponse {
	// Use username as display name if no name is set
	displayName := user.UserID
	if user.Name != nil && *user.Name != "" {
		displayName = *user.Name
	}

	response := &UserResponse{
		ID:       user.UserID,
		Name:     displayName,
		Provider: user.Provider,
		IsAdmin:  user.IsAdmin,
	}

	if user.Email != nil {
		response.Email = *user.Email
	}

	if user.AvatarURL != nil {
		response.AvatarURL = *user.AvatarURL
	}

	if user.LastLogin != nil {
		response.LastLogin = user.LastLogin.Format("2006-01-02T15:04:05Z")
	}

	if user.APIKey != nil {
		response.APIKey = *user.APIKey
	}

	return response
}

// ResetAdminPasswordRequest for resetting admin password while login is disabled
type ResetAdminPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

// handleResetAdminPassword allows resetting an admin password when login is disabled.
// Only available when login_required is false — caller already has full admin access in that state.
//
//	@Summary		Reset admin password
//	@Description	Resets the server password. Only available when login is disabled.
//	@Tags			Auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		ResetAdminPasswordRequest	true	"Reset credentials"
//	@Success		200		{object}	APIResponse
//	@Failure		400		{object}	APIResponse
//	@Failure		403		{object}	APIResponse
//	@Router			/auth/reset-admin-password [post]
func (s *Server) handleResetAdminPassword(c *fiber.Ctx) error {
	if s.isPasswordLoginRequired() {
		return RespondForbidden(c, "Password reset is only available when login is disabled", "")
	}

	var req ResetAdminPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return RespondBadRequest(c, "Invalid request body", err.Error())
	}
	if req.NewPassword == "" {
		return RespondBadRequest(c, "New password is required", "")
	}
	if len(req.NewPassword) < 12 {
		return RespondValidationError(c, "Password must be at least 12 characters", "")
	}

	hash, err := hashServerPassword(req.NewPassword)
	if err != nil {
		return RespondInternalError(c, "Failed to hash password", err.Error())
	}

	if err := s.setServerPasswordHash(hash); err != nil {
		return RespondInternalError(c, "Failed to update password", err.Error())
	}

	slog.InfoContext(c.Context(), "Server password reset while login disabled")
	return RespondMessage(c, "Password updated successfully")
}
