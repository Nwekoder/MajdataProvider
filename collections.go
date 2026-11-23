package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	publicCollectionCreator = "Majdata Provider"
	maxCollectionPageSize   = 100
)

type publicCollection struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CreatedBy   string   `json:"createdBy"`
	Description string   `json:"description"`
	Count       int      `json:"count"`
	Items       []string `json:"items"`
	SongHashs   []string `json:"songHashs"`
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

	sort.Slice(result, func(i, j int) bool {
		left, right := strings.ToLower(result[i].Name), strings.ToLower(result[j].Name)
		if left != right {
			return left < right
		}
		return result[i].ID < result[j].ID
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
