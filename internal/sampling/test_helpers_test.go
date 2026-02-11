package sampling

import (
	"os"

	"gopkg.in/yaml.v3"
)

// newFromLegacy creates a Sampler from a legacy FileConfig by auto-converting to ProcessingConfig.
// This is a test helper to ease migration of old tests.
func newFromLegacy(cfg FileConfig) (*Sampler, error) {
	pcfg := convertLegacyConfig(cfg)
	return NewFromProcessing(pcfg)
}

// reloadFromLegacy atomically replaces the processing rules from a legacy FileConfig.
// This is a test helper to ease migration of old tests.
func reloadFromLegacy(s *Sampler, cfg FileConfig) error {
	pcfg := convertLegacyConfig(cfg)
	return s.ReloadProcessingConfig(pcfg)
}

// parseFileConfig parses a legacy FileConfig from YAML bytes.
// This is a test helper replacing the removed Parse() function.
func parseFileConfig(data []byte) (FileConfig, error) {
	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// loadFileConfig loads a legacy FileConfig from a YAML file.
// This is a test helper replacing the removed LoadFile() function.
func loadFileConfig(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, err
	}
	return parseFileConfig(data)
}
