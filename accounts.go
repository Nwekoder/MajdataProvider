package main

import (
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	sessionCookieName = "majdata_session"
	sessionDuration   = 30 * 24 * time.Hour
	argonMemory       = 64 * 1024
	argonIterations   = 2
	argonParallelism  = 1
	argonKeyLength    = 32
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)
var passwordHashSlots = make(chan struct{}, 2)
var dummyPasswordSalt = []byte("MajdataProviderDummySalt")

type application struct {
	store         *sql.DB
	inviteCode    string
	danAdminToken string
	catalog       map[string]chartCatalogEntry
}

type account struct {
	ID        string
	Username  string
	CreatedAt time.Time
}

type apiStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

var registrationTemplate = template.Must(template.New("register").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Register - Majdata Provider</title>
</head>
<body>
  <main>
    <h1>Create an account</h1>
    {{if .Message}}<p role="status">{{.Message}}</p>{{end}}
    {{if .Enabled}}
    <form method="post" action="/register">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <label>Invite code <input name="invite_code" type="password" required autocomplete="off"></label>
      <label>Username <input name="username" minlength="3" maxlength="32" pattern="[A-Za-z0-9_.-]+" required autocomplete="username"></label>
      <label>Password <input name="password" type="password" minlength="10" maxlength="128" required autocomplete="new-password"></label>
      <button type="submit">Register</button>
    </form>
    {{end}}
  </main>
</body>
</html>`))

func (app *application) serveRegistration(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/register" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if app.inviteCode == "" {
		app.writeRegistrationPage(w, r, "", "Registration is not configured by the server administrator.", false)
		return
	}
	if r.Method == http.MethodGet {
		token, err := randomToken(32)
		if err != nil {
			log.Printf("registration_csrf_error err=%v", err)
			http.Error(w, "registration is temporarily unavailable", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "majdata_register_csrf",
			Value:    token,
			Path:     "/register",
			Secure:   isSecureRequest(r),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   3600,
		})
		app.writeRegistrationPage(w, r, token, "", true)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid registration form", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie("majdata_register_csrf")
	if err != nil || !validCSRF(cookie.Value, r.FormValue("csrf")) {
		app.writeRegistrationPage(w, r, "", "The form expired. Reload the page and try again.", true)
		return
	}
	csrfToken := cookie.Value

	invite := r.FormValue("invite_code")
	if len(invite) != len(app.inviteCode) || subtle.ConstantTimeCompare([]byte(invite), []byte(app.inviteCode)) != 1 {
		app.writeRegistrationPage(w, r, csrfToken, "The invite code is invalid.", true)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	if !usernamePattern.MatchString(username) || !validRegistrationPassword(password) {
		app.writeRegistrationPage(w, r, csrfToken, "Use a 3-32 character username (letters, numbers, _, . or -) and a 10-128 character password.", true)
		return
	}

	if _, err := app.createAccount(r, username, password); err != nil {
		if errors.Is(err, errUsernameExists) {
			app.writeRegistrationPage(w, r, csrfToken, "That username is already in use.", true)
			return
		}
		log.Printf("registration_error err=%v", err)
		http.Error(w, "account registration failed", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/register?success=1", http.StatusSeeOther)
}

func (app *application) writeRegistrationPage(w http.ResponseWriter, r *http.Request, csrf, message string, enabled bool) {
	if r.URL.Query().Get("success") == "1" {
		message = "Account created. You can now log in from MajdataPlay."
		enabled = false
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := registrationTemplate.Execute(w, struct {
		CSRF    string
		Message string
		Enabled bool
	}{CSRF: csrf, Message: message, Enabled: enabled}); err != nil {
		log.Printf("registration_page_error err=%v", err)
	}
}

func validRegistrationPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 10 && utf8.RuneCountInString(password) <= 128
}

var errUsernameExists = errors.New("username already exists")

func (app *application) createAccount(r *http.Request, username, password string) (account, error) {
	id, err := randomToken(16)
	if err != nil {
		return account{}, fmt.Errorf("generate account ID: %w", err)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return account{}, fmt.Errorf("generate password salt: %w", err)
	}
	digest := md5.Sum([]byte(password))
	passwordHash, err := derivePasswordHash(r, []byte(hex.EncodeToString(digest[:])), salt)
	if err != nil {
		return account{}, fmt.Errorf("hash account password: %w", err)
	}
	createdAt := time.Now().UTC()
	_, err = app.store.ExecContext(r.Context(),
		"INSERT INTO users (id, username, password_salt, password_hash, created_at) VALUES (?, ?, ?, ?, ?)",
		id, username, salt, passwordHash, createdAt.Format(time.RFC3339Nano))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return account{}, errUsernameExists
		}
		return account{}, fmt.Errorf("insert account: %w", err)
	}
	return account{ID: id, Username: username, CreatedAt: createdAt}, nil
}

func (app *application) serveLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeAPIStatus(w, http.StatusBadRequest, 40001, "multipart form required")
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	passwordDigest := strings.ToLower(r.FormValue("password"))
	passwordBytes, err := hex.DecodeString(passwordDigest)
	if err != nil || len(passwordBytes) != md5.Size || !usernamePattern.MatchString(username) {
		writeAPIStatus(w, http.StatusUnauthorized, 60003, "invalid credentials")
		return
	}

	var user account
	var salt, expected []byte
	var createdAt string
	err = app.store.QueryRowContext(r.Context(),
		"SELECT id, username, password_salt, password_hash, created_at FROM users WHERE username = ?",
		username).Scan(&user.ID, &user.Username, &salt, &expected, &createdAt)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("login_lookup_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "login failed")
			return
		}
		_, hashErr := derivePasswordHash(r, []byte(passwordDigest), dummyPasswordSalt)
		if hashErr != nil {
			writeAPIStatus(w, http.StatusServiceUnavailable, -114514, "login is temporarily unavailable")
			return
		}
		writeAPIStatus(w, http.StatusUnauthorized, 60003, "invalid credentials")
		return
	}
	actual, err := derivePasswordHash(r, []byte(passwordDigest), salt)
	if err != nil {
		writeAPIStatus(w, http.StatusServiceUnavailable, -114514, "login is temporarily unavailable")
		return
	}
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		writeAPIStatus(w, http.StatusUnauthorized, 60003, "invalid credentials")
		return
	}

	token, err := randomToken(32)
	if err != nil {
		log.Printf("login_session_error err=%v", err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "login failed")
		return
	}
	expiry := time.Now().UTC().Add(sessionDuration)
	tokenHash := sha256.Sum256([]byte(token))
	if _, err := app.store.ExecContext(r.Context(), "DELETE FROM sessions WHERE expires_at <= ?", time.Now().Unix()); err != nil {
		log.Printf("session_cleanup_error err=%v", err)
	}
	if _, err := app.store.ExecContext(r.Context(),
		"INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)",
		tokenHash[:], user.ID, expiry.Unix()); err != nil {
		log.Printf("login_session_error err=%v", err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "login failed")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, apiStatus{Code: 114514, Message: "success"})
}

func (app *application) requireAccount(next func(http.ResponseWriter, *http.Request, account)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			writeAPIStatus(w, http.StatusUnauthorized, 60000, "not logged in")
			return
		}
		tokenHash := sha256.Sum256([]byte(cookie.Value))
		var user account
		var createdAt string
		err = app.store.QueryRowContext(r.Context(),
			`SELECT users.id, users.username, users.created_at
			 FROM sessions JOIN users ON sessions.user_id = users.id
			 WHERE sessions.token_hash = ? AND sessions.expires_at > ?`,
			tokenHash[:], time.Now().Unix()).Scan(&user.ID, &user.Username, &createdAt)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("session_lookup_error err=%v", err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "request failed")
				return
			}
			writeAPIStatus(w, http.StatusUnauthorized, 60000, "not logged in")
			return
		}
		user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			log.Printf("invalid_account_timestamp user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "request failed")
			return
		}
		next(w, r, user)
	}
}

func (app *application) serveProfile(w http.ResponseWriter, r *http.Request, user account) {
	writeJSON(w, http.StatusOK, struct {
		ID       string    `json:"id"`
		Username string    `json:"username"`
		Nickname *string   `json:"nickname"`
		Email    *string   `json:"email"`
		AvatarID *string   `json:"avatarId"`
		JoinDate time.Time `json:"joinDate"`
	}{
		ID:       user.ID,
		Username: user.Username,
		JoinDate: user.CreatedAt,
	})
}

func (app *application) serveLogout(w http.ResponseWriter, r *http.Request, user account) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		tokenHash := sha256.Sum256([]byte(cookie.Value))
		if _, err := app.store.ExecContext(r.Context(), "DELETE FROM sessions WHERE token_hash = ?", tokenHash[:]); err != nil {
			log.Printf("logout_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "logout failed")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, apiStatus{Code: 114514, Message: "success"})
}

func writeAPIStatus(w http.ResponseWriter, status, code int, message string) {
	writeJSON(w, status, apiStatus{Code: code, Message: message})
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func validCSRF(cookieToken, formToken string) bool {
	return cookieToken != "" && formToken != "" && subtle.ConstantTimeCompare([]byte(cookieToken), []byte(formToken)) == 1
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func derivePasswordHash(r *http.Request, digest, salt []byte) ([]byte, error) {
	select {
	case passwordHashSlots <- struct{}{}:
		defer func() { <-passwordHashSlots }()
	case <-r.Context().Done():
		return nil, r.Context().Err()
	}
	return argon2.IDKey(digest, salt, argonIterations, argonMemory, argonParallelism, argonKeyLength), nil
}
