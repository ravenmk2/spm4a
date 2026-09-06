package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// idlePolicy controls the §3.4 idle exit. Immediate is the current behavior
// (a short grace delay to flush the final response); never disables it; a
// duration delays the exit until the condition has held that long.
type idlePolicy struct {
	never bool
	delay time.Duration
}

// daemonFileConfig mirrors <SPM4A_HOME>/config.yaml (kebab-case fields).
type daemonFileConfig struct {
	IdleExit string `yaml:"idle-exit"`
}

func loadIdlePolicy(home string, log *slog.Logger) idlePolicy {
	def := idlePolicy{delay: idleGrace}
	data, err := os.ReadFile(filepath.Join(home, "config.yaml"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Warn("read config.yaml failed; using immediate idle-exit", "err", err)
		}
		return def
	}
	var cfg daemonFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Warn("parse config.yaml failed; using immediate idle-exit", "err", err)
		return def
	}
	pol, err := parseIdleExit(cfg.IdleExit)
	if err != nil {
		log.Warn("invalid idle-exit value; using immediate", "value", cfg.IdleExit, "err", err)
		return def
	}
	return pol
}

func parseIdleExit(raw string) (idlePolicy, error) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); {
	case v == "" || v == "immediate":
		return idlePolicy{delay: idleGrace}, nil
	case v == "never":
		return idlePolicy{never: true}, nil
	default:
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return idlePolicy{}, fmt.Errorf("idle-exit %q: want immediate|never|<duration>", raw)
		}
		return idlePolicy{delay: d}, nil
	}
}
