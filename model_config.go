package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ModelConfig holds all parameters for one model entry in presets.ini.
//
// Identity:
//   ModelID   — preset section name; used with /models/load and /models/unload.
//   ModelPath — absolute path to the primary .gguf file. Written as
//               "model = <path>" only for subdirectory models.
//   MmprojPath — optional mmproj file path; written as "mmproj = <path>".
//
// Load-time parameters (written to presets.ini):
//   NGLLayers, CtxSize, NoMmap, MLock, KVOffload, UnifiedKV,
//   FlashAttn, BatchSize, UBatchSize, CacheTypeK, CacheTypeV, CacheReuse,
//   NParallel, Threads, ThreadsBatch, Seed, Jinja, ReasoningFormat,
//   ReasoningBudget, ExtraArgs
//
// Inference-time defaults (stored as UI hints only; sent per-request in body):
//   Temperature, TopP, TopK, MinP
type ModelConfig struct {
	ModelPath  string `json:"model_path"`
	MmprojPath string `json:"mmproj_path"`
	ModelID    string `json:"model_id"`  // immutable internal key (file stem)
	Alias      string `json:"alias"`     // user-visible section name; defaults to ModelID
	InSubdir   bool   `json:"in_subdir"`

	// Load-time (presets.ini)
	NGLLayers       string `json:"ngl"`
	CtxSize         string `json:"ctx_size"`
	NoMmap          bool   `json:"no_mmap"`
	MLock           bool   `json:"mlock"`
	KVOffload       bool   `json:"kv_offload"`   // offload KV cache to GPU (default on)
	UnifiedKV       bool   `json:"unified_kv"`   // unified KV cache across all slots
	FlashAttn       bool   `json:"flash_attn"`
	BatchSize       string `json:"batch_size"`
	UBatchSize      string `json:"ubatch_size"`
	CacheTypeK      string `json:"cache_type_k"`
	CacheTypeV      string `json:"cache_type_v"`
	CacheReuse      string `json:"cache_reuse"`
	NParallel       string `json:"n_parallel"`
	Threads         string `json:"threads"`
	ThreadsBatch    string `json:"threads_batch"`
	Seed            string `json:"seed"`
	Jinja           bool   `json:"jinja"`
	ReasoningFormat string `json:"reasoning_format"`
	ReasoningBudget string `json:"reasoning_budget"`
	ExtraArgs       string `json:"extra_args"`

	// Inference-time defaults (UI only)
	Temperature string `json:"temperature"`
	TopP        string `json:"top_p"`
	TopK        string `json:"top_k"`
	MinP        string `json:"min_p"`
}

// GGUFModel is a discovered model file with its current load status.
type GGUFModel struct {
	Path       string `json:"path"`
	MmprojPath string `json:"mmproj_path"`
	ModelID    string `json:"model_id"`
	Alias      string `json:"alias"`    // from saved config; empty = use model_id
	Name       string `json:"name"`
	SizeMB     int64  `json:"size_mb"`
	Status     string `json:"status"`
	InSubdir   bool   `json:"in_subdir"`
}

var modelConfigsDir string

func initModelConfigsDir() {
	modelConfigsDir = filepath.Join(configDir, "model_params")
	_ = os.MkdirAll(modelConfigsDir, 0755)
}

var reUnsafe = regexp.MustCompile(`[^\w\-]+`)

func configFileForID(modelID string) string {
	safe := reUnsafe.ReplaceAllString(strings.ReplaceAll(modelID, "/", "__"), "_")
	if safe == "" {
		safe = "model"
	}
	return filepath.Join(modelConfigsDir, safe+".json")
}

func defaultModelConfig(modelPath, mmprojPath, modelID string, inSubdir bool) ModelConfig {
	return ModelConfig{
		ModelPath:       modelPath,
		MmprojPath:      mmprojPath,
		ModelID:         modelID,
		InSubdir:        inSubdir,
		NGLLayers:       "999",
		CtxSize:         "32768",
		NoMmap:          true,
		MLock:           true,
		KVOffload:       true,
		UnifiedKV:       true,
		FlashAttn:       true,
		BatchSize:       "512",
		UBatchSize:      "512",
		CacheReuse:      "256",
		NParallel:       "1",
		Jinja:           true,
		ReasoningFormat: "deepseek",
		ReasoningBudget: "-1",
		Temperature:     "0.7",
		TopP:            "0.8",
		TopK:            "20",
		MinP:            "0.0",
	}
}

func loadModelConfig(modelPath, mmprojPath, modelID string, inSubdir bool) ModelConfig {
	data, err := os.ReadFile(configFileForID(modelID))
	if err != nil {
		return defaultModelConfig(modelPath, mmprojPath, modelID, inSubdir)
	}
	var c ModelConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return defaultModelConfig(modelPath, mmprojPath, modelID, inSubdir)
	}
	c.ModelPath = modelPath
	c.MmprojPath = mmprojPath
	c.InSubdir = inSubdir
	return c
}

func saveModelConfig(c ModelConfig) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFileForID(c.ModelID), data, 0644)
}

func presetsINIPath() string {
	return filepath.Join(configDir, "presets.ini")
}

// writePresetsINI scans the live models directory and regenerates presets.ini
// from the merged state of discovered models and any saved per-model configs.
// This is the authoritative way to rebuild the file; it works even when no
// configs have been explicitly saved by the user yet.
func writePresetsINI() (string, error) {
	st := loadAppSettings()
	if st.ModelsDir == "" {
		return "", nil
	}

	// Discover all models on disk (status map is empty — we only care about paths).
	discovered, err := scanModels(st.ModelsDir, nil)
	if err != nil {
		return "", fmt.Errorf("scan models: %w", err)
	}

	// Build config list: load saved config or generate defaults for each model.
	configs := make([]ModelConfig, 0, len(discovered))
	for _, m := range discovered {
		cfg := loadModelConfig(m.Path, m.MmprojPath, m.ModelID, m.InSubdir)
		configs = append(configs, cfg)
	}

	content := generatePresetsINI(configs)
	path := presetsINIPath()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

// generatePresetsINI converts a list of ModelConfigs into a presets.ini string.
// Format matches what llama-server expects for --models-preset.
func generatePresetsINI(configs []ModelConfig) string {
	var sb strings.Builder
	sb.WriteString("# presets.ini — generated by LLaMA Runner\n")
	sb.WriteString("# Section name = model ID used with /models/load and /models/unload.\n\n")

	iniStr := func(key, val string) {
		if val != "" {
			fmt.Fprintf(&sb, "%s = %s\n", key, val)
		}
	}
	iniBool := func(key string, val bool) {
		if val {
			fmt.Fprintf(&sb, "%s = 1\n", key)
		}
	}

	for _, c := range configs {
		if c.ModelID == "" {
			continue
		}
		// Section name = alias when set, otherwise model ID.
		sectionName := c.Alias
		if sectionName == "" {
			sectionName = c.ModelID
		}
		fmt.Fprintf(&sb, "[%s]\n", sectionName)

		if c.InSubdir && c.ModelPath != "" {
			fmt.Fprintf(&sb, "model = %s\n", c.ModelPath)
		}
		if c.MmprojPath != "" {
			fmt.Fprintf(&sb, "mmproj = %s\n", c.MmprojPath)
		}

		iniStr("n-gpu-layers", c.NGLLayers)
		iniStr("ctx-size", c.CtxSize)
		iniStr("batch-size", c.BatchSize)
		iniStr("ubatch-size", c.UBatchSize)
		iniStr("cache-type-k", c.CacheTypeK)
		iniStr("cache-type-v", c.CacheTypeV)
		iniStr("cache-reuse", c.CacheReuse)
		iniStr("parallel", c.NParallel)
		iniStr("threads", c.Threads)
		iniStr("threads-batch", c.ThreadsBatch)
		iniStr("seed", c.Seed)
		iniStr("reasoning-format", c.ReasoningFormat)
		iniStr("reasoning-budget", c.ReasoningBudget)

		// mmap is ON by default; write "mmap = false" to disable.
		if c.NoMmap {
			sb.WriteString("mmap = false\n")
		}
		iniBool("mlock", c.MLock)
		// kv-offload is ON by default; write "kv-offload = false" to disable.
		if !c.KVOffload {
			sb.WriteString("kv-offload = false\n")
		}
		iniBool("kv-unified", c.UnifiedKV)
		iniBool("flash-attn", c.FlashAttn)
		iniBool("jinja", c.Jinja)

		if c.ExtraArgs != "" {
			fmt.Fprintf(&sb, "# extra-args: %s\n", c.ExtraArgs)
		}

		// Inference-time sampling defaults (also written to preset so they
		// apply as model-level defaults for requests that omit these fields).
		iniStr("temperature", c.Temperature)
		iniStr("top-p", c.TopP)
		iniStr("top-k", c.TopK)
		iniStr("min-p", c.MinP)

		sb.WriteString("\n")
	}
	return sb.String()
}

// ─── Model scanning ───────────────────────────────────────────────────────────

var reShardSuffix = regexp.MustCompile(`-(\d{5})-of-(\d{5})$`)

// scanModels recursively discovers all .gguf models under dir.
// mmproj-*.gguf files are associated with the primary model in the same directory.
// Multi-shard sets are collapsed: only the first shard is shown.
// Subdirectory models have InSubdir=true and need an explicit model= path in presets.ini.
func scanModels(dir string, loadedIDs map[string]bool) ([]GGUFModel, error) {
	if dir == "" {
		return []GGUFModel{}, nil
	}
	var models []GGUFModel
	seen := make(map[string]bool)
	if err := walkDir(dir, dir, loadedIDs, &models, seen); err != nil {
		return nil, err
	}
	return models, nil
}

func walkDir(rootDir, subDir string, loadedIDs map[string]bool, models *[]GGUFModel, seen map[string]bool) error {
	entries, err := os.ReadDir(subDir)
	if err != nil {
		return err
	}

	inSubdir := subDir != rootDir

	type ggufFile struct {
		name     string
		fullPath string
		sizeMB   int64
		isMmproj bool
	}
	var files []ggufFile

	for _, e := range entries {
		if e.IsDir() {
			_ = walkDir(rootDir, filepath.Join(subDir, e.Name()), loadedIDs, models, seen)
			continue
		}
		name := e.Name()
		lower := strings.ToLower(name)
		if !strings.HasSuffix(lower, ".gguf") {
			continue
		}
		info, _ := e.Info()
		sz := int64(0)
		if info != nil {
			sz = info.Size() / 1024 / 1024
		}
		files = append(files, ggufFile{
			name:     name,
			fullPath: filepath.Join(subDir, name),
			sizeMB:   sz,
			isMmproj: strings.HasPrefix(lower, "mmproj"),
		})
	}

	mmprojPath := ""
	for _, f := range files {
		if f.isMmproj {
			mmprojPath = f.fullPath
			break
		}
	}

	dirSeen := make(map[string]bool)
	for _, f := range files {
		if f.isMmproj {
			continue
		}
		stem := f.name[:len(f.name)-5]
		if m := reShardSuffix.FindStringSubmatch(stem); m != nil {
			if m[1] != "00001" {
				continue
			}
			stem = stem[:len(stem)-len(m[0])]
		}
		if dirSeen[stem] || seen[f.fullPath] {
			continue
		}
		dirSeen[stem] = true
		seen[f.fullPath] = true

		cfg := loadModelConfig(f.fullPath, mmprojPath, stem, inSubdir)
		modelID := cfg.ModelID
		if modelID == "" {
			modelID = stem
		}
		alias := cfg.Alias
		if alias == "" {
			alias = modelID
		}
		status := "unloaded"
		if loadedIDs != nil {
			if loadedIDs[alias] || loadedIDs[modelID] || loadedIDs[stem] {
				status = "loaded"
			}
		}
		*models = append(*models, GGUFModel{
			Path: f.fullPath, MmprojPath: mmprojPath,
			ModelID: modelID, Alias: alias, Name: stem,
			SizeMB: f.sizeMB, Status: status, InSubdir: inSubdir,
		})
	}
	return nil
}
