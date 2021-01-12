package ignition

import (
	"encoding/json"
	"io/ioutil"

	"github.com/coreos/ignition/v2/config/validate"

	config31 "github.com/coreos/ignition/v2/config/v3_1"
	config31types "github.com/coreos/ignition/v2/config/v3_1/types"

	"github.com/pkg/errors"
)

//go:generate mockgen -source=ignition.go -package=ignition -destination=mock_ignition.go
type Ignition interface {
	ParseIgnitionFile(path string) (*config31types.Config, error)
	WriteIgnitionFile(path string, config *config31types.Config) error
	MergeIgnitionConfig(base *config31types.Config, overrides *config31types.Config) (*config31types.Config, error)
}

type ignition struct{}

func NewIgnition() *ignition {
	return &ignition{}
}

// ParseIgnitionFile reads an ignition config from a given path on disk
func (i *ignition) ParseIgnitionFile(path string) (*config31types.Config, error) {
	configBytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, errors.Errorf("error reading file %s: %v", path, err)
	}
	config, _, err := config31.Parse(configBytes)
	if err != nil {
		return nil, errors.Errorf("error parsing ignition: %v", err)
	}
	return &config, nil
}

// WriteIgnitionFile writes an ignition config to a given path on disk
func (i *ignition) WriteIgnitionFile(path string, config *config31types.Config) error {
	updatedBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}
	err = ioutil.WriteFile(path, updatedBytes, 0600)
	if err != nil {
		return errors.Wrapf(err, "error writing file %s", path)
	}
	return nil
}

// MergeIgnitionConfig merges the specified configs and check the result is a valid Ignition config
func (i *ignition) MergeIgnitionConfig(base *config31types.Config, overrides *config31types.Config) (*config31types.Config, error) {
	config := config31.Merge(*base, *overrides)
	report := validate.ValidateWithContext(config, nil)
	if report.IsFatal() {
		return &config, errors.Errorf("merged ignition config is invalid: %s", report.String())
	}
	return &config, nil
}
