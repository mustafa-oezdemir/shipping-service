package portal

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"net/mail"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Handler struct {
	db        *gorm.DB
	shipments *services.ShipmentService
	sessions  *SessionManager
	limiter   *LoginLimiter
	images    *ImageStore
	publicURL string
	secure    bool
	dummyHash []byte
}

func New(db *gorm.DB, shipmentService *services.ShipmentService, sessionSecret, imageDirectory, publicURL string, secure bool, ttl time.Duration) (*Handler, error) {
	images, err := NewImageStore(imageDirectory)
	if err != nil {
		return nil, err
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("timing-only-invalid-password"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Handler{db: db, shipments: shipmentService, sessions: NewSessionManager(db, sessionSecret, secure, ttl), limiter: NewLoginLimiter(6, 10*time.Minute), images: images, publicURL: strings.TrimRight(publicURL, "/"), secure: secure, dummyHash: dummyHash}, nil
}

func BootstrapAdmin(db *gorm.DB, email, password, firstName, lastName string) error {
	if email == "" && password == "" {
		return nil
	}
	if err := validatePassword(password); err != nil {
		return fmt.Errorf("initial admin password: %w", err)
	}
	if !validEmail(email) {
		return errors.New("initial admin email is invalid")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	user := models.User{FirstName: strings.TrimSpace(firstName), LastName: strings.TrimSpace(lastName), Email: strings.ToLower(strings.TrimSpace(email)), PasswordHash: string(hash), Role: models.RoleAdmin, IsActive: true, SecurityVersion: 1}
	result := db.Where("email = ?", user.Email).FirstOrCreate(&user)
	return result.Error
}

func (h *Handler) Register(router *gin.Engine) {
	router.GET("/login", h.ShowLogin)
	router.POST("/login", h.Login)
	authenticated := router.Group("")
	authenticated.Use(h.sessions.RequireAuth(), h.sessions.VerifyCSRF())
	authenticated.POST("/logout", h.Logout)
	authenticated.GET("/dashboard", h.Dashboard)
	authenticated.GET("/shipments", h.Shipments)
	authenticated.GET("/shipments/:id", h.Shipment)
	authenticated.GET("/shipments/:id/label", h.Label)
	authenticated.POST("/shipments/:id/status", h.TransitionShipment)
	authenticated.POST("/shipments/:id/stops", h.UpdateStops)
	authenticated.GET("/returns", h.Returns)
	authenticated.GET("/returns/:id", h.Shipment)
	authenticated.GET("/scan", h.Scan)
	authenticated.POST("/scan", h.ScanResult)
	authenticated.GET("/profile", h.Profile)
	authenticated.POST("/profile", h.UpdateProfile)
	authenticated.POST("/profile/image", h.UpdateProfileImage)
	authenticated.POST("/profile/image/delete", h.DeleteProfileImage)
	authenticated.POST("/profile/password", h.ChangePassword)
	authenticated.GET("/profile/images/:filename", h.ProfileImage)
	admin := authenticated.Group("/admin")
	admin.Use(h.sessions.RequireRole(models.RoleAdmin))
	admin.GET("/dashboard", h.AdminDashboard)
	admin.GET("/shipments", h.AdminShipments)
	admin.GET("/users", h.Users)
	admin.GET("/users/new", h.NewUser)
	admin.POST("/users", h.CreateUser)
	admin.GET("/users/:id", h.UserDetail)
	admin.POST("/users/:id", h.UpdateUser)
	admin.POST("/users/:id/status", h.ChangeUserStatus)
	admin.POST("/users/:id/role", h.ChangeUserRole)
	admin.GET("/audit", h.Audit)
}

func (h *Handler) ShowLogin(c *gin.Context) {
	token, _ := randomHex(24)
	http.SetCookie(c.Writer, &http.Cookie{Name: "shipping_login_csrf", Value: token, Path: "/login", MaxAge: 600, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode})
	c.HTML(http.StatusOK, "login.tmpl", gin.H{"CSRF": token})
}

func (h *Handler) Login(c *gin.Context) {
	cookie, err := c.Cookie("shipping_login_csrf")
	if err != nil || subtle.ConstantTimeCompare([]byte(cookie), []byte(c.PostForm("csrf_token"))) != 1 {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	if len(email) > 254 {
		email = ""
	}
	key := c.ClientIP() + "|" + email
	if !h.limiter.Allow(key) {
		h.loginFailure(c)
		return
	}
	var user models.User
	err = h.db.WithContext(c.Request.Context()).Where("email = ?", email).First(&user).Error
	hash := h.dummyHash
	if err == nil {
		hash = []byte(user.PasswordHash)
	}
	passwordMatches := bcrypt.CompareHashAndPassword(hash, []byte(c.PostForm("password"))) == nil
	if err != nil || !user.IsActive || !passwordMatches {
		h.audit(c, nil, "login_failed", "user", email, "", "")
		h.loginFailure(c)
		return
	}
	now := time.Now().UTC()
	user.LastLoginAt = &now
	if err := h.db.WithContext(c.Request.Context()).Model(&user).Update("last_login_at", now).Error; err != nil || h.sessions.Start(c, &user) != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	h.limiter.Reset(key)
	http.SetCookie(c.Writer, &http.Cookie{Name: "shipping_login_csrf", Value: "", Path: "/login", MaxAge: -1, HttpOnly: true, Secure: h.secure, SameSite: http.SameSiteLaxMode})
	h.audit(c, &user.ID, "login", "user", strconv.Itoa(int(user.ID)), "", "")
	if user.Role == models.RoleAdmin {
		c.Redirect(http.StatusFound, "/admin/dashboard")
	} else {
		c.Redirect(http.StatusFound, "/dashboard")
	}
}

func (h *Handler) loginFailure(c *gin.Context) {
	c.HTML(http.StatusUnauthorized, "login.tmpl", gin.H{"Error": "Invalid email or password.", "CSRF": c.PostForm("csrf_token")})
}

func (h *Handler) Logout(c *gin.Context) {
	user, _ := CurrentUser(c)
	h.audit(c, &user.ID, "logout", "user", strconv.Itoa(int(user.ID)), "", "")
	h.sessions.Destroy(c)
	c.Redirect(http.StatusFound, "/login")
}

func (h *Handler) base(c *gin.Context) gin.H {
	user, _ := CurrentUser(c)
	return gin.H{"User": user, "CSRF": h.sessions.TokenFor(c)}
}

func (h *Handler) Dashboard(c *gin.Context) {
	user, _ := CurrentUser(c)
	if user.Role == models.RoleAdmin {
		c.Redirect(http.StatusFound, "/admin/dashboard")
		return
	}
	data := h.base(c)
	data["Counts"] = h.statusCounts(c, false)
	data["Recent"] = h.userActivity(c, user.ID, 10)
	c.HTML(http.StatusOK, "employee_dashboard.tmpl", data)
}

func (h *Handler) AdminDashboard(c *gin.Context) {
	data := h.base(c)
	data["Counts"] = h.statusCounts(c, false)
	data["ReturnCounts"] = h.statusCounts(c, true)
	var todayShipments, deliveredToday int64
	h.db.Model(&models.Shipment{}).Where("created_at >= ?", startOfDay()).Count(&todayShipments)
	h.db.Model(&models.Shipment{}).Where("delivered_at >= ?", startOfDay()).Count(&deliveredToday)
	data["TodayShipments"], data["DeliveredToday"] = todayShipments, deliveredToday
	var active int64
	h.db.Model(&models.User{}).Where("is_active = ? AND role = ?", true, models.RoleEmployee).Count(&active)
	data["ActiveEmployees"] = active
	data["Recent"] = h.userActivity(c, 0, 12)
	var workload []struct {
		FirstName, LastName string
		Actions             int64
	}
	h.db.Table("audit_logs a").Select("u.first_name, u.last_name, count(*) actions").Joins("join users u on u.id = a.actor_user_id").Where("a.created_at >= ?", startOfDay()).Group("u.id, u.first_name, u.last_name").Order("actions desc").Limit(10).Scan(&workload)
	data["Workload"] = workload
	c.HTML(http.StatusOK, "admin_dashboard.tmpl", data)
}

func (h *Handler) statusCounts(c *gin.Context, returns bool) map[string]int64 {
	var rows []struct {
		Status string
		Count  int64
	}
	h.db.WithContext(c.Request.Context()).Model(&models.Shipment{}).Select("status, count(*) count").Where("is_return = ?", returns).Group("status").Scan(&rows)
	result := map[string]int64{}
	for _, row := range rows {
		result[row.Status] = row.Count
	}
	return result
}

func (h *Handler) userActivity(c *gin.Context, userID uint, limit int) []models.AuditLog {
	var logs []models.AuditLog
	query := h.db.WithContext(c.Request.Context()).Order("created_at desc").Limit(limit)
	if userID != 0 {
		query = query.Where("actor_user_id = ?", userID)
	}
	query.Find(&logs)
	return logs
}

func page(c *gin.Context) (int, int) {
	value, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if value < 1 {
		value = 1
	}
	return value, 25
}

func (h *Handler) Shipments(c *gin.Context)      { h.shipmentList(c, false, false) }
func (h *Handler) Returns(c *gin.Context)        { h.shipmentList(c, true, false) }
func (h *Handler) AdminShipments(c *gin.Context) { h.shipmentList(c, false, true) }
func (h *Handler) shipmentList(c *gin.Context, returns, admin bool) {
	p, size := page(c)
	var shipments []models.Shipment
	query := h.db.WithContext(c.Request.Context()).Where("is_return = ?", returns)
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	query.Order("updated_at desc").Limit(size).Offset((p - 1) * size).Find(&shipments)
	data := h.base(c)
	data["Shipments"] = shipments
	data["Page"] = p
	data["Admin"] = admin
	c.HTML(http.StatusOK, "shipments.tmpl", data)
}

func (h *Handler) Shipment(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	shipment, err := h.shipments.GetInternal(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	data := h.base(c)
	data["Shipment"] = shipment
	data["NextStatuses"] = nextStatuses(shipment.Status)
	c.HTML(http.StatusOK, "portal_shipment.tmpl", data)
}

func nextStatuses(status models.ShipmentStatus) []models.ShipmentStatus {
	all := []models.ShipmentStatus{models.StatusReceivedAtOrigin, models.StatusSorting, models.StatusInTransit, models.StatusArrivedDestinationHub, models.StatusOutForDelivery, models.StatusDelivered, models.StatusDeliveryFailed, models.StatusDeliveryRescheduled, models.StatusReturnRequested, models.StatusReturnAuthorized, models.StatusReturnLabelCreated, models.StatusReturnInTransit, models.StatusReturnReceived, models.StatusReturnCompleted}
	result := []models.ShipmentStatus{}
	for _, next := range all {
		if status.CanTransitionTo(next) {
			result = append(result, next)
		}
	}
	return result
}

func (h *Handler) TransitionShipment(c *gin.Context) {
	user, _ := CurrentUser(c)
	expected := models.ShipmentStatus(c.PostForm("expected_status"))
	next := models.ShipmentStatus(c.PostForm("status"))
	shipment, _, err := h.shipments.Transition(c.Request.Context(), c.Param("id"), expected, next, user.Role, strconv.Itoa(int(user.ID)), "portal-"+middleware.CurrentRequestID(c), middleware.CurrentRequestID(c), "shipping-portal")
	if err != nil {
		c.String(http.StatusConflict, "Invalid or unauthorized shipment transition")
		return
	}
	c.Redirect(http.StatusSeeOther, "/shipments/"+strconv.Itoa(int(shipment.ID)))
}

func (h *Handler) Label(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	shipment, err := h.shipments.GetInternal(c.Request.Context(), id)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.HTML(http.StatusOK, "label.tmpl", gin.H{"Shipment": shipment, "TrackingURL": h.publicURL + "/track/" + url.PathEscape(shipment.TrackingNumber)})
}

func (h *Handler) UpdateStops(c *gin.Context) {
	user, _ := CurrentUser(c)
	stops, err := strconv.Atoi(c.PostForm("remaining_stops"))
	if err != nil {
		c.AbortWithStatus(422)
		return
	}
	_, _, err = h.shipments.UpdateStops(c.Request.Context(), c.Param("id"), stops, user.Role, strconv.Itoa(int(user.ID)), "portal-stops-"+middleware.CurrentRequestID(c), middleware.CurrentRequestID(c), "shipping-portal")
	if err != nil {
		c.AbortWithStatus(409)
		return
	}
	c.Redirect(http.StatusFound, "/shipments")
}

func (h *Handler) Scan(c *gin.Context) { c.HTML(http.StatusOK, "scan.tmpl", h.base(c)) }
func (h *Handler) ScanResult(c *gin.Context) {
	value := strings.TrimSpace(c.PostForm("tracking_number"))
	var shipment models.Shipment
	if value == "" || h.db.WithContext(c.Request.Context()).Where("tracking_number = ? OR public_id = ?", value, value).First(&shipment).Error != nil {
		data := h.base(c)
		data["Error"] = "Shipment not found"
		c.HTML(http.StatusNotFound, "scan.tmpl", data)
		return
	}
	user, _ := CurrentUser(c)
	h.audit(c, &user.ID, "qr_scanned", "shipment", shipment.PublicID, "", string(shipment.Status))
	c.Redirect(http.StatusFound, "/shipments/"+strconv.Itoa(int(shipment.ID)))
}

func (h *Handler) Profile(c *gin.Context) { c.HTML(http.StatusOK, "profile.tmpl", h.base(c)) }
func (h *Handler) UpdateProfile(c *gin.Context) {
	user, _ := CurrentUser(c)
	first, last := strings.TrimSpace(c.PostForm("first_name")), strings.TrimSpace(c.PostForm("last_name"))
	if first == "" || last == "" || len(first) > 100 || len(last) > 100 {
		c.AbortWithStatus(422)
		return
	}
	old, _ := json.Marshal(map[string]string{"first_name": user.FirstName, "last_name": user.LastName})
	if err := h.db.Model(user).Updates(map[string]any{"first_name": first, "last_name": last}).Error; err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	updated, _ := json.Marshal(map[string]string{"first_name": first, "last_name": last})
	h.audit(c, &user.ID, "profile_updated", "user", strconv.Itoa(int(user.ID)), string(old), string(updated))
	c.Redirect(303, "/profile")
}
func (h *Handler) UpdateProfileImage(c *gin.Context) {
	user, _ := CurrentUser(c)
	file, err := c.FormFile("image")
	if err != nil || file.Size > 3<<20 {
		c.AbortWithStatus(422)
		return
	}
	opened, err := file.Open()
	if err != nil {
		c.AbortWithStatus(422)
		return
	}
	defer opened.Close()
	filename, err := h.images.Save(file.Filename, opened)
	if err != nil {
		c.AbortWithStatus(422)
		return
	}
	old := user.ProfileImageFilename
	if err := h.db.Model(user).Update("profile_image_filename", filename).Error; err != nil {
		h.images.Delete(filename)
		c.AbortWithStatus(500)
		return
	}
	h.images.Delete(old)
	h.audit(c, &user.ID, "profile_image_updated", "user", strconv.Itoa(int(user.ID)), "", "")
	c.Redirect(303, "/profile")
}
func (h *Handler) DeleteProfileImage(c *gin.Context) {
	user, _ := CurrentUser(c)
	old := user.ProfileImageFilename
	if h.db.Model(user).Update("profile_image_filename", "").Error != nil {
		c.AbortWithStatus(500)
		return
	}
	h.images.Delete(old)
	h.audit(c, &user.ID, "profile_image_deleted", "user", strconv.Itoa(int(user.ID)), "", "")
	c.Redirect(303, "/profile")
}
func (h *Handler) ProfileImage(c *gin.Context) {
	file, err := h.images.Open(c.Param("filename"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	defer file.Close()
	c.Header("Cache-Control", "private, max-age=3600")
	c.DataFromReader(200, -1, mime.TypeByExtension(filepath.Ext(file.Name())), file, nil)
}

func (h *Handler) ChangePassword(c *gin.Context) {
	user, _ := CurrentUser(c)
	current, next, confirm := c.PostForm("current_password"), c.PostForm("new_password"), c.PostForm("confirm_password")
	if next != confirm || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(current)) != nil || validatePassword(next) != nil {
		data := h.base(c)
		data["PasswordError"] = "Password could not be changed."
		c.HTML(422, "profile.tmpl", data)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	user.SecurityVersion++
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(user).Updates(map[string]any{"password_hash": string(hash), "security_version": user.SecurityVersion}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.BrowserSession{}, "user_id = ?", user.ID).Error
	}); err != nil {
		c.AbortWithStatus(500)
		return
	}
	h.audit(c, &user.ID, "password_changed", "user", strconv.Itoa(int(user.ID)), "", "")
	if err := h.sessions.Start(c, user); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Redirect(303, "/profile")
}

func validatePassword(password string) error {
	if len(password) < 12 || len(password) > 72 {
		return errors.New("must contain 12 to 72 bytes")
	}
	var upper, lower, digit bool
	for _, r := range password {
		if r >= 'A' && r <= 'Z' {
			upper = true
		}
		if r >= 'a' && r <= 'z' {
			lower = true
		}
		if r >= '0' && r <= '9' {
			digit = true
		}
	}
	if !upper || !lower || !digit {
		return errors.New("must contain upper, lower and digit")
	}
	return nil
}

func validEmail(email string) bool {
	normalized := strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(normalized)
	return err == nil && parsed.Address == normalized && len(normalized) <= 254
}

func (h *Handler) Users(c *gin.Context) {
	p, size := page(c)
	var users []models.User
	h.db.Order("created_at desc").Limit(size).Offset((p - 1) * size).Find(&users)
	data := h.base(c)
	data["Users"] = users
	data["Page"] = p
	c.HTML(200, "users.tmpl", data)
}
func (h *Handler) NewUser(c *gin.Context) { c.HTML(200, "user_form.tmpl", h.base(c)) }
func (h *Handler) CreateUser(c *gin.Context) {
	role := models.Role(c.PostForm("role"))
	email := strings.ToLower(strings.TrimSpace(c.PostForm("email")))
	password := c.PostForm("password")
	firstName, lastName := strings.TrimSpace(c.PostForm("first_name")), strings.TrimSpace(c.PostForm("last_name"))
	if !role.IsPersonnelRole() || !validEmail(email) || validatePassword(password) != nil || firstName == "" || lastName == "" || len(firstName) > 100 || len(lastName) > 100 {
		c.AbortWithStatus(422)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	user := models.User{FirstName: firstName, LastName: lastName, Email: email, PasswordHash: string(hash), Role: role, IsActive: true, SecurityVersion: 1}
	if h.db.Create(&user).Error != nil {
		c.AbortWithStatus(409)
		return
	}
	actor, _ := CurrentUser(c)
	h.audit(c, &actor.ID, "user_created", "user", strconv.Itoa(int(user.ID)), "", string(role))
	c.Redirect(303, "/admin/users/"+strconv.Itoa(int(user.ID)))
}
func (h *Handler) UserDetail(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	var user models.User
	if h.db.First(&user, id).Error != nil {
		c.AbortWithStatus(404)
		return
	}
	data := h.base(c)
	data["ManagedUser"] = &user
	data["Recent"] = h.userActivity(c, id, 20)
	c.HTML(200, "user_detail.tmpl", data)
}
func (h *Handler) UpdateUser(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	first, last := strings.TrimSpace(c.PostForm("first_name")), strings.TrimSpace(c.PostForm("last_name"))
	if first == "" || last == "" || len(first) > 100 || len(last) > 100 {
		c.AbortWithStatus(422)
		return
	}
	if h.db.Model(&models.User{}).Where("id = ?", id).Updates(map[string]any{"first_name": first, "last_name": last}).Error != nil {
		c.AbortWithStatus(500)
		return
	}
	actor, _ := CurrentUser(c)
	h.audit(c, &actor.ID, "user_updated", "user", strconv.Itoa(int(id)), "", "")
	c.Redirect(303, "/admin/users/"+strconv.Itoa(int(id)))
}
func (h *Handler) ChangeUserStatus(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	actor, _ := CurrentUser(c)
	active := c.PostForm("active") == "true"
	if id == actor.ID && !active {
		c.String(409, "You cannot disable your own account")
		return
	}
	var target models.User
	if h.db.First(&target, id).Error != nil {
		c.AbortWithStatus(404)
		return
	}
	if !active && target.Role == models.RoleAdmin {
		var count int64
		h.db.Model(&models.User{}).Where("role = ? AND is_active = ?", models.RoleAdmin, true).Count(&count)
		if count <= 1 {
			c.String(409, "The final active administrator cannot be disabled")
			return
		}
	}
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&target).Updates(map[string]any{"is_active": active, "security_version": gorm.Expr("security_version + 1")}).Error; err != nil {
			return err
		}
		if !active {
			return tx.Delete(&models.BrowserSession{}, "user_id = ?", id).Error
		}
		return nil
	}); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	h.audit(c, &actor.ID, map[bool]string{true: "user_enabled", false: "user_disabled"}[active], "user", strconv.Itoa(int(id)), strconv.FormatBool(!active), strconv.FormatBool(active))
	c.Redirect(303, "/admin/users/"+strconv.Itoa(int(id)))
}
func (h *Handler) ChangeUserRole(c *gin.Context) {
	id, err := ParseID(c.Param("id"))
	role := models.Role(c.PostForm("role"))
	if err != nil || !role.IsPersonnelRole() {
		c.AbortWithStatus(422)
		return
	}
	actor, _ := CurrentUser(c)
	if id == actor.ID && role != models.RoleAdmin {
		c.String(409, "You cannot remove your own administrator role")
		return
	}
	var target models.User
	if h.db.First(&target, id).Error != nil {
		c.AbortWithStatus(404)
		return
	}
	if target.Role == models.RoleAdmin && role != models.RoleAdmin {
		var count int64
		h.db.Model(&models.User{}).Where("role = ? AND is_active = ?", models.RoleAdmin, true).Count(&count)
		if count <= 1 {
			c.String(409, "The final active administrator cannot be changed")
			return
		}
	}
	old := target.Role
	if err := h.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&target).Updates(map[string]any{"role": role, "security_version": gorm.Expr("security_version + 1")}).Error; err != nil {
			return err
		}
		return tx.Delete(&models.BrowserSession{}, "user_id = ?", id).Error
	}); err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	h.audit(c, &actor.ID, "role_changed", "user", strconv.Itoa(int(id)), string(old), string(role))
	c.Redirect(303, "/admin/users/"+strconv.Itoa(int(id)))
}

func (h *Handler) Audit(c *gin.Context) {
	p, size := page(c)
	var logs []models.AuditLog
	query := h.db.Order("created_at desc")
	if action := strings.TrimSpace(c.Query("action")); action != "" {
		query = query.Where("action = ?", action)
	}
	if entity := strings.TrimSpace(c.Query("entity_type")); entity != "" {
		query = query.Where("entity_type = ?", entity)
	}
	if user := strings.TrimSpace(c.Query("user")); user != "" {
		query = query.Where("actor_user_id = ?", user)
	}
	if date := strings.TrimSpace(c.Query("date")); date != "" {
		if parsed, err := time.Parse("2006-01-02", date); err == nil {
			query = query.Where("created_at >= ? AND created_at < ?", parsed, parsed.Add(24*time.Hour))
		}
	}
	query.Limit(size).Offset((p - 1) * size).Find(&logs)
	data := h.base(c)
	data["Logs"] = logs
	data["Page"] = p
	c.HTML(200, "audit.tmpl", data)
}

func (h *Handler) audit(c *gin.Context, actor *uint, action, entityType, entityID, oldValue, newValue string) {
	log := models.AuditLog{ActorUserID: actor, ActorType: "personnel", Action: action, EntityType: entityType, EntityID: entityID, OldValue: oldValue, NewValue: newValue, RequestID: middleware.CurrentRequestID(c)}
	if actor != nil {
		log.ActorID = strconv.Itoa(int(*actor))
	}
	_ = h.db.WithContext(c.Request.Context()).Create(&log).Error
}

func startOfDay() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}
