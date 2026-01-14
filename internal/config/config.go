package config

import (
	"bufio"
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
	configDirName   = "boox-serve"
	configFileName  = "config.json"
	envFileName     = ".env"
)

type ProviderConfig struct {
	MangaDexAPIKey string `json:"mangadex_api_key,omitempty"`
}

type Config struct {
	BooxURL   string         `json:"boox_url"`
	BooxIP    string         `json:"boox_ip"`
	BooxPort  int            `json:"boox_port"`
	Verbose   bool           `json:"verbose"`
	Providers ProviderConfig `json:"providers,omitempty"`
}

func DefaultConfig() Config {
	return Config{
		BooxPort: defaultBooxPort,
	}
}

func ConfigDir() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("unable to resolve config dir: %w", err)
	}

	return filepath.Join(configDir, configDirName), nil
}

func ConfigPath() (string, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, configFileName), nil
}

func EnvPath() (string, error) {
	configDir, err := ConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, envFileName), nil
}

func LoadEnv() error {
	envPath, err := EnvPath()
	if err != nil {
		return err
	}

	file, err := os.Open(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("unable to read env file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}
		value = strings.Trim(value, "\"'")
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, value)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("unable to parse env file: %w", err)
	}

	return nil
}

func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	if err := LoadEnv(); err != nil {
		return cfg, err
	}

	configPath, err := ConfigPath()
	if err != nil {
		return cfg, err
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ApplyEnvDefaults(cfg), nil
		}
		return cfg, fmt.Errorf("unable to read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("unable to parse config: %w", err)
	}

	return ApplyEnvDefaults(cfg), nil
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

func ApplyEnvDefaults(cfg Config) Config {
	if cfg.BooxURL == "" {
		if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_URL")); value != "" {
			cfg.BooxURL = value
		}
	}
	if cfg.BooxIP == "" {
		if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_IP")); value != "" {
			cfg.BooxIP = value
		}
	}
	if cfg.BooxPort == 0 {
		if value := strings.TrimSpace(os.Getenv("BOOX_TABLET_PORT")); value != "" {
			if port, err := strconv.Atoi(value); err == nil {
				cfg.BooxPort = port
			}
		}
	}
	if !cfg.Verbose {
		if value := strings.TrimSpace(os.Getenv("BOOX_VERBOSE")); value != "" {
			cfg.Verbose = value == "1" || strings.EqualFold(value, "true")
		}
	}
	if cfg.Providers.MangaDexAPIKey == "" {
		if value := strings.TrimSpace(os.Getenv("BOOX_MANGADEX_API_KEY")); value != "" {
			cfg.Providers.MangaDexAPIKey = value
		}
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
