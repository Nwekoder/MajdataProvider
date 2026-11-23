package main

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const maxGameSettingSize = 1 << 20

type userProfile struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	Nickname     string    `json:"nickname"`
	Introduction *string   `json:"introduction"`
	Email        *string   `json:"email"`
	AvatarID     string    `json:"avatarId"`
	JoinDate     time.Time `json:"joinDate"`
}

func randomObjectID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (app *application) serveRegister(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	if app.inviteCode == "" {
		writeAPIStatus(w, http.StatusForbidden, 60002, "registration is not enabled")
		return
	}

	var request struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		InviteCode string `json:"inviteCode"`
	}
	if err := decodeJSONBody(w, r, 16<<10, &request); err != nil {
		writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid registration data")
		return
	}
	if len(request.InviteCode) != len(app.inviteCode) ||
		subtle.ConstantTimeCompare([]byte(request.InviteCode), []byte(app.inviteCode)) != 1 {
		writeAPIStatus(w, http.StatusForbidden, 60002, "invalid invite code")
		return
	}
	request.Username = strings.TrimSpace(request.Username)
	if !usernamePattern.MatchString(request.Username) || !validRegistrationPassword(request.Password) {
		writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid username or password")
		return
	}

	user, err := app.createAccount(r, request.Username, request.Password)
	if err != nil {
		if errors.Is(err, errUsernameExists) {
			writeAPIStatus(w, http.StatusConflict, 60103, "username already exists")
			return
		}
		log.Printf("registration_error err=%v", err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "account registration failed")
		return
	}
	writeJSON(w, http.StatusOK, userProfile{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Username,
		AvatarID: "",
		JoinDate: user.CreatedAt,
	})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, maxBytes int64, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func (app *application) serveProfile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		app.requireAccount(app.updateProfile)(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	if username := strings.TrimSpace(r.URL.Query().Get("username")); username != "" {
		profile, err := app.getUserProfile(r, "username", username)
		if errors.Is(err, sql.ErrNoRows) {
			writeAPIStatus(w, http.StatusNotFound, 40400, "user not found")
			return
		}
		if err != nil {
			log.Printf("profile_lookup_error username=%q err=%v", username, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "profile could not be loaded")
			return
		}
		writeJSON(w, http.StatusOK, profile)
		return
	}
	app.requireAccount(app.getOwnProfile)(w, r)
}

func (app *application) getOwnProfile(w http.ResponseWriter, r *http.Request, user account) {
	profile, err := app.getUserProfile(r, "id", user.ID)
	if err != nil {
		log.Printf("profile_lookup_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "profile could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (app *application) getUserProfile(r *http.Request, column, value string) (userProfile, error) {
	if column != "id" && column != "username" {
		return userProfile{}, fmt.Errorf("invalid profile lookup column")
	}
	var profile userProfile
	var introduction sql.NullString
	var createdAt string
	query := fmt.Sprintf("SELECT id, username, nickname, introduction, created_at FROM users WHERE %s = ?", column)
	err := app.store.QueryRowContext(r.Context(), query, value).Scan(
		&profile.ID, &profile.Username, &profile.Nickname, &introduction, &createdAt,
	)
	if err != nil {
		return userProfile{}, err
	}
	if profile.Nickname == "" {
		profile.Nickname = profile.Username
	}
	if introduction.Valid {
		profile.Introduction = &introduction.String
	}
	profile.AvatarID = ""
	profile.JoinDate, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return userProfile{}, fmt.Errorf("parse profile creation time: %w", err)
	}
	return profile, nil
}

func (app *application) updateProfile(w http.ResponseWriter, r *http.Request, user account) {
	var request map[string]json.RawMessage
	if err := decodeJSONBody(w, r, 1<<20, &request); err != nil {
		writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid profile data")
		return
	}

	var nickname *string
	if value, ok := request["nickname"]; ok {
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil || strings.TrimSpace(parsed) == "" {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "nickname cannot be empty")
			return
		}
		parsed = strings.TrimSpace(parsed)
		if !utf8.ValidString(parsed) || utf8.RuneCountInString(parsed) > 80 {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid nickname")
			return
		}
		nickname = &parsed
	}

	var introduction *string
	if value, ok := request["introduction"]; ok && string(value) != "null" {
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil || !utf8.ValidString(parsed) ||
			utf8.RuneCountInString(parsed) > 1000 {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid introduction")
			return
		}
		introduction = &parsed
	}
	if nickname == nil && introduction == nil {
		writeAPIStatus(w, http.StatusBadRequest, 40001, "no profile fields supplied")
		return
	}

	result, err := app.store.ExecContext(r.Context(), `
UPDATE users
SET nickname = COALESCE(?, nickname), introduction = COALESCE(?, introduction)
WHERE id = ?`, nickname, introduction, user.ID)
	if err != nil {
		log.Printf("profile_update_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "profile could not be updated")
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		log.Printf("profile_update_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "profile could not be updated")
		return
	}
	if affected != 1 {
		writeAPIStatus(w, http.StatusNotFound, 40400, "user not found")
		return
	}
	profile, err := app.getUserProfile(r, "id", user.ID)
	if err != nil {
		log.Printf("profile_lookup_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "profile could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (app *application) serveGameSetting(w http.ResponseWriter, r *http.Request, user account) {
	switch r.Method {
	case http.MethodGet:
		var setting string
		err := app.store.QueryRowContext(r.Context(),
			"SELECT settings_json FROM game_settings WHERE user_id = ?", user.ID).Scan(&setting)
		if errors.Is(err, sql.ErrNoRows) {
			setting = "{}"
		} else if err != nil {
			log.Printf("game_setting_read_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "game setting could not be loaded")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, setting); err != nil {
			log.Printf("game_setting_write_error user_id=%s err=%v", user.ID, err)
		}
	case http.MethodPost:
		r.Body = http.MaxBytesReader(w, r.Body, maxGameSettingSize)
		data, err := io.ReadAll(r.Body)
		if err != nil {
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				writeAPIStatus(w, http.StatusRequestEntityTooLarge, 41300, "game setting is too large")
				return
			}
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid game setting")
			return
		}
		if !json.Valid(data) {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid game setting")
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = app.store.ExecContext(r.Context(), `
INSERT INTO game_settings (user_id, settings_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT(user_id) DO UPDATE SET settings_json = excluded.settings_json, updated_at = excluded.updated_at`,
			user.ID, string(data), now)
		if err != nil {
			log.Printf("game_setting_write_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "game setting could not be saved")
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Username        string `json:"username"`
			UserSettingJSON string `json:"userSettingJson"`
		}{
			Username:        user.Username,
			UserSettingJSON: base64.StdEncoding.EncodeToString(data),
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
	}
}
