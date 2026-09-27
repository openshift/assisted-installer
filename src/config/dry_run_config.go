package config

import (
	"encoding/json"
	"os"
)

// DryRunConfig defines configuration of the agent's dry-run mode
type DryRunConfig struct {
	DryRunEnabled          bool   `envconfig:"DRY_ENABLE"`
	FakeRebootMarkerPath   string `envconfig:"DRY_FAKE_REBOOT_MARKER_PATH"`
	ForcedHostID           string `envconfig:"DRY_HOST_ID"`
	DryRunClusterHostsPath string `envconfig:"DRY_CLUSTER_HOSTS_PATH"`
	// DryRunClusterHostsPath gets read parsed into ParsedClusterHosts by DryParseClusterHosts
	ParsedClusterHosts DryClusterHosts
}

var DefaultDryRunConfig = DryRunConfig{
	DryRunEnabled:          false,
	FakeRebootMarkerPath:   "",
	ForcedHostID:           "",
	DryRunClusterHostsPath: "",
}

type DryClusterHost struct {
	// The hostname is used to link between the cluster node to the inventory host
	Hostname string `json:"hostname"`
	// The IP is used to fake MCS logs for the host pulling ignition
	Ip string `json:"ip"`
	// The reboot marker path is used to allow the bootstrap node to track which node completed
	// reboot and which still haven't
	RebootMarkerPath string `json:"rebootMarkerPath"`
}

// An array containing information about all hosts in the cluster. This is used
// for tracking when cluster hosts complete reboot so their status can be mocked
// by the controller / bootstrap host accordingly
type DryClusterHosts []DryClusterHost

// DryParseClusterHosts parses the JSON hosts file string into a DryClusterHosts
func DryParseClusterHosts(clusterHostsJsonPath string, parsedClusterHosts *DryClusterHosts) error {
	if clusterHostsJsonPath == "" {
		return nil
	}

	clusterHostsJson, err := os.ReadFile(clusterHostsJsonPath)
	if err != nil {
		return err
	}

	err = json.Unmarshal(clusterHostsJson, parsedClusterHosts)
	if err != nil {
		return err
	}

	return nil
}
