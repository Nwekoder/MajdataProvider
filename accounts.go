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
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	sessionCookieName = "token"
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
	store      *sql.DB
	inviteCode string
	catalog    map[string]chartCatalogEntry
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

func validRegistrationPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= 10 && utf8.RuneCountInString(password) <= 128
}

var errUsernameExists = errors.New("username already exists")

func (app *application) createAccount(r *http.Request, username, password string) (account, error) {
	id, err := randomObjectID()
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
		"INSERT INTO users (id, username, nickname, password_salt, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, username, username, salt, passwordHash, createdAt.Format(time.RFC3339Nano))
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
	var username, passwordDigest string
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := decodeJSONBody(w, r, 16<<10, &request); err != nil || request.Password == "" {
			writeAPIStatus(w, http.StatusBadRequest, 40001, "invalid login data")
			return
		}
		username = strings.TrimSpace(request.Username)
		digest := md5.Sum([]byte(request.Password))
		passwordDigest = hex.EncodeToString(digest[:])
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40001, "multipart form required")
			return
		}
		username = strings.TrimSpace(r.FormValue("username"))
		passwordDigest = strings.ToLower(r.FormValue("password"))
	}
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
	secure := isSecureRequest(r)
	sameSite := http.SameSiteLaxMode
	if secure {
		sameSite = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiry,
		MaxAge:   int(sessionDuration.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
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
