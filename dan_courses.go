package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

const danCourseCreator = "Server Admin"

type danCourse struct {
	ID              string
	Name            string
	Description     string
	ChartIDs        []string
	SongLevels      []int
	IsForceGameover bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (app *application) listDanCourses(ctx context.Context) ([]danCourse, error) {
	rows, err := app.store.QueryContext(ctx, `
SELECT id, name, description, chart_ids, song_levels, is_force_gameover, created_at, updated_at
FROM dan_courses
ORDER BY name COLLATE NOCASE ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query dan courses: %w", err)
	}
	defer rows.Close()

	courses := make([]danCourse, 0)
	for rows.Next() {
		course, err := scanDanCourse(rows)
		if err != nil {
			return nil, err
		}
		courses = append(courses, course)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dan courses: %w", err)
	}
	return courses, nil
}

func (app *application) getDanCourse(ctx context.Context, id string) (danCourse, error) {
	course, err := scanDanCourse(app.store.QueryRowContext(ctx, `
SELECT id, name, description, chart_ids, song_levels, is_force_gameover, created_at, updated_at
FROM dan_courses
WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return danCourse{}, sql.ErrNoRows
		}
		return danCourse{}, fmt.Errorf("query dan course: %w", err)
	}
	return course, nil
}

type danCourseRow interface {
	Scan(dest ...any) error
}

func scanDanCourse(row danCourseRow) (danCourse, error) {
	var course danCourse
	var chartIDs, songLevels, createdAt, updatedAt string
	var forceGameover int
	if err := row.Scan(
		&course.ID, &course.Name, &course.Description, &chartIDs, &songLevels, &forceGameover, &createdAt, &updatedAt,
	); err != nil {
		return danCourse{}, fmt.Errorf("scan dan course: %w", err)
	}
	if err := json.Unmarshal([]byte(chartIDs), &course.ChartIDs); err != nil {
		return danCourse{}, fmt.Errorf("decode chart IDs for dan course %s: %w", course.ID, err)
	}
	if course.ChartIDs == nil {
		return danCourse{}, fmt.Errorf("dan course %s has no chart ID list", course.ID)
	}
	if err := json.Unmarshal([]byte(songLevels), &course.SongLevels); err != nil {
		return danCourse{}, fmt.Errorf("decode song levels for dan course %s: %w", course.ID, err)
	}
	var err error
	course.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return danCourse{}, fmt.Errorf("parse creation time for dan course %s: %w", course.ID, err)
	}
	course.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return danCourse{}, fmt.Errorf("parse update time for dan course %s: %w", course.ID, err)
	}
	course.IsForceGameover = forceGameover == 1
	return course, nil
}

func (app *application) createDanCourse(ctx context.Context, course danCourse) error {
	chartIDs, err := json.Marshal(course.ChartIDs)
	if err != nil {
		return fmt.Errorf("encode dan course chart IDs: %w", err)
	}
	songLevels, err := json.Marshal(course.SongLevels)
	if err != nil {
		return fmt.Errorf("encode dan course song levels: %w", err)
	}
	now := time.Now().UTC()
	course.CreatedAt = now
	course.UpdatedAt = now
	_, err = app.store.ExecContext(ctx, `
INSERT INTO dan_courses (id, name, description, chart_ids, song_levels, is_force_gameover, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		course.ID, course.Name, course.Description, string(chartIDs), string(songLevels), course.IsForceGameover,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("insert dan course: %w", err)
	}
	return nil
}

func (app *application) updateDanCourse(ctx context.Context, course danCourse) error {
	chartIDs, err := json.Marshal(course.ChartIDs)
	if err != nil {
		return fmt.Errorf("encode dan course chart IDs: %w", err)
	}
	songLevels, err := json.Marshal(course.SongLevels)
	if err != nil {
		return fmt.Errorf("encode dan course song levels: %w", err)
	}
	result, err := app.store.ExecContext(ctx, `
UPDATE dan_courses
SET name = ?, description = ?, chart_ids = ?, song_levels = ?, is_force_gameover = ?, updated_at = ?
WHERE id = ?`,
		course.Name, course.Description, string(chartIDs), string(songLevels), course.IsForceGameover,
		time.Now().UTC().Format(time.RFC3339Nano), course.ID)
	if err != nil {
		return fmt.Errorf("update dan course: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check dan course update: %w", err)
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (app *application) deleteDanCourse(ctx context.Context, id string) error {
	result, err := app.store.ExecContext(ctx, "DELETE FROM dan_courses WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete dan course: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check dan course deletion: %w", err)
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (app *application) serveGameCollections(collections []publicCollection) func(http.ResponseWriter, *http.Request, account) {
	return func(w http.ResponseWriter, r *http.Request, _ account) {
		w.Header().Set("Cache-Control", "no-store")
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
		courses, err := app.listDanCourses(r.Context())
		if err != nil {
			log.Printf("dan_course_list_error err=%v", err)
			writeAPIStatus(w, http.StatusInternalServerError, -114514, "courses could not be loaded")
			return
		}
		for _, course := range courses {
			if !danCourseHasValidLevels(course, app.catalog) {
				log.Printf("dan_course_levels_invalid course_id=%s", course.ID)
				continue
			}
			hashes := make([]string, 0, len(course.ChartIDs))
			valid := true
			for _, chartID := range course.ChartIDs {
				entry, exists := app.catalog[strings.ToLower(chartID)]
				if !exists || entry.chart.Hash == "" {
					log.Printf("dan_course_chart_missing course_id=%s chart_id=%s", course.ID, chartID)
					valid = false
					break
				}
				hashes = append(hashes, entry.chart.Hash)
			}
			if !valid || len(hashes) == 0 {
				continue
			}
			result = append(result, publicCollectionHashList{
				ID:              course.ID,
				Name:            course.Name,
				CreatedBy:       danCourseCreator,
				Description:     course.Description,
				SongHashs:       hashes,
				IsPlayList:      false,
				IsForceGameover: course.IsForceGameover,
				SongLevels:      course.SongLevels,
			})
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func danCourseHasValidLevels(course danCourse, catalog map[string]chartCatalogEntry) bool {
	if len(course.ChartIDs) == 0 || len(course.SongLevels) != len(course.ChartIDs) {
		return false
	}
	for i, chartID := range course.ChartIDs {
		entry, ok := catalog[strings.ToLower(chartID)]
		if !ok || !danChartHasDifficulty(entry.chart, course.SongLevels[i]) {
			return false
		}
	}
	return true
}

func danChartHasDifficulty(chart ParsedChart, difficulty int) bool {
	return difficulty >= 0 &&
		difficulty < len(chart.Levels) &&
		difficulty < 7 &&
		chart.Levels[difficulty] != nil &&
		strings.TrimSpace(*chart.Levels[difficulty]) != ""
}
