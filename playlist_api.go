package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func (app *application) serveCollectionList(collections []publicCollection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
			return
		}
		page, pageSize, ok := parseCollectionPagination(r.URL.Query())
		if !ok {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid pagination")
			return
		}
		var user account
		if session, valid, err := app.sessionAccount(r); err != nil {
			log.Printf("collection_session_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
			return
		} else if valid {
			user = session
		}

		keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
		var matcher *regexp.Regexp
		if keyword != "" {
			matcher, _ = regexp.Compile("(?i)" + keyword)
			if matcher == nil {
				writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid keyword")
				return
			}
		}
		createdBy := strings.TrimSpace(r.URL.Query().Get("createdBy"))
		items := make([]publicCollectionListItem, 0, len(collections))
		appendItem := func(item publicCollectionListItem) {
			if createdBy != "" && !strings.EqualFold(item.CreatedBy, createdBy) {
				return
			}
			if matcher != nil && !matcher.MatchString(item.Name+" "+item.Description) {
				return
			}
			items = append(items, item)
		}
		for _, collection := range collections {
			appendItem(publicCollectionListItem{
				ID: collection.ID, Name: collection.Name, CreatedBy: collection.CreatedBy,
				Description: collection.Description, Count: collection.Count,
			})
		}

		rows, err := app.store.QueryContext(r.Context(), `
SELECT playlists.id, playlists.name, users.username, playlists.description, playlists.visibility,
       COUNT(playlist_items.chart_id)
FROM playlists
JOIN users ON users.id = playlists.owner_id
LEFT JOIN playlist_items ON playlist_items.playlist_id = playlists.id
WHERE playlists.visibility = 1 OR playlists.owner_id = ?
GROUP BY playlists.id
ORDER BY playlists.updated_at DESC, playlists.id ASC`, user.ID)
		if err != nil {
			log.Printf("playlist_list_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
			return
		}
		for rows.Next() {
			var item publicCollectionListItem
			var visibility int
			if err := rows.Scan(&item.ID, &item.Name, &item.CreatedBy, &item.Description, &visibility, &item.Count); err != nil {
				rows.Close()
				log.Printf("playlist_list_scan_error err=%v", err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
				return
			}
			appendItem(item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			log.Printf("playlist_list_iter_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
			return
		}
		if err := rows.Close(); err != nil {
			log.Printf("playlist_list_close_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
			return
		}
		start := page * pageSize
		if start >= len(items) {
			writeJSON(w, http.StatusOK, []publicCollectionListItem{})
			return
		}
		end := min(start+pageSize, len(items))
		writeJSON(w, http.StatusOK, items[start:end])
	}
}

func indexCollections(collections []publicCollection) map[string]publicCollection {
	index := make(map[string]publicCollection, len(collections))
	for _, collection := range collections {
		index[strings.ToLower(collection.ID)] = collection
	}
	return index
}

func parseCollectionPagination(query url.Values) (int, int, bool) {
	page, pageSize := 0, 30
	if value := query.Get("page"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, 0, false
		}
		page = parsed
	}
	if value := query.Get("pageSize"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maxCollectionPageSize {
			return 0, 0, false
		}
		pageSize = parsed
	}
	if page > int(^uint(0)>>1)/pageSize {
		return 0, 0, false
	}
	return page, pageSize, true
}

func (app *application) sessionAccount(r *http.Request) (account, bool, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return account{}, false, nil
	}
	tokenHash := sha256Bytes(cookie.Value)
	var user account
	var createdAt string
	err = app.store.QueryRowContext(r.Context(), `
SELECT users.id, users.username, users.created_at
FROM sessions JOIN users ON sessions.user_id = users.id
WHERE sessions.token_hash = ? AND sessions.expires_at > ?`, tokenHash, time.Now().Unix()).
		Scan(&user.ID, &user.Username, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, false, nil
	}
	if err != nil {
		return account{}, false, err
	}
	user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return account{}, false, err
	}
	return user, true, nil
}

func sha256Bytes(value string) []byte {
	sum := sha256.Sum256([]byte(value))
	return sum[:]
}

func (app *application) serveCollection(collections []publicCollection) http.Handler {
	static := indexCollections(collections)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) == 3 && parts[0] == "api" && parts[1] == "collection" && parts[2] == "create" {
			app.createPlaylist(w, r)
			return
		}
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "collection" {
			http.NotFound(w, r)
			return
		}

		id := strings.ToLower(parts[2])
		switch parts[3] {
		case "songlist", "hashlist":
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", http.MethodGet)
				writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
				return
			}
			collection, visibility, ownerID, err := app.getCollection(r.Context(), id, static)
			viewer, signedIn, sessionErr := app.sessionAccount(r)
			if sessionErr != nil {
				log.Printf("collection_session_error err=%v", sessionErr)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be loaded")
				return
			}
			if errors.Is(err, sql.ErrNoRows) || (visibility == 0 && (!signedIn || viewer.ID != ownerID)) {
				writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
				return
			}
			if err != nil {
				log.Printf("playlist_get_error collection_id=%s err=%v", id, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be loaded")
				return
			}
			if parts[3] == "songlist" {
				writeJSON(w, http.StatusOK, publicCollectionSongList{
					ID: collection.ID, Name: collection.Name, CreatedBy: collection.CreatedBy,
					Description: collection.Description, Count: collection.Count,
					Visibility: visibility, Items: collection.Items,
				})
				return
			}
			hashes := make([]string, 0, len(collection.Items))
			for _, chartID := range collection.Items {
				if entry, exists := app.catalog[strings.ToLower(chartID)]; exists {
					hashes = append(hashes, entry.chart.Hash)
				}
			}
			writeJSON(w, http.StatusOK, publicCollectionHashList{
				ID: collection.ID, Name: collection.Name, CreatedBy: collection.CreatedBy,
				Description: collection.Description, SongHashs: hashes, IsPlayList: true,
			})
		case "modify":
			if r.Method != http.MethodPut {
				w.Header().Set("Allow", http.MethodPut)
				writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
				return
			}
			app.requireAccount(func(w http.ResponseWriter, r *http.Request, user account) {
				if _, exists := static[id]; exists {
					writeAPIStatus(w, http.StatusForbidden, 60002, "permission denied")
					return
				}
				app.modifyPlaylist(w, r, user, id)
			})(w, r)
		case "destroy":
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", http.MethodPost)
				writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
				return
			}
			app.requireAccount(func(w http.ResponseWriter, r *http.Request, user account) {
				app.destroyPlaylist(w, r, user, id, static)
			})(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (app *application) getCollection(ctx context.Context, id string, static map[string]publicCollection) (publicCollectionSongList, int, string, error) {
	if collection, ok := static[id]; ok {
		return publicCollectionSongList{
			ID: collection.ID, Name: collection.Name, CreatedBy: collection.CreatedBy,
			Description: collection.Description, Count: collection.Count, Visibility: 1, Items: collection.Items,
		}, 1, "", nil
	}
	var result publicCollectionSongList
	var ownerID string
	err := app.store.QueryRowContext(ctx, `
SELECT playlists.id, playlists.name, users.username, playlists.description, playlists.visibility, playlists.owner_id
FROM playlists JOIN users ON users.id = playlists.owner_id
WHERE playlists.id = ?`, id).Scan(
		&result.ID, &result.Name, &result.CreatedBy, &result.Description, &result.Visibility, &ownerID,
	)
	if err != nil {
		return publicCollectionSongList{}, 0, "", err
	}
	result.Items, err = app.playlistChartIDs(ctx, id)
	if err != nil {
		return publicCollectionSongList{}, 0, "", err
	}
	filtered := result.Items[:0]
	for _, chartID := range result.Items {
		if _, exists := app.catalog[strings.ToLower(chartID)]; exists {
			filtered = append(filtered, chartID)
		}
	}
	result.Items = filtered
	result.Count = len(filtered)
	return result, result.Visibility, ownerID, nil
}

func (app *application) playlistChartIDs(ctx context.Context, id string) ([]string, error) {
	rows, err := app.store.QueryContext(ctx,
		"SELECT chart_id FROM playlist_items WHERE playlist_id = ? ORDER BY position ASC", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]string, 0)
	for rows.Next() {
		var chartID string
		if err := rows.Scan(&chartID); err != nil {
			return nil, err
		}
		items = append(items, chartID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (app *application) createPlaylist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	app.requireAccount(func(w http.ResponseWriter, r *http.Request, user account) {
		var request struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Visibility  *int   `json:"visibility"`
		}
		if err := decodeJSONBody(w, r, 1<<20, &request); err != nil ||
			strings.TrimSpace(request.Name) == "" || request.Visibility == nil ||
			(*request.Visibility != 0 && *request.Visibility != 1) {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection data")
			return
		}
		id, err := randomObjectID()
		if err != nil {
			log.Printf("playlist_id_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be created")
			return
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		_, err = app.store.ExecContext(r.Context(), `
INSERT INTO playlists (id, owner_id, name, description, visibility, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, user.ID, strings.TrimSpace(request.Name), request.Description, *request.Visibility, now, now)
		if err != nil {
			log.Printf("playlist_create_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be created")
			return
		}
		writeJSON(w, http.StatusOK, publicCollectionSongList{
			ID: id, Name: strings.TrimSpace(request.Name), CreatedBy: user.Username,
			Description: request.Description, Visibility: *request.Visibility, Items: []string{},
		})
	})(w, r)
}

func (app *application) modifyPlaylist(w http.ResponseWriter, r *http.Request, user account, id string) {
	var request map[string]json.RawMessage
	if err := decodeJSONBody(w, r, 1<<20, &request); err != nil {
		writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection data")
		return
	}
	var name, description *string
	var visibility *int
	var items *[]string
	if value, ok := request["name"]; ok && string(value) != "null" {
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection name")
			return
		}
		if strings.TrimSpace(parsed) != "" {
			parsed = strings.TrimSpace(parsed)
			name = &parsed
		}
	}
	if value, ok := request["description"]; ok && string(value) != "null" {
		var parsed string
		if err := json.Unmarshal(value, &parsed); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection description")
			return
		}
		description = &parsed
	}
	if value, ok := request["visibility"]; ok && string(value) != "null" {
		var parsed int
		if err := json.Unmarshal(value, &parsed); err != nil || (parsed != 0 && parsed != 1) {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection visibility")
			return
		}
		visibility = &parsed
	}
	if value, ok := request["items"]; ok && string(value) != "null" {
		var parsed []string
		if err := json.Unmarshal(value, &parsed); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection items")
			return
		}
		items = &parsed
	}
	if name == nil && description == nil && visibility == nil && items == nil {
		writeAPIStatus(w, http.StatusBadRequest, 40001, "no collection fields supplied")
		return
	}

	tx, err := app.store.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("playlist_update_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be updated")
		return
	}
	defer tx.Rollback()
	var ownerID string
	var currentVisibility int
	err = tx.QueryRowContext(r.Context(),
		"SELECT owner_id, visibility FROM playlists WHERE id = ?", id).Scan(&ownerID, &currentVisibility)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
		return
	}
	if err != nil {
		log.Printf("playlist_update_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be updated")
		return
	}
	if ownerID != user.ID {
		if currentVisibility == 0 {
			writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
		} else {
			writeAPIStatus(w, http.StatusForbidden, 60002, "permission denied")
		}
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(r.Context(), `
UPDATE playlists
SET name = COALESCE(?, name), description = COALESCE(?, description),
    visibility = COALESCE(?, visibility), updated_at = ?
WHERE id = ?`, name, description, visibility, now, id); err != nil {
		log.Printf("playlist_update_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be updated")
		return
	}
	if items != nil {
		filtered := uniqueKnownChartIDs(*items, app.catalog)
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM playlist_items WHERE playlist_id = ?", id); err != nil {
			log.Printf("playlist_items_update_error collection_id=%s err=%v", id, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection items could not be updated")
			return
		}
		for position, chartID := range filtered {
			if _, err := tx.ExecContext(r.Context(),
				"INSERT INTO playlist_items (playlist_id, chart_id, position) VALUES (?, ?, ?)",
				id, chartID, position); err != nil {
				log.Printf("playlist_items_update_error collection_id=%s err=%v", id, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection items could not be updated")
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		log.Printf("playlist_update_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be updated")
		return
	}
	collection, visibilityValue, _, err := app.getCollection(r.Context(), id, map[string]publicCollection{})
	if err != nil {
		log.Printf("playlist_read_after_update_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be loaded")
		return
	}
	collection.Visibility = visibilityValue
	writeJSON(w, http.StatusOK, struct {
		Code       int                      `json:"code"`
		Message    string                   `json:"message"`
		Collection publicCollectionSongList `json:"collection"`
	}{Code: 114514, Message: "Ok", Collection: collection})
}

func uniqueKnownChartIDs(values []string, catalog map[string]chartCatalogEntry) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		id := strings.ToLower(strings.TrimSpace(value))
		if _, exists := catalog[id]; !exists {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, catalog[id].chart.ID)
	}
	return result
}

func (app *application) destroyPlaylist(w http.ResponseWriter, r *http.Request, user account, id string, static map[string]publicCollection) {
	if _, exists := static[id]; exists {
		writeAPIStatus(w, http.StatusForbidden, 60002, "permission denied")
		return
	}
	tx, err := app.store.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("playlist_delete_error collection_id=%s user_id=%s err=%v", id, user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	defer tx.Rollback()
	var ownerID string
	var visibility int
	err = tx.QueryRowContext(r.Context(),
		"SELECT owner_id, visibility FROM playlists WHERE id = ?", id).Scan(&ownerID, &visibility)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
		return
	}
	if err != nil {
		log.Printf("playlist_delete_error collection_id=%s user_id=%s err=%v", id, user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	if ownerID != user.ID {
		if visibility == 0 {
			writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
		} else {
			writeAPIStatus(w, http.StatusForbidden, 60002, "permission denied")
		}
		return
	}
	if _, err := tx.ExecContext(r.Context(), "DELETE FROM favorites WHERE collection_id = ?", id); err != nil {
		log.Printf("playlist_favorites_delete_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	result, err := tx.ExecContext(r.Context(),
		"DELETE FROM playlists WHERE id = ? AND owner_id = ?", id, user.ID)
	if err != nil {
		log.Printf("playlist_delete_error collection_id=%s user_id=%s err=%v", id, user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	affected, err := result.RowsAffected()
	if err != nil {
		log.Printf("playlist_delete_error collection_id=%s err=%v", id, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	if affected != 1 {
		log.Printf("playlist_delete_unexpected_rows collection_id=%s rows=%d", id, affected)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("playlist_delete_error collection_id=%s user_id=%s err=%v", id, user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "collection could not be deleted")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (app *application) serveFavoriteCollectionList(collections []publicCollection) func(http.ResponseWriter, *http.Request, account) {
	static := indexCollections(collections)
	return func(w http.ResponseWriter, r *http.Request, user account) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
			return
		}
		includeExtra := strings.HasPrefix(r.UserAgent(), "MajdataPlay")
		if value := r.URL.Query().Get("isExtra"); value != "" {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid isExtra")
				return
			}
			includeExtra = includeExtra || parsed
		}
		rows, err := app.store.QueryContext(r.Context(),
			"SELECT collection_id FROM favorites WHERE user_id = ? ORDER BY created_at ASC, collection_id ASC",
			user.ID)
		if err != nil {
			log.Printf("favorite_list_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be loaded")
			return
		}
		ids := make([]string, 0)
		seen := make(map[string]struct{})
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				log.Printf("favorite_list_scan_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be loaded")
				return
			}
			ids = appendUniqueID(ids, seen, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			log.Printf("favorite_list_iter_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be loaded")
			return
		}
		if err := rows.Close(); err != nil {
			log.Printf("favorite_list_close_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be loaded")
			return
		}
		if includeExtra {
			for _, collection := range collections {
				ids = appendUniqueID(ids, seen, collection.ID)
			}
			ownRows, err := app.store.QueryContext(r.Context(),
				"SELECT id FROM playlists WHERE owner_id = ? ORDER BY updated_at DESC, id ASC", user.ID)
			if err != nil {
				log.Printf("own_playlist_list_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
				return
			}
			for ownRows.Next() {
				var id string
				if err := ownRows.Scan(&id); err != nil {
					ownRows.Close()
					log.Printf("own_playlist_scan_error user_id=%s err=%v", user.ID, err)
					writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
					return
				}
				ids = appendUniqueID(ids, seen, id)
			}
			if err := ownRows.Err(); err != nil {
				ownRows.Close()
				log.Printf("own_playlist_iter_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
				return
			}
			if err := ownRows.Close(); err != nil {
				log.Printf("own_playlist_close_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "collections could not be loaded")
				return
			}
		}
		result := make([]publicCollectionHashList, 0, len(ids))
		for _, id := range ids {
			collection, visibility, ownerID, err := app.getCollection(r.Context(), strings.ToLower(id), static)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					continue
				}
				log.Printf("favorite_collection_error collection_id=%s err=%v", id, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be loaded")
				return
			}
			if visibility == 0 && ownerID != user.ID {
				continue
			}
			hashes := make([]string, 0, len(collection.Items))
			for _, chartID := range collection.Items {
				if entry, exists := app.catalog[strings.ToLower(chartID)]; exists {
					hashes = append(hashes, entry.chart.Hash)
				}
			}
			result = append(result, publicCollectionHashList{
				ID: collection.ID, Name: collection.Name, CreatedBy: collection.CreatedBy,
				Description: collection.Description, SongHashs: hashes, IsPlayList: true,
			})
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func appendUniqueID(ids []string, seen map[string]struct{}, id string) []string {
	key := strings.ToLower(id)
	if _, exists := seen[key]; exists {
		return ids
	}
	seen[key] = struct{}{}
	return append(ids, id)
}

func (app *application) serveFavoriteCollectionDiff(collections []publicCollection) http.HandlerFunc {
	static := indexCollections(collections)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			w.Header().Set("Allow", "POST, DELETE")
			writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
			return
		}
		app.requireAccount(func(w http.ResponseWriter, r *http.Request, user account) {
			var ids []string
			if err := decodeJSONBody(w, r, 1<<20, &ids); err != nil {
				writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid collection IDs")
				return
			}
			if ids == nil {
				writeAPIStatus(w, http.StatusBadRequest, 40001, "collection ID list is required")
				return
			}
			if r.Method == http.MethodDelete {
				tx, err := app.store.BeginTx(r.Context(), nil)
				if err != nil {
					log.Printf("favorite_remove_error user_id=%s err=%v", user.ID, err)
					writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
					return
				}
				defer tx.Rollback()
				for _, id := range ids {
					if _, err := tx.ExecContext(r.Context(),
						"DELETE FROM favorites WHERE user_id = ? AND collection_id = ?", user.ID, strings.ToLower(strings.TrimSpace(id))); err != nil {
						log.Printf("favorite_remove_error user_id=%s err=%v", user.ID, err)
						writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
						return
					}
				}
				if err := tx.Commit(); err != nil {
					log.Printf("favorite_remove_error user_id=%s err=%v", user.ID, err)
					writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
					return
				}
				writeJSON(w, http.StatusOK, apiStatus{Code: 114514, Message: "Ok"})
				return
			}

			validIDs := make([]string, 0, len(ids))
			seen := make(map[string]struct{})
			for _, rawID := range ids {
				id := strings.ToLower(strings.TrimSpace(rawID))
				if id == "" {
					continue
				}
				if _, exists := seen[id]; exists {
					continue
				}
				_, visibility, ownerID, err := app.getCollection(r.Context(), id, static)
				if errors.Is(err, sql.ErrNoRows) || (visibility == 0 && ownerID != user.ID) {
					continue
				}
				if err != nil {
					log.Printf("favorite_validate_error collection_id=%s err=%v", id, err)
					writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
					return
				}
				seen[id] = struct{}{}
				validIDs = append(validIDs, id)
			}
			if len(ids) > 0 && len(validIDs) == 0 {
				writeAPIStatus(w, http.StatusNotFound, 40400, "no visible collections matched")
				return
			}
			tx, err := app.store.BeginTx(r.Context(), nil)
			if err != nil {
				log.Printf("favorite_add_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
				return
			}
			defer tx.Rollback()
			now := time.Now().UTC().Format(time.RFC3339Nano)
			for _, id := range validIDs {
				if _, err := tx.ExecContext(r.Context(),
					"INSERT OR IGNORE INTO favorites (user_id, collection_id, created_at) VALUES (?, ?, ?)",
					user.ID, id, now); err != nil {
					log.Printf("favorite_add_error user_id=%s collection_id=%s err=%v", user.ID, id, err)
					writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
					return
				}
			}
			if err := tx.Commit(); err != nil {
				log.Printf("favorite_add_error user_id=%s err=%v", user.ID, err)
				writeAPIStatus(w, http.StatusInternalServerError, -114514, "favorites could not be updated")
				return
			}
			writeJSON(w, http.StatusOK, apiStatus{Code: 114514, Message: "Ok"})
		})(w, r)
	}
}
