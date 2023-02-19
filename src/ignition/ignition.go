package ignition

import (
	"encoding/json"
	"os"

	ignitionConfigPrevVersion "github.com/coreos/ignition/v2/config/v3_1"
	ignitionConfig "github.com/coreos/ignition/v2/config/v3_2"
	"github.com/coreos/ignition/v2/config/v3_2/translate"
	"github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/coreos/ignition/v2/config/validate"
	"github.com/pkg/errors"
)

// Added to manage ignition versions in one file
var EmptyIgnitionConfig = types.IgnitionConfig{}
var EmptyIgnition = types.Config{}

//go:generate mockgen -source=ignition.go -package=ignition -destination=mock_ignition.go
type Ignition interface {
	ParseIgnitionFile(path string) (*types.Config, error)
	WriteIgnitionFile(path string, config *types.Config) error
	MergeIgnitionConfig(base *types.Config, overrides *types.Config) (*types.Config, error)
}

type ignition struct{}

func NewIgnition() *ignition {
	return &ignition{}
}

// ParseIgnitionFile reads an ignition config from a given path on disk
func (i *ignition) ParseIgnitionFile(path string) (*types.Config, error) {
	configBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrapf(err, "error reading file %s", path)
	}
	config, err := i.parseToLatest(configBytes)
	if err != nil {
		return nil, err
	}
	return config, nil
}

// ParseIgnitionFile reads an ignition config from a given path on disk
func (i *ignition) parseToLatest(content []byte) (*types.Config, error) {
	configLatest, _, err := ignitionConfig.Parse(content)
	if err != nil {
		configvPrev, _, err := ignitionConfigPrevVersion.Parse(content)
		if err != nil {
			return nil, errors.Wrap(err, "error parsing ignition")
		}
		configLatest = translate.Translate(configvPrev)
	}

	return &configLatest, nil
}

// WriteIgnitionFile writes an ignition config to a given path on disk
func (i *ignition) WriteIgnitionFile(path string, config *types.Config) error {
	updatedBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}
	err = os.WriteFile(path, updatedBytes, 0600)
	if err != nil {
		return errors.Wrapf(err, "error writing file %s", path)
	}
	return nil
}

// MergeIgnitionConfig merges the specified configs and check the result is a valid Ignition config
func (i *ignition) MergeIgnitionConfig(base *types.Config, overrides *types.Config) (*types.Config, error) {
	config := ignitionConfig.Merge(*base, *overrides)
	report := validate.ValidateWithContext(config, nil)
	if report.IsFatal() {
		return &config, errors.Errorf("merged ignition config is invalid: %s", report.String())
	}
	return &config, nil
}
