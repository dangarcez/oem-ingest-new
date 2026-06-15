package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// WriteTargets writes configTargets.yaml in the simplified official format.
func WriteTargets(path string, sites []SiteConfig) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("caminho de saida da configuracao validada nao informado")
	}
	data, err := MarshalTargets(sites)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("criar diretorio da configuracao validada %q: %w", dir, err)
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("escrever configuracao validada %q: %w", path, err)
	}
	return nil
}

// MarshalTargets renders target configuration without reintroducing the nested
// legacy "site:" object format.
func MarshalTargets(sites []SiteConfig) ([]byte, error) {
	out := make([]targetSiteYAML, len(sites))
	for i, site := range sites {
		out[i] = targetSiteYAML{
			Name:     site.Name,
			Site:     nilString(site.Site),
			Endpoint: site.Endpoint,
			Targets:  site.Targets,
		}
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("serializar configuracao validada: %w", err)
	}
	return data, nil
}

type targetSiteYAML struct {
	Name     string         `yaml:"name"`
	Site     any            `yaml:"site"`
	Endpoint string         `yaml:"endpoint"`
	Targets  []TargetConfig `yaml:"targets"`
}

func nilString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
