package config

import (
	"encoding/json"
	"flag"
	"os"

	"fmt"

	"github.com/kelseyhightower/envconfig"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
)

type Config struct {
	DryRunConfig
	Role                        string
	ClusterID                   string
	InfraEnvID                  string
	HostID                      string
	Device                      string
	URL                         string
	Verbose                     bool
	OpenshiftVersion            string
	MCOImage                    string
	ControllerImage             string
	AgentImage                  string
	PullSecretToken             string `secret:"true"`
	SkipCertVerification        bool
	CACertPath                  string
	HTTPProxy                   string
	HTTPSProxy                  string
	NoProxy                     string
	ServiceIPs                  string
	InstallerArgs               []string
	HighAvailabilityMode        string
	CheckClusterVersion         bool
	MustGatherImage             string
	DisksToFormat               ArrayFlags
	SkipInstallationDiskCleanup bool
	EnableSkipMcoReboot         bool
}

func printHelpAndExit(err error) {
	if err != nil {
		println(err)
	}
	flag.CommandLine.Usage()
	os.Exit(0)
}

func (c *Config) ProcessArgs(args []string) {
	flagSet := flag.NewFlagSet("flagset", flag.ExitOnError)

	flagSet.StringVar(&c.Role, "role", string(models.HostRoleMaster), "The node role")
	flagSet.StringVar(&c.ClusterID, "cluster-id", "", "The cluster id")
	flagSet.StringVar(&c.InfraEnvID, "infra-env-id", "", "This host infra env id")
	flagSet.StringVar(&c.HostID, "host-id", "", "This host id")
	flagSet.StringVar(&c.Device, "boot-device", "", "The boot device")
	flagSet.StringVar(&c.URL, "url", "", "The BM inventory URL, including a scheme and optionally a port (overrides the host and port arguments")
	flagSet.StringVar(&c.OpenshiftVersion, "openshift-version", "4.4", "Openshift version to install")
	flagSet.StringVar(&c.MCOImage, "mco-image", "", "MCO image to install")
	flagSet.BoolVar(&c.Verbose, "verbose", false, "Increase verbosity, set log level to debug")
	flagSet.StringVar(&c.ControllerImage, "controller-image", "quay.io/ocpmetal/assisted-installer-controller:latest",
		"Assisted Installer Controller image URL")
	flagSet.StringVar(&c.AgentImage, "agent-image", "quay.io/ocpmetal/assisted-installer-agent:latest",
		"Assisted Installer Agent image URL that will be used to send logs on successful installation")
	flagSet.BoolVar(&c.SkipCertVerification, "insecure", false, "Do not validate TLS certificate")
	flagSet.StringVar(&c.CACertPath, "cacert", "", "Path to custom CA certificate in PEM format")
	flagSet.StringVar(&c.HTTPProxy, "http-proxy", "", "A proxy URL to use for creating HTTP connections outside the cluster")
	flagSet.StringVar(&c.HTTPSProxy, "https-proxy", "", "A proxy URL to use for creating HTTPS connections outside the cluster")
	flagSet.StringVar(&c.NoProxy, "no-proxy", "", "A comma-separated list of destination domain names, domains, IP addresses, or other network CIDRs to exclude proxying")
	flagSet.StringVar(&c.ServiceIPs, "service-ips", "", "All IPs of assisted service node")
	flagSet.StringVar(&c.HighAvailabilityMode, "high-availability-mode", "", "high-availability expectations, \"Full\" which represents the behavior in a \"normal\" cluster. Use 'None' for single-node deployment. Leave this value as \"\" for workers as we do not care about HA mode for workers.")
	flagSet.BoolVar(&c.CheckClusterVersion, "check-cluster-version", false, "Do not monitor CVO")
	flagSet.StringVar(&c.MustGatherImage, "must-gather-image", "", "Custom must-gather image")
	flagSet.Var(&c.DisksToFormat, "format-disk", "Disk to format. Can be specified multiple times")
	flagSet.BoolVar(&c.SkipInstallationDiskCleanup, "skip-installation-disk-cleanup", false, "Skip installation disk cleanup gives disk management to coreos-installer in case needed")
	flagSet.BoolVar(&c.EnableSkipMcoReboot, "enable-skip-mco-reboot", false, "indicate assisted installer to generate settings to match MCO requirements for skipping reboot after firstboot")

	var installerArgs string
	flagSet.StringVar(&installerArgs, "installer-args", "", "JSON array of additional coreos-installer arguments")
	h := flagSet.Bool("help", false, "Help message")

	// Add dry-run specific flag bindings.
	err := envconfig.Process("dryconfig", &DefaultDryRunConfig)
	if err != nil {
		fmt.Printf("envconfig error: %v", err)
		os.Exit(1)
	}

	flagSet.BoolVar(&c.DryRunEnabled, "dry-run", DefaultDryRunConfig.DryRunEnabled, "Dry run avoids/fakes certain actions while communicating with the service")
	flagSet.StringVar(&c.ForcedHostID, "force-id", DefaultDryRunConfig.ForcedHostID, "The fake host ID to give to the host")
	flagSet.StringVar(&c.FakeRebootMarkerPath, "fake-reboot-marker-path", DefaultDryRunConfig.FakeRebootMarkerPath, "A path whose existence indicates a fake reboot happened")
	flagSet.StringVar(&c.DryRunClusterHostsPath, "dry-run-cluster-hosts-path", DefaultDryRunConfig.DryRunClusterHostsPath, "A path to a JSON file with information about hosts in the cluster")

	err = flagSet.Parse(args)
	if err != nil {
		printHelpAndExit(err)
	}

	//Process dry-run arguments if necessary.
	if c.DryRunEnabled {
		if parseErr := DryParseClusterHosts(c.DryRunClusterHostsPath, &c.ParsedClusterHosts); parseErr != nil {
			fmt.Printf("Error parsing cluster hosts: %v", parseErr)
			os.Exit(1)
		}
	}

	if err := c.SetInstallerArgs(installerArgs); err != nil {
		printHelpAndExit(err)
	}

	if h != nil && *h {
		printHelpAndExit(nil)
	}

	if c.NoProxy != "" {
		utils.SetNoProxyEnv(c.NoProxy)
	}

	c.SetDefaults()
}

func (c *Config) SetInstallerArgs(installerArgs string) error {
	if installerArgs != "" {
		return json.Unmarshal([]byte(installerArgs), &c.InstallerArgs)
	}
	return nil
}

func (c *Config) SetDefaults() {
	if c.Role == string(models.HostRoleWorker) {
		//High availability mode is not relevant to workers, so make sure we clear this.
		c.HighAvailabilityMode = ""
	}

	if c.InfraEnvID == "" {
		c.InfraEnvID = c.ClusterID
	}
}
