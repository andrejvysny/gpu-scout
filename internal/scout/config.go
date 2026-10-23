package scout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var serverPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
var channelPattern = regexp.MustCompile(`^[CG][A-Z0-9]{2,}$`)
var userPattern = regexp.MustCompile(`^[UW][A-Z0-9]{2,}$`)

type Config struct {
	Server         string `json:"server"`
	SlackChannel   string `json:"slack_channel"`
	StateDir       string `json:"state_dir,omitempty"`
	SlackPoll      string `json:"slack_poll_interval"`
	GPUPoll        string `json:"gpu_poll_interval"`
	IdlePeriod     string `json:"idle_period"`
	MaxUtilization uint32 `json:"max_utilization"`
	MaxMemoryMiB   uint64 `json:"max_memory_mib"`
}

func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	return filepath.Join(dir, "gpu-scout", "config.json"), err
}

func LoadConfig(path string) (Config, error) {
	c := Config{SlackPoll: "15s", GPUPoll: "30s", IdlePeriod: "2m", MaxUtilization: 5, MaxMemoryMiB: 512}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, fmt.Errorf("parse config: %w", err)
	}
	if c.StateDir == "" {
		c.StateDir, err = defaultStateDir()
		if err != nil {
			return c, err
		}
	}
	return c, c.validate()
}

func defaultStateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "gpu-scout"), nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "gpu-scout"), err
}

func (c Config) validate() error {
	if !serverPattern.MatchString(c.Server) || !channelPattern.MatchString(c.SlackChannel) {
		return errors.New("config requires a valid server name and Slack channel ID")
	}
	if !filepath.IsAbs(c.StateDir) {
		return errors.New("state_dir must be an absolute path")
	}
	for name, value := range map[string]string{"slack_poll_interval": c.SlackPoll, "gpu_poll_interval": c.GPUPoll, "idle_period": c.IdlePeriod} {
		d, err := time.ParseDuration(value)
		if err != nil || d < time.Second || d > 24*time.Hour {
			return fmt.Errorf("%s must be between 1s and 24h", name)
		}
	}
	if c.MaxUtilization > 100 || c.MaxMemoryMiB > ^uint64(0)/(1024*1024) {
		return errors.New("invalid utilization or memory threshold")
	}
	return nil
}

func duration(value string) time.Duration {
	d, _ := time.ParseDuration(value)
	return d
}
