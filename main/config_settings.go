package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultBooxPort = 8085
	configDirName   = "boox-uploader"
	configFileName  = "config.json"
)

type Config struct {
	BooxURL  string `json:"boox_url"`
	BooxIP   string `json:"boox_ip"`
	BooxPort int    `json:"boox_port"`
	Verbose  bool   `json:"verbose"`
}

func DefaultConfig() Config {
	return Config{
		BooxPort: defaultBooxPort,
	}
}

func ConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("unable to resolve config dir: %w", err)
	}

	return filepath.Join(configDir, configDirName, configFileName), nil
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	configPath, err := ConfigPath()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ApplyEnvOverrides(cfg), nil
		}
		return cfg, fmt.Errorf("unable to read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("unable to parse config: %w", err)
	}

	return ApplyEnvOverrides(cfg), nil
}

func SaveConfig(cfg Config) error {
	configPath, err := ConfigPath()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("unable to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to marshal config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0o644); err != nil {
		return fmt.Errorf("unable to write config: %w", err)
	}

	return nil
}

func ApplyEnvOverrides(cfg Config) Config {
	if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_URL")); value != "" {
		cfg.BooxURL = value
	}
	if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_IP")); value != "" {
		cfg.BooxIP = value
	}
	if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_PORT")); value != "" {
		if port, err := strconv.Atoi(value); err == nil {
			cfg.BooxPort = port
		}
	}
	if value := strings.TrimSpace(os.Getenv("BOOX_VERBOSE")); value != "" {
		cfg.Verbose = value == "1" || strings.EqualFold(value, "true")
	}

	return cfg
}

func (cfg Config) BaseURL() (string, error) {
	if cfg.BooxURL != "" {
		return normalizeURL(cfg.BooxURL, cfg.BooxPort)
	}
	if cfg.BooxIP != "" {
		return normalizeURL(cfg.BooxIP, cfg.BooxPort)
	}

	return "", errors.New("boox url not configured")
}

func normalizeURL(rawURL string, port int) (string, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return "", errors.New("boox url is empty")
	}
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		trimmed = "http://" + trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid boox url: %w", err)
	}

	if parsed.Host == "" {
		return "", errors.New("boox url missing host")
	}

	if port > 0 {
		if _, _, err := net.SplitHostPort(parsed.Host); err != nil {
			parsed.Host = net.JoinHostPort(parsed.Host, strconv.Itoa(port))
		}
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")

	return parsed.String(), nil
}

func (cfg Config) Validate() error {
	_, err := cfg.BaseURL()
	return err
}
