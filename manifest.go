package main

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

func buildManifest(root string) error {
	chartsDir := filepath.Join(root, "charts")
	info, err := os.Stat(chartsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("charts is not a directory")
	}

	candidateFolders, err := discoverChartFolders(chartsDir)
	if err != nil {
		return err
	}

	manifest := struct {
		Charts []ParsedChart `json:"charts"`
	}{
		Charts: []ParsedChart{},
	}

	type chartResult struct {
		chart ParsedChart
		err   error
	}

	workerLimit := runtime.NumCPU()
	if workerLimit < 1 {
		workerLimit = 1
	}
	sem := make(chan struct{}, workerLimit)
	results := make(chan chartResult, len(candidateFolders))
	var wg sync.WaitGroup

	for _, folderName := range candidateFolders {
		wg.Add(1)
		go func(folder string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			maidataPath := filepath.Join(chartsDir, folder, "maidata.txt")
			data, err := os.ReadFile(maidataPath)
			if err != nil {
				results <- chartResult{err: err}
				return
			}
			info, err := os.Stat(maidataPath)
			if err != nil {
				results <- chartResult{err: err}
				return
			}
			parsed, err := parseMaidata(data)
			if err != nil {
				results <- chartResult{err: err}
				return
			}
			parsed.ID = chartIDForFolder(folder)
			parsed.Description = ""
			parsed.Timestamp = info.ModTime().Format(time.RFC3339Nano)
			sum := md5.Sum(data)
			parsed.Hash = base64.StdEncoding.EncodeToString(sum[:])
			results <- chartResult{chart: parsed}
		}(folderName)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	total := len(candidateFolders)
	loaded := 0
	if total == 0 {
		fmt.Print("\rCharts loaded: 0/0\n")
	}
	for result := range results {
		loaded++
		fmt.Printf("\rLoading charts: %d/%d", loaded, total)
		if result.err != nil {
			fmt.Println()
			return result.err
		}
		manifest.Charts = append(manifest.Charts, result.chart)
	}
	if total > 0 {
		fmt.Printf("\rCharts loaded: %d/%d\n", loaded, total)
	}

	out, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
		return err
	}
	log.Printf("build path=%s file=%s", manifestPath, "manifest.json")
	return nil
}

func discoverChartFolders(chartsDir string) ([]string, error) {
	candidateFolders := []string{}
	err := filepath.WalkDir(chartsDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != "maidata.txt" {
			return nil
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(chartsDir, dir)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel != "." {
			candidateFolders = append(candidateFolders, rel)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return candidateFolders, nil
}

// The fixed namespace keeps folder-derived chart IDs stable across restarts.
var chartIDNamespace = [16]byte{
	0x42, 0x2d, 0x7d, 0x5a, 0x8c, 0x1f, 0x4c, 0x2b,
	0x9a, 0x61, 0x33, 0x45, 0x7e, 0x90, 0x1f, 0x6a,
}

func chartIDForFolder(folder string) string {
	name := filepath.ToSlash(filepath.Clean(folder))
	input := append(chartIDNamespace[:], []byte(name)...)
	hash := sha1.Sum(input)
	id := hash[:16]
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80

	encoded := hex.EncodeToString(id)
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		encoded[:8],
		encoded[8:12],
		encoded[12:16],
		encoded[16:20],
		encoded[20:],
	)
}
