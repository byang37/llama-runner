package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// EnvVar is a key/value environment variable.
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// AppSettings holds global application configuration.
type AppSettings struct {
	ServiceHost string   `json:"service_host"`
	ServicePort string   `json:"service_port"`
	ModelsDir   string   `json:"models_dir"`
	EnvVars     []EnvVar `json:"env_vars"`
}

func settingsFilePath() string {
	return filepath.Join(configDir, "app_settings.json")
}

func defaultAppSettings() AppSettings {
	return AppSettings{
		ServiceHost: "127.0.0.1",
		ServicePort: "8080",
		ModelsDir:   "",
		EnvVars:     loadEnvIni(),
	}
}

func loadAppSettings() AppSettings {
	data, err := os.ReadFile(settingsFilePath())
	if err != nil {
		return defaultAppSettings()
	}
	var s AppSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return defaultAppSettings()
	}
	if s.ServiceHost == "" {
		s.ServiceHost = "127.0.0.1"
	}
	if s.ServicePort == "" {
		s.ServicePort = "8080"
	}
	return s
}

func saveAppSettings(s AppSettings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsFilePath(), data, 0644)
}

// loadEnvIni reads configs/env.ini. Creates a template if missing.
func loadEnvIni() []EnvVar {
	iniPath := filepath.Join(configDir, "env.ini")

	if _, err := os.Stat(iniPath); os.IsNotExist(err) {
		defaultIni := `# llama-runner environment variables
# Format: KEY=VALUE  (lines starting with # are comments)
#
# ── AMD GPU (ROCm / HIP) ──────────────────────────────────────────
# HSA_OVERRIDE_GFX_VERSION=11.5.1
# HIP_VISIBLE_DEVICES=0
#
# ── NVIDIA GPU (CUDA) ─────────────────────────────────────────────
# CUDA_VISIBLE_DEVICES=0
#
# ── CPU / Threading ───────────────────────────────────────────────
# OMP_NUM_THREADS=8
`
		_ = os.WriteFile(iniPath, []byte(defaultIni), 0644)
		return nil
	}

	f, err := os.Open(iniPath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var vars []EnvVar
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		vars = append(vars, EnvVar{
			Key:   strings.TrimSpace(line[:idx]),
			Value: strings.TrimSpace(line[idx+1:]),
		})
	}
	return vars
}
