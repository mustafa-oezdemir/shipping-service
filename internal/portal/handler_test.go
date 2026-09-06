package portal

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mustafa-oezdemir/shipping-service/internal/config"
	"github.com/mustafa-oezdemir/shipping-service/internal/middleware"
	"github.com/mustafa-oezdemir/shipping-service/internal/models"
	"github.com/mustafa-oezdemir/shipping-service/internal/services"
	"github.com/mustafa-oezdemir/shipping-service/internal/testutil"
	"github.com/mustafa-oezdemir/shipping-service/web"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func TestPersonnelLoginRBACAndInactiveAccount(t *testing.T) {
	router, handler, database := portalTestRouter(t)
	employee := createPortalUser(t, database, "employee@example.com", models.RoleEmployee, true)
	createPortalUser(t, database, "admin@example.com", models.RoleAdmin, true)
	createPortalUser(t, database, "inactive@example.com", models.RoleEmployee, false)

	employeeCookie := loginPortal(t, router, employee.Email, "ValidPassword123", http.StatusFound)
	request := httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	request.AddCookie(employeeCookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("employee admin access: got %d", response.Code)
	}

	adminCookie := loginPortal(t, router, "admin@example.com", "ValidPassword123", http.StatusFound)
	request = httptest.NewRequest(http.MethodGet, "/admin/users", nil)
	request.AddCookie(adminCookie)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin access: got %d", response.Code)
	}

	loginPortal(t, router, "employee@example.com", "wrong-password", http.StatusUnauthorized)
	loginPortal(t, router, "inactive@example.com", "ValidPassword123", http.StatusUnauthorized)

	request = httptest.NewRequest(http.MethodPost, "/profile", strings.NewReader(url.Values{"first_name": {"Changed"}, "last_name": {"Name"}}.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(employeeCookie)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing csrf: got %d", response.Code)
	}
	_ = handler
}

func TestAdminCreatesEmployeeAndEmployeeTransitionIsAudited(t *testing.T) {
	router, handler, database := portalTestRouter(t)
	admin := createPortalUser(t, database, "admin@example.com", models.RoleAdmin, true)
	employee := createPortalUser(t, database, "employee@example.com", models.RoleEmployee, true)
	adminCookie := loginPortal(t, router, admin.Email, "ValidPassword123", http.StatusFound)
	csrf := handler.sessions.CSRFToken(adminCookie.Value)
	form := url.Values{"csrf_token": {csrf}, "first_name": {"New"}, "last_name": {"Employee"}, "email": {"new@example.com"}, "role": {"employee"}, "password": {"AnotherPassword123"}}
	request := httptest.NewRequest(http.MethodPost, "/admin/users", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(adminCookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("create employee: got %d: %s", response.Code, response.Body.String())
	}
	var created models.User
	if database.Where("email = ?", "new@example.com").First(&created).Error != nil || created.Role != models.RoleEmployee {
		t.Fatal("employee was not created")
	}

	shipment := models.Shipment{PublicID: "shp_portal", ShipmentNumber: "S-1", TrackingNumber: "T-1", ExternalOrderID: "O-1", ExternalCustomerID: "C-1", BusinessKey: "shipment:O-1", SourceService: "test", IdempotencyKey: "create-1", Status: models.StatusHandedOver, Carrier: "DHL", ServiceLevel: "standard", Version: 1}
	if err := database.Create(&shipment).Error; err != nil {
		t.Fatal(err)
	}
	employeeCookie := loginPortal(t, router, employee.Email, "ValidPassword123", http.StatusFound)
	csrf = handler.sessions.CSRFToken(employeeCookie.Value)
	form = url.Values{"csrf_token": {csrf}, "numeric_id": {strconv.Itoa(int(shipment.ID))}, "expected_status": {string(models.StatusHandedOver)}, "status": {string(models.StatusReceivedAtOrigin)}}
	request = httptest.NewRequest(http.MethodPost, "/shipments/"+shipment.PublicID+"/status", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(employeeCookie)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("transition: got %d: %s", response.Code, response.Body.String())
	}
	if err := database.First(&shipment, shipment.ID).Error; err != nil || shipment.Status != models.StatusReceivedAtOrigin {
		t.Fatal("shipment was not transitioned")
	}
	var events, audits int64
	database.Model(&models.ShipmentEvent{}).Where("shipment_id = ?", shipment.ID).Count(&events)
	database.Model(&models.AuditLog{}).Where("shipment_id = ? AND actor_user_id = ?", shipment.ID, employee.ID).Count(&audits)
	if events != 1 || audits != 1 {
		t.Fatalf("expected event and audit, got %d/%d", events, audits)
	}
}

func TestProfileUpdateUsesAuthenticatedUserAndAuditsChange(t *testing.T) {
	router, handler, database := portalTestRouter(t)
	employee := createPortalUser(t, database, "owner@example.com", models.RoleEmployee, true)
	other := createPortalUser(t, database, "other@example.com", models.RoleEmployee, true)
	cookie := loginPortal(t, router, employee.Email, "ValidPassword123", http.StatusFound)

	form := url.Values{
		"csrf_token": {handler.sessions.CSRFToken(cookie.Value)},
		"user_id":    {strconv.Itoa(int(other.ID))},
		"first_name": {"Authenticated"},
		"last_name":  {"Owner"},
	}
	response := portalFormRequest(router, http.MethodPost, "/profile", cookie, form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("profile update: got %d", response.Code)
	}
	if err := database.First(employee, employee.ID).Error; err != nil || employee.FirstName != "Authenticated" {
		t.Fatal("authenticated employee profile was not updated")
	}
	if err := database.First(other, other.ID).Error; err != nil || other.FirstName != "Test" {
		t.Fatal("forged user_id changed another account")
	}
	var audits int64
	database.Model(&models.AuditLog{}).Where("actor_user_id = ? AND action = ?", employee.ID, "profile_updated").Count(&audits)
	if audits != 1 {
		t.Fatalf("expected profile audit, got %d", audits)
	}
}

func TestAdminDisableInvalidatesEmployeeSession(t *testing.T) {
	router, handler, database := portalTestRouter(t)
	admin := createPortalUser(t, database, "admin@example.com", models.RoleAdmin, true)
	employee := createPortalUser(t, database, "employee@example.com", models.RoleEmployee, true)
	adminCookie := loginPortal(t, router, admin.Email, "ValidPassword123", http.StatusFound)
	employeeCookie := loginPortal(t, router, employee.Email, "ValidPassword123", http.StatusFound)

	form := url.Values{"csrf_token": {handler.sessions.CSRFToken(adminCookie.Value)}, "active": {"false"}}
	response := portalFormRequest(router, http.MethodPost, "/admin/users/"+strconv.Itoa(int(employee.ID))+"/status", adminCookie, form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("disable employee: got %d", response.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(employeeCookie)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/login" {
		t.Fatalf("disabled session remained usable: %d %s", response.Code, response.Header().Get("Location"))
	}
	var sessions int64
	database.Model(&models.BrowserSession{}).Where("user_id = ?", employee.ID).Count(&sessions)
	if sessions != 0 {
		t.Fatalf("disabled employee has %d sessions", sessions)
	}
}

func TestProductionSessionCookieAndStaleLoginCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	database := testutil.NewTestDB(t)
	user := createPortalUser(t, database, "secure@example.com", models.RoleEmployee, true)
	manager := NewSessionManager(database, strings.Repeat("s", 32), true, time.Hour)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/login", nil)
	if err := manager.Start(context, user); err != nil {
		t.Fatal(err)
	}
	var sessionCookie *http.Cookie
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == "shipping_session" && cookie.Value != "" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil || !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("insecure production session cookie: %#v", sessionCookie)
	}

	router, _, _ := portalTestRouter(t)
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	request.AddCookie(&http.Cookie{Name: "shipping_session", Value: "stale"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("stale cookie caused login redirect loop: %d", response.Code)
	}
}

func TestLabelUsesCanonicalTrackingURLAndRedirectIgnoresFormID(t *testing.T) {
	router, handler, database := portalTestRouter(t)
	employee := createPortalUser(t, database, "employee@example.com", models.RoleEmployee, true)
	shipment := models.Shipment{PublicID: "shp_label", ShipmentNumber: "S-LABEL", TrackingNumber: "TRK-LABEL", ExternalOrderID: "O-LABEL", ExternalCustomerID: "C-LABEL", BusinessKey: "shipment:O-LABEL", SourceService: "test", IdempotencyKey: "label-create", Status: models.StatusHandedOver, Carrier: "DHL", ServiceLevel: "standard", Version: 1}
	if err := database.Create(&shipment).Error; err != nil {
		t.Fatal(err)
	}
	cookie := loginPortal(t, router, employee.Email, "ValidPassword123", http.StatusFound)
	request := httptest.NewRequest(http.MethodGet, "/shipments/"+strconv.Itoa(int(shipment.ID))+"/label", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "https://pehlione-shipping.com/track/TRK-LABEL") {
		t.Fatalf("label canonical URL missing: %d %s", response.Code, response.Body.String())
	}

	form := url.Values{"csrf_token": {handler.sessions.CSRFToken(cookie.Value)}, "numeric_id": {"999999"}, "expected_status": {string(models.StatusHandedOver)}, "status": {string(models.StatusReceivedAtOrigin)}}
	response = portalFormRequest(router, http.MethodPost, "/shipments/"+shipment.PublicID+"/status", cookie, form)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/shipments/"+strconv.Itoa(int(shipment.ID)) {
		t.Fatalf("transition trusted forged redirect id: %d %s", response.Code, response.Header().Get("Location"))
	}
}

func portalTestRouter(t *testing.T) (*gin.Engine, *Handler, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	database := testutil.NewTestDB(t)
	service := services.NewShipmentService(database, config.Warehouse{Name: "Warehouse", Street: "Street", HouseNumber: "1", PostalCode: "10000", City: "Berlin", CountryCode: "DE"})
	handler, err := New(database, service, strings.Repeat("s", 32), t.TempDir(), "https://pehlione-shipping.com", false, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	templates, err := web.ParseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.SetHTMLTemplate(templates)
	router.Use(middleware.RequestMetadata("shipping-service"))
	handler.Register(router)
	return router, handler, database
}

func createPortalUser(t *testing.T, database *gorm.DB, email string, role models.Role, active bool) *models.User {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte("ValidPassword123"), bcrypt.MinCost)
	user := &models.User{FirstName: "Test", LastName: "User", Email: email, PasswordHash: string(hash), Role: role, IsActive: active, SecurityVersion: 1}
	if err := database.Create(user).Error; err != nil {
		t.Fatal(err)
	}
	if !active {
		database.Model(user).Update("is_active", false)
	}
	return user
}

func loginPortal(t *testing.T, router http.Handler, email, password string, want int) *http.Cookie {
	t.Helper()
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/login", nil))
	match := regexp.MustCompile(`name="csrf_token" value="([a-f0-9]+)"`).FindStringSubmatch(get.Body.String())
	if len(match) != 2 {
		t.Fatal("login csrf missing")
	}
	var csrfCookie *http.Cookie
	for _, cookie := range get.Result().Cookies() {
		if cookie.Name == "shipping_login_csrf" {
			csrfCookie = cookie
		}
	}
	form := url.Values{"csrf_token": {match[1]}, "email": {email}, "password": {password}}
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(csrfCookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("login %s: got %d want %d", email, response.Code, want)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "shipping_session" && cookie.Value != "" {
			return cookie
		}
	}
	return &http.Cookie{Name: "shipping_session", Value: "invalid"}
}

func portalFormRequest(router http.Handler, method, target string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
