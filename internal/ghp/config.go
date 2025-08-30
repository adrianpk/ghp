package ghp

import (
	"os"

	"gopkg.in/yaml.v3"
)

type App struct {
	PromptPath       string `yaml:"prompt_path"`
	OutDir           string `yaml:"out_dir"`
	ReposLimit       int    `yaml:"repos_limit"`
	ChunksPerRepo    int    `yaml:"chunks_per_repo"`
	MaxChunkBytes    int    `yaml:"max_chunk_bytes"`
	IncludePinned    bool   `yaml:"include_pinned"`
	IncludeNonPinned bool   `yaml:"include_non_pinned"`
	ExcludeForks     bool   `yaml:"exclude_forks"`
}

type Auth struct {
	GithubToken string `yaml:"github_token"`
}

type LLM struct {
	Provider          string  `yaml:"provider"`
	Model             string  `yaml:"model"`
	APIKey            string  `yaml:"api_key"`
	Endpoint          string  `yaml:"endpoint"`
	MaxTokens         int     `yaml:"max_tokens"`
	Temperature       float32 `yaml:"temperature"`
	RequestsPerMinute int     `yaml:"requests_per_minute"`
	ParallelRequests  int     `yaml:"parallel_requests"`
}

type Config struct {
	App  App  `yaml:"app"`
	Auth Auth `yaml:"auth"`
	LLM  LLM  `yaml:"llm"`
}

func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}

	if c.LLM.ParallelRequests <= 0 {
		c.LLM.ParallelRequests = 4
	}

	if c.LLM.RequestsPerMinute <= 0 {
		c.LLM.RequestsPerMinute = 60
	}

	if c.Auth.GithubToken == "" {
		c.Auth.GithubToken = os.Getenv("GITHUB_TOKEN")
	}

	return &c, nil
}
