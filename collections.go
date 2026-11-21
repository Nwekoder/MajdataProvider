package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	publicCollectionCreator = "Majdata Provider"
	maxCollectionPageSize   = 100
)

var maimaiSeriesReleaseDates = map[string]string{
	"maimai":                  "2012-07-11",
	"maimai PLUS":             "2012-12-13",
	"maimai GreeN":            "2013-07-11",
	"maimai GreeN PLUS":       "2014-01-31",
	"maimai ORANGE":           "2014-09-18",
	"maimai ORANGE PLUS":      "2015-03-19",
	"maimai PiNK":             "2015-12-09",
	"maimai PiNK PLUS":        "2016-06-30",
	"maimai MURASAKi":         "2016-12-15",
	"maimai MURASAKi PLUS":    "2017-06-22",
	"maimai MiLK":             "2017-12-14",
	"maimai MiLK PLUS":        "2018-06-21",
	"maimai FiNALE":           "2018-12-13",
	"maimai DX":               "2019-07-11",
	"maimai DX PLUS":          "2020-01-23",
	"maimai DX Splash":        "2020-09-17",
	"maimai DX Splash PLUS":   "2021-03-18",
	"maimai DX UNiVERSE":      "2021-09-16",
	"maimai DX UNiVERSE PLUS": "2022-03-24",
	"maimai DX FESTiVAL":      "2022-09-15",
	"maimai DX FESTiVAL PLUS": "2023-03-23",
	"maimai DX BUDDiES":       "2023-09-14",
	"maimai DX BUDDiES PLUS":  "2024-03-21",
	"maimai DX PRiSM":         "2024-09-12",
	"maimai DX PRiSM PLUS":    "2025-03-13",
	"maimai DX CiRCLE":        "2025-09-18",
	"maimai DX CiRCLE PLUS":   "2026-03-19",
}

type publicCollection struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CreatedBy   string   `json:"createdBy"`
	Description string   `json:"description"`
	Count       int      `json:"count"`
	Items       []string `json:"items"`
	SongHashs   []string `json:"songHashs"`
	folderName  string
}

type collectionPlaylistDefinition struct {
	Name     string   `json:"name"`
	LevelIDs []string `json:"levelIds"`
}

type collectionManifest struct {
	Collections []publicCollection `json:"collections"`
}

type publicCollectionListItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CreatedBy   string `json:"createdBy"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

type publicCollectionSongList struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CreatedBy   string   `json:"createdBy"`
	Description string   `json:"description"`
	Count       int      `json:"count"`
	Visibility  int      `json:"visibility"`
	Items       []string `json:"items"`
}

type publicCollectionHashList struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	CreatedBy       string   `json:"createdBy"`
	Description     string   `json:"description"`
	SongHashs       []string `json:"songHashs"`
	IsPlayList      bool     `json:"isPlayList"`
	IsForceGameover bool     `json:"isForceGameover"`
	SongLevels      []int    `json:"songLevels,omitempty"`
}

func cleanupEmptyUncategorized(root string) error {
	path := filepath.Join(root, "charts", "uncategorized")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect uncategorized charts folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("uncategorized charts path is not a directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("read uncategorized charts folder: %w", err)
	}
	if len(entries) != 0 {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove empty uncategorized charts folder: %w", err)
	}
	log.Printf("remove_empty path=%s", path)
	return nil
}

func buildPublicCollections(root string, catalog map[string]chartCatalogEntry) ([]publicCollection, error) {
	collectionsDir := filepath.Join(root, "collections")
	dirEntries, err := os.ReadDir(collectionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return []publicCollection{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read collections directory: %w", err)
	}

	chartsBySongID := make(map[string]chartCatalogEntry, len(catalog))
	for _, entry := range catalog {
		songID := filepath.Base(filepath.FromSlash(entry.folder))
		if previous, exists := chartsBySongID[songID]; exists {
			return nil, fmt.Errorf("duplicate chart songId %q in %q and %q", songID, previous.folder, entry.folder)
		}
		chartsBySongID[songID] = entry
	}

	result := make([]publicCollection, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		if !dirEntry.IsDir() {
			continue
		}
		collectionDir := filepath.Join(collectionsDir, dirEntry.Name())
		playlistPath := filepath.Join(collectionDir, "playlist.json")
		data, err := os.ReadFile(playlistPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read collection playlist %q: %w", playlistPath, err)
		}

		var definition collectionPlaylistDefinition
		if err := json.Unmarshal(data, &definition); err != nil {
			return nil, fmt.Errorf("decode collection playlist %q: %w", playlistPath, err)
		}
		if definition.LevelIDs == nil {
			return nil, fmt.Errorf("collection playlist %q is missing levelIds", playlistPath)
		}
		name := strings.TrimSpace(definition.Name)
		if name == "" {
			name = dirEntry.Name()
		}
		collection := publicCollection{
			ID:          chartIDForFolder(filepath.ToSlash(filepath.Join("collection", dirEntry.Name()))),
			Name:        name,
			CreatedBy:   publicCollectionCreator,
			Description: "Playlist from collections/" + filepath.ToSlash(dirEntry.Name()) + "/playlist.json.",
			Items:       make([]string, 0, len(definition.LevelIDs)),
			SongHashs:   make([]string, 0, len(definition.LevelIDs)),
			folderName:  dirEntry.Name(),
		}
		for _, songID := range definition.LevelIDs {
			entry, exists := chartsBySongID[songID]
			if !exists {
				continue
			}
			collection.Items = append(collection.Items, entry.chart.ID)
			collection.SongHashs = append(collection.SongHashs, entry.chart.Hash)
		}
		collection.Count = len(collection.Items)
		result = append(result, collection)
	}

	sort.SliceStable(result, func(i, j int) bool {
		leftDate, leftIsSeries := maimaiSeriesReleaseDates[result[i].folderName]
		rightDate, rightIsSeries := maimaiSeriesReleaseDates[result[j].folderName]
		if leftIsSeries != rightIsSeries {
			return leftIsSeries
		}
		if leftIsSeries && leftDate != rightDate {
			return leftDate < rightDate
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	if err := writeCollectionManifest(collectionsDir, result); err != nil {
		return nil, err
	}
	return result, nil
}

func writeCollectionManifest(collectionsDir string, collections []publicCollection) error {
	data, err := json.MarshalIndent(collectionManifest{Collections: collections}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode collection manifest: %w", err)
	}
	path := filepath.Join(collectionsDir, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write collection manifest %q: %w", path, err)
	}
	log.Printf("build path=%s file=%s", path, filepath.Base(path))
	return nil
}

func servePublicCollectionList(collections []publicCollection) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, pageSize, ok := parseCollectionPagination(r.URL.Query())
		if !ok {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid pagination")
			return
		}
		createdBy := strings.TrimSpace(r.URL.Query().Get("createdBy"))
		keyword := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("keyword")))
		matches := make([]publicCollectionListItem, 0, len(collections))
		for _, collection := range collections {
			if createdBy != "" && !strings.EqualFold(collection.CreatedBy, createdBy) {
				continue
			}
			if keyword != "" &&
				!strings.Contains(strings.ToLower(collection.Name), keyword) &&
				!strings.Contains(strings.ToLower(collection.Description), keyword) {
				continue
			}
			matches = append(matches, publicCollectionListItem{
				ID:          collection.ID,
				Name:        collection.Name,
				CreatedBy:   collection.CreatedBy,
				Description: collection.Description,
				Count:       collection.Count,
			})
		}

		if page > len(matches)/pageSize {
			writeJSON(w, http.StatusOK, []publicCollectionListItem{})
			return
		}
		start := page * pageSize
		if start >= len(matches) {
			writeJSON(w, http.StatusOK, []publicCollectionListItem{})
			return
		}
		end := start + pageSize
		if end > len(matches) {
			end = len(matches)
		}
		writeJSON(w, http.StatusOK, matches[start:end])
	}
}

func serveGameCollectionList(collections []publicCollection) func(http.ResponseWriter, *http.Request, account) {
	return func(w http.ResponseWriter, r *http.Request, _ account) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
			return
		}
		result := make([]publicCollectionHashList, 0, len(collections))
		for _, collection := range collections {
			result = append(result, publicCollectionHashList{
				ID:              collection.ID,
				Name:            collection.Name,
				CreatedBy:       collection.CreatedBy,
				Description:     collection.Description,
				SongHashs:       collection.SongHashs,
				IsPlayList:      true,
				IsForceGameover: false,
			})
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func parseCollectionPagination(query url.Values) (int, int, bool) {
	page := 0
	pageSize := 30
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
	return page, pageSize, true
}

func servePublicCollection(collections []publicCollection) http.HandlerFunc {
	byID := make(map[string]publicCollection, len(collections))
	for _, collection := range collections {
		byID[strings.ToLower(collection.ID)] = collection
	}

	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 4 || parts[0] != "api" || parts[1] != "collection" {
			http.NotFound(w, r)
			return
		}
		collection, exists := byID[strings.ToLower(parts[2])]
		if !exists {
			writeAPIStatus(w, http.StatusNotFound, 40400, "collection not found")
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
			return
		}

		switch parts[3] {
		case "songlist":
			writeJSON(w, http.StatusOK, publicCollectionSongList{
				ID:          collection.ID,
				Name:        collection.Name,
				CreatedBy:   collection.CreatedBy,
				Description: collection.Description,
				Count:       collection.Count,
				Visibility:  1,
				Items:       collection.Items,
			})
		case "hashlist":
			writeJSON(w, http.StatusOK, publicCollectionHashList{
				ID:              collection.ID,
				Name:            collection.Name,
				CreatedBy:       collection.CreatedBy,
				Description:     collection.Description,
				SongHashs:       collection.SongHashs,
				IsPlayList:      true,
				IsForceGameover: false,
			})
		default:
			http.NotFound(w, r)
		}
	}
}
