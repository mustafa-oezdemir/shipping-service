package portal

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"gorm.io/gorm"
)

const currentUserKey = "portal_current_user"

type SessionManager struct {
	db     *gorm.DB
	secret []byte
	secure bool
	ttl    time.Duration
}

func NewSessionManager(db *gorm.DB, secret string, secure bool, ttl time.Duration) *SessionManager {
	return &SessionManager{db: db, secret: []byte(secret), secure: secure, ttl: ttl}
}

func (manager *SessionManager) Start(c *gin.Context, user *models.User) error {
	manager.Destroy(c)
	_ = manager.db.WithContext(c.Request.Context()).Delete(&models.BrowserSession{}, "expires_at <= ?", time.Now().UTC()).Error
	token, err := randomHex(32)
	if err != nil {
		return err
	}
	session := models.BrowserSession{TokenHash: hashToken(token), UserID: user.ID, SecurityVersion: user.SecurityVersion, ExpiresAt: time.Now().UTC().Add(manager.ttl)}
	if err := manager.db.WithContext(c.Request.Context()).Create(&session).Error; err != nil {
		return err
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "shipping_session", Value: token, Path: "/", MaxAge: int(manager.ttl.Seconds()), HttpOnly: true, Secure: manager.secure, SameSite: http.SameSiteLaxMode})
	return nil
}

func (manager *SessionManager) Destroy(c *gin.Context) {
	if cookie, err := c.Cookie("shipping_session"); err == nil && cookie != "" {
		_ = manager.db.WithContext(c.Request.Context()).Delete(&models.BrowserSession{}, "token_hash = ?", hashToken(cookie)).Error
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: "shipping_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: manager.secure, SameSite: http.SameSiteLaxMode})
}

func (manager *SessionManager) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("shipping_session")
		if err != nil || len(token) != 64 {
			manager.unauthorized(c)
			return
		}
		var session models.BrowserSession
		if err := manager.db.WithContext(c.Request.Context()).Where("token_hash = ? AND expires_at > ?", hashToken(token), time.Now().UTC()).First(&session).Error; err != nil {
			manager.Destroy(c)
			manager.unauthorized(c)
			return
		}
		var user models.User
		if err := manager.db.WithContext(c.Request.Context()).First(&user, session.UserID).Error; err != nil || !user.IsActive || user.SecurityVersion != session.SecurityVersion {
			manager.Destroy(c)
			manager.unauthorized(c)
			return
		}
		c.Set(currentUserKey, &user)
		c.Set("shipping_role", user.Role)
		c.Next()
	}
}

func (manager *SessionManager) RequireRole(role models.Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := CurrentUser(c)
		if !ok || user.Role != role {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func (manager *SessionManager) VerifyCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		token, err := c.Cookie("shipping_session")
		provided := c.PostForm("csrf_token")
		if provided == "" {
			provided = c.GetHeader("X-CSRF-Token")
		}
		expected := manager.CSRFToken(token)
		if err != nil || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func (manager *SessionManager) CSRFToken(sessionToken string) string {
	mac := hmac.New(sha256.New, manager.secret)
	_, _ = mac.Write([]byte("csrf:" + sessionToken))
	return hex.EncodeToString(mac.Sum(nil))
}

func (manager *SessionManager) TokenFor(c *gin.Context) string {
	token, _ := c.Cookie("shipping_session")
	return manager.CSRFToken(token)
}

func (manager *SessionManager) unauthorized(c *gin.Context) {
	if strings.Contains(c.GetHeader("Accept"), "application/json") {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	c.Redirect(http.StatusFound, "/login")
	c.Abort()
}

func CurrentUser(c *gin.Context) (*models.User, bool) {
	value, exists := c.Get(currentUserKey)
	user, ok := value.(*models.User)
	return user, exists && ok && user != nil
}

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ParseID(raw string) (uint, error) {
	id, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(id), nil
}
