package main

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	danAdminSessionCookie = "majdata_dan_admin"
	danAdminCSRFCookie    = "majdata_dan_admin_csrf"
	danAdminLoginCookie   = "majdata_dan_login_csrf"
	danAdminSessionTTL    = 8 * time.Hour
	maxDanCourseSongs     = 50
	maxDanNameLength      = 80
	maxDanDescription     = 500
	maxDanAdminTokenSize  = 4096
)

var danAdminLoginTemplate = template.Must(template.New("dan-admin-login").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Dan Course Admin Login</title>
  <style>
    body { font: 16px system-ui, sans-serif; margin: 2rem auto; max-width: 32rem; padding: 0 1rem; }
    label, input, button { display: block; margin-top: 1rem; }
    input { box-sizing: border-box; padding: .6rem; width: 100%; }
    button { padding: .6rem 1rem; }
    [role=alert], [role=status] { padding: .75rem; background: #f3f4f6; }
  </style>
</head>
<body>
  <main>
    <h1>Dan Course Admin</h1>
    {{if .Message}}<p role="{{.Role}}">{{.Message}}</p>{{end}}
    {{if .Enabled}}
    <form method="post" action="/admin/dan/login">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <label>Admin token
        <input type="password" name="admin_token" required autocomplete="current-password" maxlength="4096">
      </label>
      <button type="submit">Sign in</button>
    </form>
    {{end}}
  </main>
</body>
</html>`))

var danAdminPageTemplate = template.Must(template.New("dan-admin").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Dan Course Admin</title>
  <style>
    :root { color-scheme: light dark; font: 16px system-ui, sans-serif; }
    body { margin: 1.5rem auto; max-width: 76rem; padding: 0 1rem; }
    header, .toolbar, .filters, .actions { align-items: center; display: flex; flex-wrap: wrap; gap: .75rem; }
    header { justify-content: space-between; }
    input, select, textarea, button { font: inherit; padding: .5rem; }
    input[type=search], input[type=text], textarea { box-sizing: border-box; width: 100%; }
    textarea { min-height: 5rem; }
    main { display: grid; gap: 1.5rem; grid-template-columns: minmax(15rem, 1fr) minmax(30rem, 3fr); }
    section { border: 1px solid #8886; border-radius: .5rem; padding: 1rem; min-width: 0; }
    .course-list, .chart-list, #selected-songs { list-style: none; margin: .75rem 0 0; max-height: 55vh; overflow: auto; padding: 0; }
    .course-row, .chart-row { border-bottom: 1px solid #8884; padding: .65rem .25rem; }
    .course-row a { font-weight: 600; }
    .course-row small, .chart-row small { display: block; opacity: .8; }
    .chart-row[hidden] { display: none; }
    .chart-choice { margin-right: .5rem; }
    .chart-level-choice { margin: .4rem 0 0 1.8rem; }
    .filters > label { flex: 1 1 12rem; }
    .song-order-row { align-items: center; display: flex; gap: .5rem; padding: .35rem 0; }
    .song-order-row span { flex: 1; }
    .song-order-row button { padding: .25rem .5rem; }
    .notice { background: #dcefdc; color: #152515; padding: .75rem; }
    .error { background: #f9dddd; color: #351515; padding: .75rem; }
    .muted { opacity: .75; }
    @media (max-width: 52rem) { main { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <header>
    <div><h1>Dan Course Admin</h1><p class="muted">Only this admin page can change the server course list.</p></div>
    <form method="post" action="/admin/dan/logout">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <button type="submit">Sign out</button>
    </form>
  </header>
  {{if .Notice}}<p class="notice" role="status">{{.Notice}}</p>{{end}}
  {{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
  <main>
    <section>
      <div class="toolbar">
        <h2>Courses ({{len .Courses}})</h2>
        <a href="/admin/dan?new=1">Create course</a>
      </div>
      <label for="course-filter">Filter courses</label>
      <input id="course-filter" type="search" autocomplete="off" placeholder="Name or description">
      <ul class="course-list" id="course-list">
        {{range .Courses}}
        <li class="course-row" data-course-search="{{.Search}}">
          <a href="/admin/dan?course={{.ID}}">{{.Name}}</a>
          <small>{{.ChartCount}} charts{{if .HasMissingCharts}} · contains unavailable charts{{end}}{{if .HasMissingLevels}} · difficulty setup required{{end}}</small>
        </li>
        {{else}}
        <li class="muted">No courses yet.</li>
        {{end}}
      </ul>
    </section>
    <section>
      <h2>{{if .IsEditing}}Edit course{{else}}Create course{{end}}</h2>
      {{if .MissingChartIDs}}
      <p class="error">Unavailable chart IDs are not sent to the game. Remove or replace these entries before saving:</p>
      <ul>{{range .MissingChartIDs}}<li>{{.}}</li>{{end}}</ul>
      {{end}}
      {{if .NeedsDifficultySetup}}
      <p class="error">Choose a difficulty for every selected song and save the course before it is sent to the game.</p>
      {{end}}
      <form method="post" action="/admin/dan" id="dan-course-form">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <input type="hidden" name="action" value="save">
        <input type="hidden" name="course_id" value="{{.Current.ID}}">
        <input type="hidden" name="song_order" id="song-order" value="{{.SongOrder}}">
        <input type="hidden" name="song_levels" id="song-levels" value="">
        <label for="course-name">Course name</label>
        <input id="course-name" type="text" name="name" maxlength="80" required value="{{.Current.Name}}">
        <label for="course-description">Description</label>
        <textarea id="course-description" name="description" maxlength="500">{{.Current.Description}}</textarea>
        <label><input type="checkbox" name="is_force_gameover" value="1" {{if .Current.IsForceGameover}}checked{{end}}>
          End the course when life reaches zero
        </label>
        <h3>Song pool and order</h3>
        <p class="muted">Choose up to {{.MaxSongs}} songs, select a difficulty for each, and use the arrows to set the play order.</p>
        <ol id="selected-songs" aria-live="polite"></ol>
        <p id="selected-count" class="muted"></p>
        <div class="filters">
          <label>Search title, artist, designer, or chart ID
            <input id="chart-search" type="search" autocomplete="off">
          </label>
          <label>Difficulty
            <select id="difficulty-filter">
              <option value="">All difficulties</option>
              <option value="0">Easy</option>
              <option value="1">Basic</option>
              <option value="2">Advanced</option>
              <option value="3">Expert</option>
              <option value="4">Master</option>
              <option value="5">Re:MASTER</option>
              <option value="6">UTAGE</option>
            </select>
          </label>
          <label>Level contains
            <input id="level-filter" type="search" autocomplete="off" placeholder="e.g. 13+">
          </label>
        </div>
        <ul class="chart-list" id="chart-list">
          {{range .Charts}}
          <li class="chart-row" data-search="{{.Search}}" data-difficulties="{{.Difficulties}}" data-levels="{{.LevelValues}}">
            <label>
              <input class="chart-choice" type="checkbox" name="chart_id" value="{{.ID}}" {{if .Selected}}checked{{end}} {{if and (not .Selected) (or (not .Available) (not .HasLevels))}}disabled{{end}}>
              <strong>{{if .Title}}{{.Title}}{{else}}Untitled chart{{end}}</strong>
              <small>{{.Artist}}{{if .Artist}} · {{end}}ID {{.ID}}</small>
              <small>{{.Levels}}</small>
              {{if not .Available}}<small>Unavailable hash; remove this chart from the course.</small>{{end}}
              {{if not .HasLevels}}<small>No difficulty metadata; this song cannot be used in a Dan course.</small>{{end}}
            </label>
            {{if .DifficultyOptions}}
            {{$selectedLevel := .SelectedLevel}}
            <label class="chart-level-choice">Dan difficulty
              <select class="chart-level-choice" aria-label="Dan difficulty for {{.Title}}">
                {{range .DifficultyOptions}}<option value="{{.Index}}" {{if eq $selectedLevel .Index}}selected{{end}}>{{.Name}} · {{.Level}}</option>{{end}}
              </select>
            </label>
            {{end}}
          </li>
          {{end}}
        </ul>
        <div class="actions">
          <button type="submit">Save Dan course</button>
          <a href="/admin/dan?new=1">Clear form</a>
        </div>
      </form>
      {{if .IsEditing}}
      <form method="post" action="/admin/dan">
        <input type="hidden" name="csrf" value="{{.CSRF}}">
        <input type="hidden" name="action" value="delete">
        <input type="hidden" name="course_id" value="{{.Current.ID}}">
        <button type="submit">Delete course</button>
      </form>
      {{end}}
    </section>
  </main>
  <script src="/admin/dan/app.js" defer></script>
</body>
</html>`))

type danAdminSession struct {
	tokenHash []byte
	csrfHash  []byte
	csrf      string
}

type danAdminChart struct {
	ID                string
	Title             string
	Artist            string
	Levels            string
	Search            string
	Difficulties      string
	LevelValues       string
	DifficultyOptions []danAdminDifficultyOption
	SelectedLevel     int
	Available         bool
	HasLevels         bool
	Selected          bool
}

type danAdminDifficultyOption struct {
	Index int
	Name  string
	Level string
}

type danAdminCourseSummary struct {
	ID               string
	Name             string
	Search           string
	ChartCount       int
	HasMissingCharts bool
	HasMissingLevels bool
}

type danAdminPageData struct {
	CSRF                 string
	Courses              []danAdminCourseSummary
	Charts               []danAdminChart
	Current              danCourse
	SongOrder            string
	IsEditing            bool
	Error                string
	Notice               string
	MaxSongs             int
	MissingChartIDs      []string
	NeedsDifficultySetup bool
}

func (app *application) danAdminConfigured() bool {
	return len(app.danAdminToken) >= 32 && len(app.danAdminToken) <= maxDanAdminTokenSize
}

func setDanAdminHeaders(w http.ResponseWriter) {
	setSecurityHeaders(w)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "no-store")
}

func (app *application) serveDanAdminLogin(w http.ResponseWriter, r *http.Request) {
	setDanAdminHeaders(w)
	if r.URL.Path != "/admin/dan/login" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !app.danAdminConfigured() {
		app.writeDanAdminLoginPage(w, http.StatusServiceUnavailable, "", "Admin access is disabled. Configure MAJDATA_DAN_ADMIN_TOKEN with at least 32 characters.", "alert", false)
		return
	}
	if r.Method == http.MethodGet {
		csrf, err := randomToken(32)
		if err != nil {
			log.Printf("dan_admin_csrf_error err=%v", err)
			http.Error(w, "admin login is temporarily unavailable", http.StatusInternalServerError)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     danAdminLoginCookie,
			Value:    csrf,
			Path:     "/admin/dan/login",
			MaxAge:   600,
			HttpOnly: true,
			Secure:   isSecureRequest(r),
			SameSite: http.SameSiteStrictMode,
		})
		app.writeDanAdminLoginPage(w, http.StatusOK, csrf, "", "", true)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid login form", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(danAdminLoginCookie)
	if err != nil || !validCSRF(cookie.Value, r.FormValue("csrf")) {
		app.writeDanAdminLoginPage(w, http.StatusForbidden, "", "The form expired. Reload the page and try again.", "alert", true)
		return
	}
	token := r.FormValue("admin_token")
	if len(token) != len(app.danAdminToken) ||
		subtle.ConstantTimeCompare([]byte(token), []byte(app.danAdminToken)) != 1 {
		app.writeDanAdminLoginPage(w, http.StatusUnauthorized, cookie.Value, "Invalid admin token.", "alert", true)
		return
	}

	sessionToken, err := randomToken(32)
	if err != nil {
		log.Printf("dan_admin_session_error err=%v", err)
		http.Error(w, "admin login is temporarily unavailable", http.StatusInternalServerError)
		return
	}
	sessionCSRF, err := randomToken(32)
	if err != nil {
		log.Printf("dan_admin_session_error err=%v", err)
		http.Error(w, "admin login is temporarily unavailable", http.StatusInternalServerError)
		return
	}
	tokenHash := sha256.Sum256([]byte(sessionToken))
	csrfHash := sha256.Sum256([]byte(sessionCSRF))
	expiresAt := time.Now().UTC().Add(danAdminSessionTTL)
	if _, err := app.store.ExecContext(r.Context(),
		"DELETE FROM dan_admin_sessions WHERE expires_at <= ?", time.Now().Unix()); err != nil {
		log.Printf("dan_admin_session_cleanup_error err=%v", err)
	}
	if _, err := app.store.ExecContext(r.Context(),
		"INSERT INTO dan_admin_sessions (token_hash, csrf_hash, expires_at) VALUES (?, ?, ?)",
		tokenHash[:], csrfHash[:], expiresAt.Unix()); err != nil {
		log.Printf("dan_admin_session_error err=%v", err)
		http.Error(w, "admin login is temporarily unavailable", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     danAdminSessionCookie,
		Value:    sessionToken,
		Path:     "/admin/dan",
		Expires:  expiresAt,
		MaxAge:   int(danAdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     danAdminCSRFCookie,
		Value:    sessionCSRF,
		Path:     "/admin/dan",
		Expires:  expiresAt,
		MaxAge:   int(danAdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/admin/dan", http.StatusSeeOther)
}

func (app *application) writeDanAdminLoginPage(w http.ResponseWriter, status int, csrf, message, role string, enabled bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := danAdminLoginTemplate.Execute(w, struct {
		CSRF    string
		Message string
		Role    string
		Enabled bool
	}{CSRF: csrf, Message: message, Role: role, Enabled: enabled}); err != nil {
		log.Printf("dan_admin_login_page_error err=%v", err)
	}
}

func (app *application) requireDanAdmin(next func(http.ResponseWriter, *http.Request, danAdminSession)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setDanAdminHeaders(w)
		if !app.danAdminConfigured() {
			http.Error(w, "Dan course administration is disabled", http.StatusServiceUnavailable)
			return
		}
		cookie, err := r.Cookie(danAdminSessionCookie)
		if err != nil || cookie.Value == "" {
			http.Redirect(w, r, "/admin/dan/login", http.StatusSeeOther)
			return
		}
		csrfCookie, err := r.Cookie(danAdminCSRFCookie)
		if err != nil || csrfCookie.Value == "" {
			http.Redirect(w, r, "/admin/dan/login", http.StatusSeeOther)
			return
		}
		tokenHash := sha256.Sum256([]byte(cookie.Value))
		var csrfHash []byte
		err = app.store.QueryRowContext(r.Context(),
			"SELECT csrf_hash FROM dan_admin_sessions WHERE token_hash = ? AND expires_at > ?",
			tokenHash[:], time.Now().Unix()).Scan(&csrfHash)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				log.Printf("dan_admin_session_lookup_error err=%v", err)
				http.Error(w, "admin session could not be verified", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/admin/dan/login", http.StatusSeeOther)
			return
		}
		actualCSRFHash := sha256.Sum256([]byte(csrfCookie.Value))
		if subtle.ConstantTimeCompare(actualCSRFHash[:], csrfHash) != 1 {
			http.Redirect(w, r, "/admin/dan/login", http.StatusSeeOther)
			return
		}
		next(w, r, danAdminSession{
			tokenHash: tokenHash[:],
			csrfHash:  csrfHash,
			csrf:      csrfCookie.Value,
		})
	}
}

func (app *application) serveDanAdminLogout(w http.ResponseWriter, r *http.Request, session danAdminSession) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid logout form", http.StatusBadRequest)
		return
	}
	if !validDanAdminCSRF(r, session) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	if _, err := app.store.ExecContext(r.Context(), "DELETE FROM dan_admin_sessions WHERE token_hash = ?", session.tokenHash); err != nil {
		log.Printf("dan_admin_logout_error err=%v", err)
		http.Error(w, "admin logout failed", http.StatusInternalServerError)
		return
	}
	expireDanAdminCookies(w, r)
	http.Redirect(w, r, "/admin/dan/login", http.StatusSeeOther)
}

func expireDanAdminCookies(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{danAdminSessionCookie, danAdminCSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/admin/dan",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   isSecureRequest(r),
			SameSite: http.SameSiteStrictMode,
		})
	}
}

func validDanAdminCSRF(r *http.Request, session danAdminSession) bool {
	cookie, err := r.Cookie(danAdminCSRFCookie)
	if err != nil || !validCSRF(cookie.Value, r.FormValue("csrf")) {
		return false
	}
	actual := sha256.Sum256([]byte(cookie.Value))
	return subtle.ConstantTimeCompare(actual[:], session.csrfHash) == 1
}

func (app *application) serveDanAdmin(w http.ResponseWriter, r *http.Request, session danAdminSession) {
	if r.URL.Path != "/admin/dan" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		if r.URL.Query().Get("new") == "1" {
			app.renderDanAdminPage(w, r, session, danCourse{}, false, "", "")
			return
		}
		courseID := strings.TrimSpace(r.URL.Query().Get("course"))
		if courseID == "" {
			app.renderDanAdminPage(w, r, session, danCourse{}, false, "", "")
			return
		}
		course, err := app.getDanCourse(r.Context(), courseID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("dan_admin_course_load_error err=%v", err)
			http.Error(w, "Dan course could not be loaded", http.StatusInternalServerError)
			return
		}
		app.renderDanAdminPage(w, r, session, course, true, "", "")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid Dan course form", http.StatusBadRequest)
		return
	}
	if !validDanAdminCSRF(r, session) {
		http.Error(w, "invalid form token", http.StatusForbidden)
		return
	}
	switch r.FormValue("action") {
	case "save":
		app.saveDanAdminCourse(w, r, session)
	case "delete":
		app.deleteDanAdminCourse(w, r)
	default:
		http.Error(w, "invalid action", http.StatusBadRequest)
	}
}

func (app *application) saveDanAdminCourse(w http.ResponseWriter, r *http.Request, session danAdminSession) {
	course, err := parseDanCourseForm(r, app.catalog)
	if err != nil {
		app.renderDanAdminPage(w, r, session, course, course.ID != "", err.Error(), "")
		return
	}
	if course.ID == "" {
		course.ID, err = newDanCourseID()
		if err != nil {
			log.Printf("dan_admin_course_id_error err=%v", err)
			http.Error(w, "Dan course could not be saved", http.StatusInternalServerError)
			return
		}
		if err := app.createDanCourse(r.Context(), course); err != nil {
			log.Printf("dan_admin_course_create_error err=%v", err)
			http.Error(w, "Dan course could not be saved", http.StatusInternalServerError)
			return
		}
	} else {
		if _, err := app.getDanCourse(r.Context(), course.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("dan_admin_course_load_error err=%v", err)
			http.Error(w, "Dan course could not be saved", http.StatusInternalServerError)
			return
		}
		if err := app.updateDanCourse(r.Context(), course); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				http.NotFound(w, r)
				return
			}
			log.Printf("dan_admin_course_update_error err=%v", err)
			http.Error(w, "Dan course could not be saved", http.StatusInternalServerError)
			return
		}
	}
	http.Redirect(w, r, "/admin/dan?course="+url.QueryEscape(course.ID)+"&saved=1", http.StatusSeeOther)
}

func (app *application) deleteDanAdminCourse(w http.ResponseWriter, r *http.Request) {
	courseID := strings.TrimSpace(r.FormValue("course_id"))
	if courseID == "" {
		http.Error(w, "invalid course ID", http.StatusBadRequest)
		return
	}
	if err := app.deleteDanCourse(r.Context(), courseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("dan_admin_course_delete_error err=%v", err)
		http.Error(w, "Dan course could not be deleted", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/dan?deleted=1", http.StatusSeeOther)
}

func parseDanCourseForm(r *http.Request, catalog map[string]chartCatalogEntry) (danCourse, error) {
	course := danCourse{
		ID:              strings.TrimSpace(r.FormValue("course_id")),
		Name:            strings.TrimSpace(r.FormValue("name")),
		Description:     strings.TrimSpace(r.FormValue("description")),
		IsForceGameover: r.FormValue("is_force_gameover") == "1",
	}
	selected := r.Form["chart_id"]
	course.ChartIDs = make([]string, 0, len(selected))
	for _, chartID := range selected {
		course.ChartIDs = append(course.ChartIDs, strings.ToLower(strings.TrimSpace(chartID)))
	}
	if len(selected) == 0 || len(selected) > maxDanCourseSongs {
		return course, fmt.Errorf("Choose between 1 and %d charts.", maxDanCourseSongs)
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, chartID := range selected {
		chartID = strings.ToLower(strings.TrimSpace(chartID))
		entry, ok := catalog[chartID]
		if !ok || entry.chart.Hash == "" {
			return course, fmt.Errorf("A selected chart is no longer available. Reload the page and choose available charts.")
		}
		if _, exists := selectedSet[chartID]; exists {
			return course, fmt.Errorf("The same chart cannot be added more than once.")
		}
		selectedSet[chartID] = struct{}{}
	}

	order := course.ChartIDs
	if rawOrder := strings.TrimSpace(r.FormValue("song_order")); rawOrder != "" {
		order = strings.Split(rawOrder, ",")
	}
	ordered := make([]string, 0, len(order))
	orderedSet := make(map[string]struct{}, len(order))
	for _, chartID := range order {
		chartID = strings.ToLower(strings.TrimSpace(chartID))
		if _, ok := selectedSet[chartID]; !ok {
			return course, fmt.Errorf("Song order does not match the selected charts. Reload the page and try again.")
		}
		if _, exists := orderedSet[chartID]; exists {
			return course, fmt.Errorf("Song order contains a duplicate chart.")
		}
		orderedSet[chartID] = struct{}{}
		ordered = append(ordered, chartID)
	}
	if len(ordered) != len(selectedSet) {
		return course, fmt.Errorf("Song order does not match the selected charts. Reload the page and try again.")
	}
	course.ChartIDs = ordered
	rawLevels := strings.TrimSpace(r.FormValue("song_levels"))
	if rawLevels == "" {
		return course, fmt.Errorf("Select a valid difficulty for every selected song.")
	}
	if err := json.Unmarshal([]byte(rawLevels), &course.SongLevels); err != nil {
		return course, fmt.Errorf("Difficulty selections are invalid. Reload the page and try again.")
	}
	if len(course.SongLevels) != len(course.ChartIDs) {
		return course, fmt.Errorf("Select a valid difficulty for every selected song.")
	}
	for i, chartID := range course.ChartIDs {
		entry := catalog[chartID]
		if !danChartHasDifficulty(entry.chart, course.SongLevels[i]) {
			return course, fmt.Errorf("Choose an available difficulty for every selected song.")
		}
	}
	if !utf8.ValidString(course.Name) || course.Name == "" || utf8.RuneCountInString(course.Name) > maxDanNameLength ||
		strings.ContainsRune(course.Name, '\x00') {
		return course, fmt.Errorf("Enter a course name between 1 and %d characters.", maxDanNameLength)
	}
	if !utf8.ValidString(course.Description) || utf8.RuneCountInString(course.Description) > maxDanDescription ||
		strings.ContainsRune(course.Description, '\x00') {
		return course, fmt.Errorf("Description must be at most %d characters.", maxDanDescription)
	}
	return course, nil
}

func newDanCourseID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate dan course ID: %w", err)
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", id[0:4], id[4:6], id[6:8], id[8:10], id[10:16]), nil
}

func (app *application) renderDanAdminPage(w http.ResponseWriter, r *http.Request, session danAdminSession, current danCourse, isEditing bool, errorMessage, notice string) {
	courses, err := app.listDanCourses(r.Context())
	if err != nil {
		log.Printf("dan_admin_course_list_error err=%v", err)
		http.Error(w, "Dan courses could not be loaded", http.StatusInternalServerError)
		return
	}
	summaries := make([]danAdminCourseSummary, 0, len(courses))
	missing := make([]string, 0)
	for _, course := range courses {
		validCount := 0
		for _, chartID := range course.ChartIDs {
			if entry, ok := app.catalog[strings.ToLower(chartID)]; ok && entry.chart.Hash != "" {
				validCount++
			}
		}
		search := strings.ToLower(course.Name + " " + course.Description)
		summaries = append(summaries, danAdminCourseSummary{
			ID:               course.ID,
			Name:             course.Name,
			Search:           search,
			ChartCount:       len(course.ChartIDs),
			HasMissingCharts: validCount != len(course.ChartIDs),
			HasMissingLevels: !danCourseHasValidLevels(course, app.catalog),
		})
	}
	if current.ChartIDs != nil {
		for _, chartID := range current.ChartIDs {
			if entry, ok := app.catalog[strings.ToLower(chartID)]; !ok || entry.chart.Hash == "" {
				missing = append(missing, chartID)
			}
		}
	}
	charts := buildDanAdminCharts(app.catalog, current.ChartIDs, current.SongLevels)
	data := danAdminPageData{
		CSRF:                 session.csrf,
		Courses:              summaries,
		Charts:               charts,
		Current:              current,
		SongOrder:            strings.Join(current.ChartIDs, ","),
		IsEditing:            isEditing,
		Error:                errorMessage,
		Notice:               notice,
		MaxSongs:             maxDanCourseSongs,
		MissingChartIDs:      missing,
		NeedsDifficultySetup: isEditing && !danCourseHasValidLevels(current, app.catalog),
	}
	if r.URL.Query().Get("saved") == "1" {
		data.Notice = "Course saved. It is now available to signed-in players in MajdataPlay."
	}
	if r.URL.Query().Get("deleted") == "1" {
		data.Notice = "Course deleted."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := danAdminPageTemplate.Execute(w, data); err != nil {
		log.Printf("dan_admin_page_error err=%v", err)
	}
}

func buildDanAdminCharts(catalog map[string]chartCatalogEntry, selectedIDs []string, selectedLevels []int) []danAdminChart {
	selected := make(map[string]struct{}, len(selectedIDs))
	savedLevels := make(map[string]int, len(selectedIDs))
	for i, id := range selectedIDs {
		selected[strings.ToLower(id)] = struct{}{}
		if i < len(selectedLevels) {
			savedLevels[strings.ToLower(id)] = selectedLevels[i]
		}
	}
	charts := make([]danAdminChart, 0, len(catalog))
	for id, entry := range catalog {
		levelValues := make([]string, 0, len(entry.chart.Levels))
		difficulties := make([]string, 0, len(entry.chart.Levels))
		levelText := make([]string, 0, len(entry.chart.Levels))
		options := make([]danAdminDifficultyOption, 0, len(entry.chart.Levels))
		for levelIndex, level := range entry.chart.Levels {
			if level == nil || strings.TrimSpace(*level) == "" {
				continue
			}
			levelValue := strings.TrimSpace(*level)
			levelValues = append(levelValues, strings.ToLower(levelValue))
			difficulties = append(difficulties, fmt.Sprint(levelIndex))
			levelText = append(levelText, fmt.Sprintf("%s %s", danDifficultyName(levelIndex), levelValue))
			options = append(options, danAdminDifficultyOption{
				Index: levelIndex,
				Name:  danDifficultyName(levelIndex),
				Level: levelValue,
			})
		}
		search := strings.ToLower(strings.Join([]string{entry.chart.Title, entry.chart.Artist, entry.chart.Designer, id}, " "))
		_, isSelected := selected[strings.ToLower(id)]
		selectedLevel := -1
		if len(options) > 0 {
			selectedLevel = options[0].Index
			if savedLevel, ok := savedLevels[strings.ToLower(id)]; ok {
				for _, option := range options {
					if option.Index == savedLevel {
						selectedLevel = savedLevel
						break
					}
				}
			}
		}
		charts = append(charts, danAdminChart{
			ID:                entry.chart.ID,
			Title:             entry.chart.Title,
			Artist:            entry.chart.Artist,
			Levels:            strings.Join(levelText, " · "),
			Search:            search,
			Difficulties:      strings.Join(difficulties, " "),
			LevelValues:       strings.Join(levelValues, " "),
			DifficultyOptions: options,
			SelectedLevel:     selectedLevel,
			Available:         entry.chart.Hash != "",
			HasLevels:         len(options) > 0,
			Selected:          isSelected,
		})
	}
	sort.Slice(charts, func(i, j int) bool {
		left, right := strings.ToLower(charts[i].Title), strings.ToLower(charts[j].Title)
		if left != right {
			return left < right
		}
		return charts[i].ID < charts[j].ID
	})
	return charts
}

func danDifficultyName(level int) string {
	names := [...]string{"Easy", "Basic", "Advanced", "Expert", "Master", "Re:MASTER", "UTAGE"}
	if level < 0 || level >= len(names) {
		return "Unknown"
	}
	return names[level]
}

func serveDanAdminScript(w http.ResponseWriter, r *http.Request) {
	setDanAdminHeaders(w)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	if _, err := w.Write([]byte(danAdminScript)); err != nil {
		log.Printf("dan_admin_script_write_error err=%v", err)
	}
}

const danAdminScript = `(() => {
  const choices = Array.from(document.querySelectorAll(".chart-choice"));
  const selectedList = document.getElementById("selected-songs");
  const orderInput = document.getElementById("song-order");
  const songLevelsInput = document.getElementById("song-levels");
  const count = document.getElementById("selected-count");
  const maxSongs = 50;
  const selectedOrder = (orderInput.value || "").split(",").filter(Boolean);

  function selectedSet() {
    return new Set(choices.filter((choice) => choice.checked).map((choice) => choice.value));
  }

  function selectedLevel(id) {
    const choice = choices.find((item) => item.value === id);
    const select = choice && choice.closest(".chart-row").querySelector("select.chart-level-choice");
    return select ? Number(select.value) : -1;
  }

  function renderSelected() {
    const checked = selectedSet();
    for (let i = selectedOrder.length - 1; i >= 0; i--) {
      if (!checked.has(selectedOrder[i])) selectedOrder.splice(i, 1);
    }
    choices.forEach((choice) => {
      if (choice.checked && !selectedOrder.includes(choice.value)) selectedOrder.push(choice.value);
    });
    selectedList.replaceChildren();
    selectedOrder.forEach((id, index) => {
      const choice = choices.find((item) => item.value === id);
      if (!choice) return;
      const row = choice.closest(".chart-row");
      const item = document.createElement("li");
      item.className = "song-order-row";
      const title = document.createElement("span");
      const levelSelect = row.querySelector("select.chart-level-choice");
      const levelName = levelSelect ? levelSelect.options[levelSelect.selectedIndex].textContent : "No difficulty";
      title.textContent = row.querySelector("strong").textContent + " — " + levelName;
      item.appendChild(title);
      [["up", "↑"], ["down", "↓"], ["remove", "Remove"]].forEach(([action, label]) => {
        const button = document.createElement("button");
        button.type = "button";
        button.dataset.action = action;
        button.dataset.id = id;
        button.textContent = label;
        button.disabled = (action === "up" && index === 0) || (action === "down" && index === selectedOrder.length - 1);
        item.appendChild(button);
      });
      selectedList.appendChild(item);
    });
    orderInput.value = selectedOrder.join(",");
    songLevelsInput.value = JSON.stringify(selectedOrder.map(selectedLevel));
    count.textContent = selectedOrder.length + " / " + maxSongs + " selected";
  }

  choices.forEach((choice) => choice.addEventListener("change", () => {
    if (choice.checked && selectedSet().size > maxSongs) {
      choice.checked = false;
      window.alert("A course can contain at most " + maxSongs + " charts.");
    }
    renderSelected();
  }));
  document.querySelectorAll("select.chart-level-choice").forEach((select) => {
    select.addEventListener("change", renderSelected);
  });
  selectedList.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-action]");
    if (!button) return;
    const index = selectedOrder.indexOf(button.dataset.id);
    if (button.dataset.action === "remove") {
      const choice = choices.find((item) => item.value === button.dataset.id);
      if (choice) choice.checked = false;
    } else if (button.dataset.action === "up" && index > 0) {
      [selectedOrder[index - 1], selectedOrder[index]] = [selectedOrder[index], selectedOrder[index - 1]];
    } else if (button.dataset.action === "down" && index >= 0 && index < selectedOrder.length - 1) {
      [selectedOrder[index + 1], selectedOrder[index]] = [selectedOrder[index], selectedOrder[index + 1]];
    }
    renderSelected();
  });

  function filterCharts() {
    const search = document.getElementById("chart-search").value.trim().toLowerCase();
    const difficulty = document.getElementById("difficulty-filter").value;
    const level = document.getElementById("level-filter").value.trim().toLowerCase();
    document.querySelectorAll(".chart-row").forEach((row) => {
      const matches = (!search || row.dataset.search.includes(search))
        && (!difficulty || row.dataset.difficulties.split(" ").includes(difficulty))
        && (!level || row.dataset.levels.includes(level));
      row.hidden = !matches;
    });
  }
  ["chart-search", "difficulty-filter", "level-filter"].forEach((id) => {
    document.getElementById(id).addEventListener("input", filterCharts);
    document.getElementById(id).addEventListener("change", filterCharts);
  });
  const courseFilter = document.getElementById("course-filter");
  if (courseFilter) courseFilter.addEventListener("input", () => {
    const query = courseFilter.value.trim().toLowerCase();
    document.querySelectorAll(".course-row").forEach((row) => {
      row.hidden = query !== "" && !row.dataset.courseSearch.includes(query);
    });
  });
  renderSelected();
  filterCharts();
})();`
