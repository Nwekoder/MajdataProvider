package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type serverConfig struct {
	InviteCode    string `json:"invite_code"`
	DanAdminToken string `json:"dan_admin_token"`
	DBPath        string `json:"db_path"`
}

func loadServerConfig(path string) (serverConfig, error) {
	config := serverConfig{DBPath: "data/majdata.sqlite"}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return serverConfig{}, fmt.Errorf("read server config: %w", err)
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return serverConfig{}, fmt.Errorf("decode server config: %w", err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			if err == nil {
				return serverConfig{}, fmt.Errorf("decode server config: multiple JSON values")
			}
			return serverConfig{}, fmt.Errorf("decode server config: %w", err)
		}
		if strings.TrimSpace(config.DBPath) == "" {
			config.DBPath = "data/majdata.sqlite"
		}
	}

	if value, ok := os.LookupEnv("MAJDATA_INVITE_CODE"); ok && value != "" {
		config.InviteCode = value
	}
	if value, ok := os.LookupEnv("MAJDATA_DAN_ADMIN_TOKEN"); ok && value != "" {
		config.DanAdminToken = value
	}
	if value, ok := os.LookupEnv("MAJDATA_DB_PATH"); ok && value != "" {
		config.DBPath = value
	}
	return config, nil
}
