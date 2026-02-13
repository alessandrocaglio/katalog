package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	PollInterval string   `yaml:"poll_interval"`
	Targets      []Target `yaml:"targets"`
}

type Target struct {
	Name             string            `yaml:"name"`
	Paths            []string          `yaml:"paths"`
	ExcludePattern   string            `yaml:"exclude_pattern,omitempty"`
	MultilinePattern string            `yaml:"multiline_pattern,omitempty"`
	Fields           map[string]string `yaml:"fields,omitempty"`
}

func Load(path string) (Config, error) {
	yamlFile, err := os.ReadFile(path)
	var cfg Config
	if err != nil {
		return cfg, err
	}
	err = yaml.Unmarshal(yamlFile, &cfg)
	return cfg, err
}

func (c *Config) Validate() (time.Duration, error) {
	if c.PollInterval == "" {
		return 0, fmt.Errorf("poll_interval must be set")
	}
	pollDur, err := time.ParseDuration(c.PollInterval)
	if err != nil {
		return 0, fmt.Errorf("invalid poll_interval: %w", err)
	}
	if len(c.Targets) == 0 {
		return 0, fmt.Errorf("no targets configured")
	}
	return pollDur, nil
}
