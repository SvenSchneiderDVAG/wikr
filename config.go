package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Language   string `json:"language"`
	MaxResults int    `json:"max_results"`
	Source     string `json:"source"`
}

func defaultConfig() Config {
	return Config{Language: "en", MaxResults: 5, Source: "wikipedia"}
}

func getConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", wrapLocalizedError(msgErrConfigDirNotFound, err)
	}
	wikrConfigDir := filepath.Join(configDir, "wikr")
	if err := os.MkdirAll(wikrConfigDir, 0755); err != nil {
		return "", wrapLocalizedError(msgErrConfigDirCreate, err)
	}
	return filepath.Join(wikrConfigDir, "config.json"), nil
}

func loadConfig() (Config, bool, error) {
	corrected := false
	config := defaultConfig()
	configPath, err := getConfigPath()
	if err != nil {
		return config, false, err
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := saveConfig(config); err != nil {
			return config, false, wrapLocalizedError(msgErrDefaultConfigCreate, err)
		}
		return config, false, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return config, false, wrapLocalizedError(msgErrConfigRead, err, configPath)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return config, false, wrapLocalizedError(msgErrConfigDecode, err)
	}

	if config.Language != "en" && config.Language != "de" {
		config.Language = "en"
		corrected = true
	}
	if config.MaxResults <= 0 {
		config.MaxResults = 5
		corrected = true
	}
	if config.Source != "wikipedia" && config.Source != "grokipedia" {
		config.Source = "wikipedia"
		corrected = true
	}

	return config, corrected, nil
}

func saveConfig(config Config) error {
	configPath, err := getConfigPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return wrapLocalizedError(msgErrConfigEncode, err)
	}
	if err := writeFileAtomic(configPath, data, 0644); err != nil {
		return wrapLocalizedError(msgErrConfigWrite, err, configPath)
	}
	return nil
}
