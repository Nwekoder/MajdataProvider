package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

type scoreAccuracy struct {
	DX      float64 `json:"dx"`
	Classic float64 `json:"classic"`
}

type scoreSubmission struct {
	Acc        scoreAccuracy `json:"acc"`
	DXScore    int64         `json:"dxScore"`
	ComboState int           `json:"comboState"`
	Hash       string        `json:"hash"`
	ChartLevel int           `json:"chartLevel"`
}

type accountScore struct {
	Acc        scoreAccuracy `json:"acc"`
	DXScore    int64         `json:"dxScore"`
	ChartLevel int           `json:"chartLevel"`
	ComboState int           `json:"comboState"`
	Hash       string        `json:"hash"`
	LevelHash  string        `json:"levelHash"`
	ChartInfo  any           `json:"chartInfo"`
	Timestamp  time.Time     `json:"timestamp"`
}

type chartRankPlayer struct {
	Username string `json:"username"`
}

type chartRankScore struct {
	Player     chartRankPlayer `json:"player"`
	Acc        float64         `json:"acc"`
	ComboState int             `json:"comboState"`
	Timestamp  time.Time       `json:"timestamp"`
}

type chartLeaderboardResponse struct {
	Hash   string             `json:"hash"`
	Scores [][]chartRankScore `json:"scores"`
}

func (app *application) serveChartScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	chartID := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	entry, ok := app.catalog[chartID]
	if !ok {
		writeAPIStatus(w, http.StatusNotFound, 40400, "chart not found")
		return
	}
	app.requireAccount(func(w http.ResponseWriter, r *http.Request, user account) {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var payload json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&payload); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		var supplied map[string]json.RawMessage
		if err := json.Unmarshal(payload, &supplied); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		for _, field := range []string{"acc", "dxScore", "comboState", "hash", "chartLevel"} {
			if _, ok := supplied[field]; !ok {
				writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
				return
			}
		}
		var suppliedAccuracy map[string]json.RawMessage
		if err := json.Unmarshal(supplied["acc"], &suppliedAccuracy); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		for _, field := range []string{"dx", "classic"} {
			if _, ok := suppliedAccuracy[field]; !ok || isJSONNull(suppliedAccuracy[field]) {
				writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
				return
			}
		}
		var submitted scoreSubmission
		if err := json.Unmarshal(payload, &submitted); err != nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		if hasNullScoreField(supplied) || !validScore(submitted) ||
			submitted.Hash != entry.chart.Hash ||
			submitted.ChartLevel >= len(entry.chart.Levels) ||
			entry.chart.Levels[submitted.ChartLevel] == nil {
			writeAPIStatus(w, http.StatusBadRequest, 40000, "invalid score")
			return
		}
		now := time.Now().UTC()
		_, err := app.store.ExecContext(r.Context(),
			`INSERT INTO scores
			 (user_id, chart_id, chart_level, acc_dx, acc_classic, dx_score, combo_state, hash, played_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			user.ID, chartID, submitted.ChartLevel, submitted.Acc.DX, submitted.Acc.Classic,
			submitted.DXScore, submitted.ComboState, submitted.Hash, now.Format(time.RFC3339Nano))
		if err != nil {
			log.Printf("score_save_error user_id=%s chart_id=%s err=%v", user.ID, chartID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "score could not be saved")
			return
		}
		writeJSON(w, http.StatusOK, apiStatus{Code: 114514, Message: "success"})
	})(w, r)
}

func hasNullScoreField(fields map[string]json.RawMessage) bool {
	for _, field := range []string{"acc", "dxScore", "comboState", "hash", "chartLevel"} {
		if isJSONNull(fields[field]) {
			return true
		}
	}
	return false
}

func isJSONNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func validScore(score scoreSubmission) bool {
	return score.ChartLevel >= 0 && score.ChartLevel <= 6 &&
		score.ComboState >= 0 && score.ComboState <= 4 &&
		score.DXScore >= 0 &&
		!math.IsNaN(score.Acc.DX) && !math.IsInf(score.Acc.DX, 0) &&
		!math.IsNaN(score.Acc.Classic) && !math.IsInf(score.Acc.Classic, 0) &&
		score.Acc.DX >= 0 && score.Acc.DX <= 101 &&
		score.Acc.Classic >= 0 && score.Acc.Classic <= 101 &&
		score.Hash != "" &&
		len(score.Hash) <= 128 && !strings.ContainsAny(score.Hash, "\r\n")
}

func (app *application) serveAccountScores(w http.ResponseWriter, r *http.Request, user account) {
	rows, err := app.store.QueryContext(r.Context(), `
WITH ranked AS (
    SELECT acc_dx, acc_classic, dx_score, chart_level, combo_state, hash, played_at,
           ROW_NUMBER() OVER (
               PARTITION BY chart_id, chart_level
               ORDER BY acc_classic DESC, dx_score DESC, played_at DESC, id DESC
           ) AS position
    FROM scores
    WHERE user_id = ?
)
SELECT acc_dx, acc_classic, dx_score, chart_level, combo_state, hash, played_at
FROM ranked
WHERE position = 1
ORDER BY played_at DESC`, user.ID)
	if err != nil {
		log.Printf("scores_query_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "scores could not be loaded")
		return
	}
	defer rows.Close()
	scores := make([]accountScore, 0)
	for rows.Next() {
		var score accountScore
		var playedAt string
		if err := rows.Scan(&score.Acc.DX, &score.Acc.Classic, &score.DXScore, &score.ChartLevel,
			&score.ComboState, &score.Hash, &playedAt); err != nil {
			log.Printf("scores_scan_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "scores could not be loaded")
			return
		}
		score.Timestamp, err = time.Parse(time.RFC3339Nano, playedAt)
		if err != nil {
			log.Printf("scores_timestamp_error user_id=%s err=%v", user.ID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "scores could not be loaded")
			return
		}
		scores = append(scores, score)
	}
	if err := rows.Err(); err != nil {
		log.Printf("scores_iteration_error user_id=%s err=%v", user.ID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "scores could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, scores)
}

func (app *application) serveChartLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIStatus(w, http.StatusMethodNotAllowed, 40000, "method not allowed")
		return
	}
	chartID := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	entry, ok := app.catalog[chartID]
	if !ok {
		writeAPIStatus(w, http.StatusNotFound, 40400, "chart not found")
		return
	}

	rows, err := app.store.QueryContext(r.Context(), `
WITH personal_best AS (
    SELECT scores.chart_level, users.username, scores.acc_classic, scores.dx_score,
           scores.combo_state, scores.played_at,
           ROW_NUMBER() OVER (
               PARTITION BY scores.chart_level, scores.user_id
               ORDER BY scores.acc_classic DESC, scores.dx_score DESC, scores.played_at DESC, scores.id DESC
           ) AS position
    FROM scores
    JOIN users ON users.id = scores.user_id
    WHERE scores.chart_id = ?
)
SELECT chart_level, username, acc_classic, dx_score, combo_state, played_at
FROM personal_best
WHERE position = 1
ORDER BY chart_level ASC, acc_classic DESC, dx_score DESC, played_at DESC, username COLLATE NOCASE ASC`,
		chartID)
	if err != nil {
		log.Printf("leaderboard_query_error chart_id=%s err=%v", chartID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "leaderboard could not be loaded")
		return
	}
	defer rows.Close()

	const leaderboardDifficultyCount = 8
	scores := make([][]chartRankScore, leaderboardDifficultyCount)
	for i := range scores {
		scores[i] = []chartRankScore{}
	}
	for rows.Next() {
		var level int
		var result chartRankScore
		var dxScore int64
		var playedAt string
		if err := rows.Scan(&level, &result.Player.Username, &result.Acc, &dxScore, &result.ComboState, &playedAt); err != nil {
			log.Printf("leaderboard_scan_error chart_id=%s err=%v", chartID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "leaderboard could not be loaded")
			return
		}
		if level < 0 || level >= leaderboardDifficultyCount-1 {
			log.Printf("leaderboard_invalid_level chart_id=%s chart_level=%d", chartID, level)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "leaderboard could not be loaded")
			return
		}
		result.Timestamp, err = time.Parse(time.RFC3339Nano, playedAt)
		if err != nil {
			log.Printf("leaderboard_timestamp_error chart_id=%s err=%v", chartID, err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "leaderboard could not be loaded")
			return
		}
		scores[level] = append(scores[level], result)
	}
	if err := rows.Err(); err != nil {
		log.Printf("leaderboard_iteration_error chart_id=%s err=%v", chartID, err)
		writeAPIStatus(w, http.StatusInternalServerError, -114514, "leaderboard could not be loaded")
		return
	}
	writeJSON(w, http.StatusOK, chartLeaderboardResponse{Hash: entry.chart.Hash, Scores: scores})
}
