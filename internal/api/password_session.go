package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TaterTotterson/tater-tube-server/internal/auth"
	"github.com/TaterTotterson/tater-tube-server/internal/database"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/crypto/bcrypt"
)

const passwordSessionCookieName = "TT_SESSION"

var passwordSessionDuration = 10 * 365 * 24 * time.Hour

func passwordAdminUser() *database.User {
	name := "Admin"
	return &database.User{
		UserID:   "admin",
		Name:     &name,
		Provider: "password",
		IsAdmin:  true,
	}
}

func hashServerPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func compareServerPassword(hash, password string) bool {
	return hash != "" && bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (s *Server) getServerPasswordHash() string {
	if s.configManager == nil {
		return ""
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return ""
	}
	return cfg.Auth.PasswordHash
}

func (s *Server) isServerPasswordConfigured() bool {
	return s.getServerPasswordHash() != ""
}

func (s *Server) setServerPasswordHash(hash string) error {
	if s.configManager == nil {
		return fmt.Errorf("config manager unavailable")
	}
	cfg := s.configManager.GetConfig()
	if cfg == nil {
		return fmt.Errorf("config unavailable")
	}

	next := cfg.DeepCopy()
	next.Auth.PasswordHash = hash
	if err := s.configManager.UpdateConfig(next); err != nil {
		return err
	}
	return s.configManager.SaveConfig()
}

func (s *Server) clearServerPasswordHash() error {
	return s.setServerPasswordHash("")
}

func (s *Server) passwordSessionKey() []byte {
	sum := sha256.Sum256([]byte("tater-tube-server-session:" + s.getServerPasswordHash()))
	return sum[:]
}

func (s *Server) signPasswordSession(message string) string {
	mac := hmac.New(sha256.New, s.passwordSessionKey())
	mac.Write([]byte(message))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) newPasswordSessionToken() (string, time.Time, error) {
	nonceBytes := make([]byte, 24)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", time.Time{}, err
	}

	expires := time.Now().Add(passwordSessionDuration)
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	message := fmt.Sprintf("%d.%s", expires.Unix(), nonce)
	return message + "." + s.signPasswordSession(message), expires, nil
}

func (s *Server) validatePasswordSessionToken(token string) bool {
	if !s.isServerPasswordConfigured() {
		return false
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}

	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expiresUnix {
		return false
	}

	message := parts[0] + "." + parts[1]
	expected := s.signPasswordSession(message)
	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[2])) == 1
}

func (s *Server) setPasswordSessionCookie(c *fiber.Ctx) error {
	token, expires, err := s.newPasswordSessionToken()
	if err != nil {
		return err
	}

	c.Cookie(&fiber.Cookie{
		Name:     passwordSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		Secure:   c.Protocol() == "https",
		HTTPOnly: true,
		SameSite: "Lax",
	})
	return nil
}

func clearPasswordSessionCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     passwordSessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Now().Add(-time.Hour),
		Secure:   c.Protocol() == "https",
		HTTPOnly: true,
		SameSite: "Lax",
	})
}

func (s *Server) attachPasswordSessionUser(c *fiber.Ctx) bool {
	if !s.validatePasswordSessionToken(c.Cookies(passwordSessionCookieName)) {
		return false
	}
	c.Locals(string(auth.UserContextKey), passwordAdminUser())
	return true
}

func (s *Server) validatePasswordSessionRequest(r *http.Request) bool {
	cookie, err := r.Cookie(passwordSessionCookieName)
	if err != nil {
		return false
	}
	return s.validatePasswordSessionToken(cookie.Value)
}
