package main

import (
	"bytes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

const (
	chartAPIPrefix    = "/api/maichart/"
	weekCacheControl  = "public,max-age=604800"
	maxThumbnailPixel = 512
)

var errNoFileFound = errors.New("no file found")

type chartAssetRequest struct {
	chartID string
	folder  string
	asset   string
}

type chartCatalogEntry struct {
	chart  ParsedChart
	folder string
}

type chartFileResource struct {
	URL    string `json:"url"`
	MD5    string `json:"md5"`
	SHA256 string `json:"sha256"`
	Length int64  `json:"length"`
}

type chartResources struct {
	Maidata   *chartFileResource `json:"maidata"`
	Track     *chartFileResource `json:"track"`
	Video     *chartFileResource `json:"video"`
	CoverArt  *chartFileResource `json:"coverArt"`
	Thumbnail *chartFileResource `json:"thumbnail"`
}

type chartInfoResponse struct {
	Metadata    ParsedChart    `json:"metadata"`
	Resources   chartResources `json:"resources"`
	LevelHashes []*string      `json:"levelHashes,omitempty"`
}

func serveServerInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{
		Name: "Majdata Provider",
	})
}

func logServe(path, file, extra string) {
	if extra == "" {
		log.Printf("serve path=%s file=%s", path, file)
		return
	}
	log.Printf("serve path=%s file=%s %s", path, file, extra)
}

func logServeError(path string, err error) {
	log.Printf("serve_error path=%s err=%v", path, err)
}

func loadChartCatalog(root string) (map[string]chartCatalogEntry, error) {
	folders, err := discoverChartFolders(filepath.Join(root, "charts"))
	if err != nil {
		return nil, err
	}
	foldersByID := make(map[string]string, len(folders))
	for _, folder := range folders {
		foldersByID[strings.ToLower(chartIDForFolder(folder))] = folder
	}

	manifestPath := filepath.Join(root, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) && len(folders) == 0 {
			return map[string]chartCatalogEntry{}, nil
		}
		return nil, err
	}
	var manifest struct {
		Charts []ParsedChart `json:"charts"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode chart manifest: %w", err)
	}

	catalog := make(map[string]chartCatalogEntry, len(manifest.Charts))
	for _, chart := range manifest.Charts {
		key := strings.ToLower(chart.ID)
		if folder, ok := foldersByID[key]; ok {
			catalog[key] = chartCatalogEntry{chart: chart, folder: folder}
		}
	}
	return catalog, nil
}

func serveChartList(root string) http.HandlerFunc {
	manifestPath := filepath.Join(root, "manifest.json")
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			http.Error(w, "manifest not found", http.StatusNotFound)
			return
		}
		var manifest struct {
			Charts []ParsedChart `json:"charts"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			http.Error(w, "invalid manifest", http.StatusInternalServerError)
			return
		}
		out, err := json.MarshalIndent(manifest.Charts, "", "  ")
		if err != nil {
			http.Error(w, "failed to serialize", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if _, err := w.Write(out); err != nil {
			http.Error(w, "failed to write response", http.StatusInternalServerError)
			return
		}
		logServe(manifestPath, filepath.Base(manifestPath), "")
	}
}

func serveBatchChartInfo(root string, catalog map[string]chartCatalogEntry) http.HandlerFunc {
	chartsDir := filepath.Join(root, "charts")
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var chartIDs []string
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if err := decoder.Decode(&chartIDs); err != nil {
			http.Error(w, "invalid chart ID list", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "invalid chart ID list", http.StatusBadRequest)
			return
		}

		infos := make([]chartInfoResponse, 0, len(chartIDs))
		for _, chartID := range chartIDs {
			entry, ok := catalog[strings.ToLower(strings.TrimSpace(chartID))]
			if !ok {
				continue
			}
			info, err := buildChartInfo(r, chartsDir, entry)
			if err != nil {
				logServeError(chartID, err)
				http.Error(w, "failed to build chart info", http.StatusInternalServerError)
				return
			}
			infos = append(infos, info)
		}
		writeJSON(w, http.StatusOK, infos)
	}
}

func parseChartAssetRequest(path string, catalog map[string]chartCatalogEntry) (chartAssetRequest, error) {
	if !strings.HasPrefix(path, chartAPIPrefix) {
		return chartAssetRequest{}, fmt.Errorf("invalid api prefix")
	}
	rest := strings.TrimPrefix(path, chartAPIPrefix)
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return chartAssetRequest{}, fmt.Errorf("invalid api path")
	}
	entry, ok := catalog[strings.ToLower(parts[0])]
	if !ok {
		return chartAssetRequest{}, fmt.Errorf("unknown chart id")
	}
	return chartAssetRequest{
		chartID: entry.chart.ID,
		folder:  entry.folder,
		asset:   parts[1],
	}, nil
}

func serveChartFile(root string, catalog map[string]chartCatalogEntry) http.HandlerFunc {
	chartsDir := filepath.Join(root, "charts")
	return func(w http.ResponseWriter, r *http.Request) {
		req, err := parseChartAssetRequest(r.URL.Path, catalog)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		switch req.asset {
		case "chart":
			maidataPath := filepath.Join(chartsDir, req.folder, "maidata.txt")
			if err := serveFileWithHash(w, maidataPath, "text/plain; charset=utf-8", "maidata.txt", ""); err != nil {
				serveChartAssetError(w, r, maidataPath, err)
			}
		case "info":
			entry := catalog[strings.ToLower(req.chartID)]
			info, err := buildChartInfo(r, chartsDir, entry)
			if err != nil {
				logServeError(req.chartID, err)
				http.Error(w, "failed to build chart info", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, info)
		case "track":
			serveChartTrack(w, r, chartsDir, req.folder)
		case "image":
			fullImage := parseFullImageQuery(r.URL.Query())
			serveChartImage(w, r, chartsDir, req.folder, fullImage)
		case "video":
			serveChartVideo(w, r, chartsDir, req.folder)
		case "thumbnail":
			path, err := firstExistingPath(chartsDir, req.folder, []string{"bg-compress.jpg"})
			if err != nil {
				serveChartAssetError(w, r, filepath.Join(chartsDir, req.folder, "bg-compress.jpg"), err)
				return
			}
			if err := serveMediaFileWithHash(w, path, "image/jpeg", "bg-compress.jpg"); err != nil {
				serveChartAssetError(w, r, path, err)
			}
		default:
			http.NotFound(w, r)
		}
	}
}

func buildChartInfo(r *http.Request, chartsDir string, entry chartCatalogEntry) (chartInfoResponse, error) {
	folderPath := filepath.Join(chartsDir, entry.folder)
	id := entry.chart.ID
	resources := chartResources{}

	var err error
	resources.Maidata, err = chartResourceForPath(r, id, "chart", false, filepath.Join(folderPath, "maidata.txt"))
	if err != nil {
		return chartInfoResponse{}, err
	}
	resources.Track, err = optionalChartResource(r, id, "track", false, chartsDir, entry.folder, []string{"track.mp3"})
	if err != nil {
		return chartInfoResponse{}, err
	}
	resources.Video, err = optionalChartResource(r, id, "video", false, chartsDir, entry.folder, []string{"bg.mp4", "pv.mp4", "mv.mp4"})
	if err != nil {
		return chartInfoResponse{}, err
	}
	resources.CoverArt, err = optionalChartResource(r, id, "image", true, chartsDir, entry.folder, []string{"bg.jpg", "bg.png"})
	if err != nil {
		return chartInfoResponse{}, err
	}
	resources.Thumbnail, err = optionalChartResource(r, id, "thumbnail", false, chartsDir, entry.folder, []string{"bg-compress.jpg"})
	if err != nil {
		return chartInfoResponse{}, err
	}

	return chartInfoResponse{
		Metadata:  entry.chart,
		Resources: resources,
	}, nil
}

func optionalChartResource(r *http.Request, chartID, asset string, fullImage bool, chartsDir, folder string, candidates []string) (*chartFileResource, error) {
	path, err := firstExistingPath(chartsDir, folder, candidates)
	if errors.Is(err, errNoFileFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return chartResourceForPath(r, chartID, asset, fullImage, path)
}

func chartResourceForPath(r *http.Request, chartID, asset string, fullImage bool, path string) (*chartFileResource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	md5Hash := md5.New()
	sha256Hash := sha256.New()
	length, err := io.Copy(io.MultiWriter(md5Hash, sha256Hash), file)
	if err != nil {
		return nil, err
	}
	return &chartFileResource{
		URL:    chartResourceURL(r, chartID, asset, fullImage),
		MD5:    hex.EncodeToString(md5Hash.Sum(nil)),
		SHA256: hex.EncodeToString(sha256Hash.Sum(nil)),
		Length: length,
	}, nil
}

func chartResourceURL(r *http.Request, chartID, asset string, fullImage bool) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	resourceURL := url.URL{
		Scheme: scheme,
		Host:   r.Host,
		Path:   chartAPIPrefix + url.PathEscape(chartID) + "/" + url.PathEscape(asset),
	}
	if fullImage {
		resourceURL.RawQuery = "fullimage=true"
	}
	return resourceURL.String()
}

func serveChartChanges(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	from, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("from"))
	if err != nil {
		http.Error(w, "invalid from time", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	if from.After(now) {
		http.Error(w, "from time is in the future", http.StatusBadRequest)
		return
	}
	to := now
	if toValue := r.URL.Query().Get("to"); toValue != "" {
		to, err = time.Parse(time.RFC3339Nano, toValue)
		if err != nil {
			http.Error(w, "invalid to time", http.StatusBadRequest)
			return
		}
		if to.Before(from) {
			http.Error(w, "to time precedes from time", http.StatusBadRequest)
			return
		}
		if to.After(now) {
			to = now
		} else {
			to = to.UTC()
		}
	}
	writeJSON(w, http.StatusOK, struct {
		To         time.Time `json:"to"`
		IsComplete bool      `json:"isComplete"`
		Changes    []any     `json:"changes"`
	}{
		To:         to,
		IsComplete: false,
		Changes:    []any{},
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	out, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "failed to serialize response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	if _, err := w.Write(out); err != nil {
		log.Printf("response_write_error err=%v", err)
	}
}

func serveChartAssetError(w http.ResponseWriter, r *http.Request, path string, err error) {
	if errors.Is(err, errNoFileFound) || errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r)
		return
	}
	logServeError(path, err)
	http.Error(w, "failed to serve chart asset", http.StatusInternalServerError)
}

func parseFullImageQuery(values map[string][]string) bool {
	v, ok := values["fullimage"]
	if !ok || len(v) == 0 {
		return false
	}
	return v[0] == "true"
}

func serveChartTrack(w http.ResponseWriter, r *http.Request, chartsDir, folder string) {
	trackPath, err := firstExistingPath(chartsDir, folder, []string{"track.mp3"})
	if err != nil {
		serveChartAssetError(w, r, filepath.Join(chartsDir, folder, "track.mp3"), err)
		return
	}
	if err := serveMediaFileWithHash(w, trackPath, "audio/mp3", "track.mp3"); err != nil {
		serveChartAssetError(w, r, trackPath, err)
	}
}

func serveChartVideo(w http.ResponseWriter, r *http.Request, chartsDir, folder string) {
	videoPath, err := firstExistingPath(chartsDir, folder, []string{"bg.mp4", "pv.mp4", "mv.mp4"})
	if err != nil {
		serveChartAssetError(w, r, filepath.Join(chartsDir, folder, "bg.mp4"), err)
		return
	}
	if err := serveMediaFileWithHash(w, videoPath, "video/mp4", "bg.mp4"); err != nil {
		serveChartAssetError(w, r, videoPath, err)
	}
}

func serveMediaFileWithHash(w http.ResponseWriter, path, contentType, downloadName string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", downloadName))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", weekCacheControl)
	if err := writeHashedBody(w, data); err != nil {
		return err
	}
	logServe(path, downloadName, "")
	return nil
}

func serveChartImage(w http.ResponseWriter, r *http.Request, chartsDir, folder string, fullImage bool) {
	fullPath, err := firstExistingPath(chartsDir, folder, []string{"bg.jpg", "bg.png"})
	if err != nil {
		serveChartAssetError(w, r, filepath.Join(chartsDir, folder, "bg.jpg"), err)
		return
	}
	logExtra := fmt.Sprintf("fullImage=%t", fullImage)
	if fullImage {
		ext := filepath.Ext(fullPath)
		contentType := mime.TypeByExtension(ext)
		if err := serveFileWithHash(w, fullPath, contentType, filepath.Base(fullPath), logExtra); err != nil {
			serveChartAssetError(w, r, fullPath, err)
		}
		return
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "failed to read file", http.StatusInternalServerError)
		return
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		http.Error(w, "failed to decode image", http.StatusInternalServerError)
		return
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	newW, newH := resizeDimensions(width, height, maxThumbnailPixel)

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	var out bytes.Buffer
	switch format {
	case "png":
		if err := png.Encode(&out, dst); err != nil {
			http.Error(w, "failed to encode png", http.StatusInternalServerError)
			return
		}
	default:
		if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: 85}); err != nil {
			http.Error(w, "failed to encode jpeg", http.StatusInternalServerError)
			return
		}
		format = "jpeg"
	}

	contentType := "image/jpeg"
	if format == "png" {
		contentType = "image/png"
	}
	if err := serveDataWithHash(w, fullPath, filepath.Base(fullPath), out.Bytes(), contentType, logExtra); err != nil {
		logServeError(fullPath, err)
	}
}

func resizeDimensions(width, height, maxDim int) (int, int) {
	if width <= maxDim && height <= maxDim {
		return width, height
	}
	if width >= height {
		newW := maxDim
		newH := int(float64(height) * float64(maxDim) / float64(width))
		if newH < 1 {
			newH = 1
		}
		return newW, newH
	}
	newH := maxDim
	newW := int(float64(width) * float64(maxDim) / float64(height))
	if newW < 1 {
		newW = 1
	}
	return newW, newH
}

func firstExistingPath(chartsDir, folder string, candidates []string) (string, error) {
	for _, name := range candidates {
		path := filepath.Join(chartsDir, folder, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() {
			return path, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", errNoFileFound
}

func serveFileWithHash(w http.ResponseWriter, path, contentType, downloadName, extra string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return serveDataWithHash(w, path, downloadName, data, contentType, extra)
}

func serveDataWithHash(w http.ResponseWriter, path, downloadName string, data []byte, contentType, extra string) error {
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if downloadName != "" {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", downloadName))
	}
	if err := writeHashedBody(w, data); err != nil {
		return err
	}
	logServe(path, downloadName, extra)
	return nil
}

func writeHashedBody(w http.ResponseWriter, data []byte) error {
	sum := sha256.Sum256(data)
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("hash", base64.StdEncoding.EncodeToString(sum[:]))
	_, err := w.Write(data)
	return err
}
