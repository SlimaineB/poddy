package region

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

type Config struct {
	Name        string  `json:"name" yaml:"name"`
	DisplayName string  `json:"displayName,omitempty" yaml:"displayName,omitempty"`
	URL         string  `json:"url" yaml:"url"`
	Key         string  `json:"key" yaml:"key"`
	SSHHost     string  `json:"sshHost,omitempty" yaml:"sshHost,omitempty"`
	SSHPort     string  `json:"sshPort,omitempty" yaml:"sshPort,omitempty"`
	Lat         float64 `json:"lat,omitempty" yaml:"lat,omitempty"`
	Lon         float64 `json:"lon,omitempty" yaml:"lon,omitempty"`
	Default     bool    `json:"default,omitempty" yaml:"default,omitempty"`
}

type File struct {
	Regions []Config `json:"regions" yaml:"regions"`
}

type Registry struct {
	regions     []Config
	defaultName string
}

func Load(path string) (*Registry, error) {
	if strings.TrimSpace(path) == "" {
		return &Registry{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(raw)
}

func Parse(raw []byte) (*Registry, error) {
	var file File
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse regions: %w", err)
	}
	seen := map[string]bool{}
	regions := make([]Config, 0, len(file.Regions))
	defaultName := ""
	for i, cfg := range file.Regions {
		cfg.Name = strings.TrimSpace(cfg.Name)
		cfg.URL = strings.TrimRight(strings.TrimSpace(cfg.URL), "/")
		cfg.DisplayName = strings.TrimSpace(cfg.DisplayName)
		cfg.SSHHost = strings.TrimSpace(cfg.SSHHost)
		cfg.SSHPort = strings.TrimSpace(cfg.SSHPort)
		cfg.Key = strings.TrimSpace(cfg.Key)
		if cfg.Lat < -90 || cfg.Lat > 90 {
			return nil, fmt.Errorf("region %q latitude must be between -90 and 90", cfg.Name)
		}
		if cfg.Lon < -180 || cfg.Lon > 180 {
			return nil, fmt.Errorf("region %q longitude must be between -180 and 180", cfg.Name)
		}
		if cfg.Name == "" {
			return nil, fmt.Errorf("regions[%d].name is required", i)
		}
		if cfg.URL == "" {
			return nil, fmt.Errorf("region %q url is required", cfg.Name)
		}
		if cfg.Key == "" {
			return nil, fmt.Errorf("region %q key is required", cfg.Name)
		}
		if seen[cfg.Name] {
			return nil, fmt.Errorf("duplicate region %q", cfg.Name)
		}
		seen[cfg.Name] = true
		if cfg.Default {
			if defaultName != "" {
				return nil, fmt.Errorf("multiple default regions")
			}
			defaultName = cfg.Name
		}
		regions = append(regions, cfg)
	}
	sort.Slice(regions, func(i, j int) bool {
		return strings.ToLower(regions[i].Name) < strings.ToLower(regions[j].Name)
	})
	if defaultName == "" && len(regions) > 0 {
		defaultName = regions[0].Name
	}
	return &Registry{regions: regions, defaultName: defaultName}, nil
}

func (r *Registry) All() []Config {
	if r == nil {
		return nil
	}
	return append([]Config(nil), r.regions...)
}

func (r *Registry) Default() (Config, bool) {
	if r == nil {
		return Config{}, false
	}
	for _, cfg := range r.regions {
		if cfg.Name == r.defaultName {
			return cfg, true
		}
	}
	return Config{}, false
}

func (r *Registry) Get(name string) (Config, bool) {
	if r == nil {
		return Config{}, false
	}
	for _, cfg := range r.regions {
		if cfg.Name == name {
			return cfg, true
		}
	}
	return Config{}, false
}

func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.regions))
	for _, cfg := range r.regions {
		out = append(out, cfg.Name)
	}
	return out
}
