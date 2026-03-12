package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type ModelProvider struct {
	Name               string `yaml:"name"`
	BaseURL            string `yaml:"base_url"`
	WireAPI            string `yaml:"wire_api"`
	RequiresOpenAIAuth bool   `yaml:"requires_openai_auth"`
}

type ServerConfig struct {
	Address string `yaml:"address"`
}

type BlogConfig struct {
	Root string `yaml:"root"`
}

type RAGConfig struct {
	Enabled      bool `yaml:"enabled"`
	ChunkSize    int  `yaml:"chunk_size"`
	ChunkOverlap int  `yaml:"chunk_overlap"`
	TopK         int  `yaml:"top_k"`
}

type Config struct {
	ModelProvider          string                   `yaml:"model_provider"`
	Model                  string                   `yaml:"model"`
	NetworkAccess          string                   `yaml:"network_access"`
	DisableResponseStorage bool                     `yaml:"disable_response_storage"`
	ModelVerbosity         string                   `yaml:"model_verbosity"`
	ModelProviders         map[string]ModelProvider `yaml:"model_providers"`
	Server                 ServerConfig             `yaml:"server"`
	Blog                   BlogConfig               `yaml:"blog"`
	RAG                    RAGConfig                `yaml:"rag"`
}

func Default() Config {
	return Config{
		ModelProvider: "su8",
		Model:         "gpt-5.2",
		ModelProviders: map[string]ModelProvider{
			"su8": {
				Name:               "su8",
				BaseURL:            "https://www.su8.codes/codex/v1",
				WireAPI:            "responses",
				RequiresOpenAIAuth: true,
			},
		},
		Server: ServerConfig{
			Address: ":8080",
		},
		Blog: BlogConfig{
			Root: "./blog",
		},
		RAG: RAGConfig{
			Enabled:      true,
			ChunkSize:    900,
			ChunkOverlap: 120,
			TopK:         5,
		},
	}
}

func Load(configPath string) (*Config, error) {
	if err := LoadDotEnv(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	cfg := Default()
	bytes, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			cfg.applyDefaults()
			return &cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(bytes, &cfg); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.ModelProvider == "" {
		c.ModelProvider = def.ModelProvider
	}
	if c.Model == "" {
		c.Model = def.Model
	}
	if c.Server.Address == "" {
		c.Server.Address = def.Server.Address
	}
	if c.Blog.Root == "" {
		c.Blog.Root = def.Blog.Root
	}
	if c.RAG.ChunkSize <= 0 {
		c.RAG.ChunkSize = def.RAG.ChunkSize
	}
	if c.RAG.ChunkOverlap < 0 {
		c.RAG.ChunkOverlap = def.RAG.ChunkOverlap
	}
	if c.RAG.TopK <= 0 {
		c.RAG.TopK = def.RAG.TopK
	}
	if c.ModelProviders == nil {
		c.ModelProviders = def.ModelProviders
	}
}

func (c *Config) ActiveProvider() (ModelProvider, error) {
	provider, ok := c.ModelProviders[c.ModelProvider]
	if !ok {
		return ModelProvider{}, fmt.Errorf("model provider %q is not configured", c.ModelProvider)
	}
	provider.BaseURL = strings.TrimRight(provider.BaseURL, "/")
	return provider, nil
}

func (c *Config) APIKey() string {
	return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
}

func LoadDotEnv(path string) error {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		val = strings.Trim(val, `"`)
		if key != "" {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}
