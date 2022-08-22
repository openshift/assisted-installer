package config

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/thoas/go-funk"

	"github.com/openshift/assisted-installer/src/utils"

	"github.com/openshift/assisted-service/models"
)

type Config struct {
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
}

var GlobalConfig Config

func printHelpAndExit() {
	flag.CommandLine.Usage()
	os.Exit(0)
}

func ProcessArgs() {
	ret := &GlobalConfig
	flag.StringVar(&ret.Role, "role", string(models.HostRoleMaster), "The node role")
	flag.StringVar(&ret.ClusterID, "cluster-id", "", "The cluster id")
	flag.StringVar(&ret.InfraEnvID, "infra-env-id", "", "This host infra env id")
	flag.StringVar(&ret.HostID, "host-id", "", "This host id")
	flag.StringVar(&ret.Device, "boot-device", "", "The boot device")
	flag.StringVar(&ret.URL, "url", "", "The BM inventory URL, including a scheme and optionally a port (overrides the host and port arguments")
	flag.StringVar(&ret.OpenshiftVersion, "openshift-version", "4.4", "Openshift version to install")
	flag.StringVar(&ret.MCOImage, "mco-image", "", "MCO image to install")
	flag.BoolVar(&ret.Verbose, "verbose", false, "Increase verbosity, set log level to debug")
	flag.StringVar(&ret.ControllerImage, "controller-image", "quay.io/ocpmetal/assisted-installer-controller:latest",
		"Assisted Installer Controller image URL")
	flag.StringVar(&ret.AgentImage, "agent-image", "quay.io/ocpmetal/assisted-installer-agent:latest",
		"Assisted Installer Agent image URL that will be used to send logs on successful installation")
	flag.BoolVar(&ret.SkipCertVerification, "insecure", false, "Do not validate TLS certificate")
	flag.StringVar(&ret.CACertPath, "cacert", "", "Path to custom CA certificate in PEM format")
	flag.StringVar(&ret.HTTPProxy, "http-proxy", "", "A proxy URL to use for creating HTTP connections outside the cluster")
	flag.StringVar(&ret.HTTPSProxy, "https-proxy", "", "A proxy URL to use for creating HTTPS connections outside the cluster")
	flag.StringVar(&ret.NoProxy, "no-proxy", "", "A comma-separated list of destination domain names, domains, IP addresses, or other network CIDRs to exclude proxying")
	flag.StringVar(&ret.ServiceIPs, "service-ips", "", "All IPs of assisted service node")
	flag.StringVar(&ret.HighAvailabilityMode, "high-availability-mode", "Full", "high-availability expectations. default is 'Full', which represents the behavior in a \"normal\" cluster. Use 'None' for single-node deployment")
	flag.BoolVar(&ret.CheckClusterVersion, "check-cluster-version", false, "Do not monitor CVO")
	flag.StringVar(&ret.MustGatherImage, "must-gather-image", "", "Custom must-gather image")
	flag.Var(&ret.DisksToFormat, "format-disk", "Disk to format. Can be specified multiple times")
	flag.BoolVar(&ret.SkipInstallationDiskCleanup, "skip-installation-disk-cleanup", false, "Skip installation disk cleanup gives disk management to coreos-installer in case needed")

	var installerArgs string
	flag.StringVar(&installerArgs, "installer-args", "", "JSON array of additional coreos-installer arguments")

	h := flag.Bool("help", false, "Help message")
	flag.Parse()

	if ret.NoProxy != "" {
		utils.SetNoProxyEnv(ret.NoProxy)
	}
	if h != nil && *h {
		printHelpAndExit()
	}

	if installerArgs != "" {
		err := json.Unmarshal([]byte(installerArgs), &ret.InstallerArgs)
		if err != nil {
			println(err.Error())
			printHelpAndExit()
		}
	}
	if ret.HighAvailabilityMode == models.ClusterHighAvailabilityModeNone {
		validRoles := []string{string(models.HostRoleMaster), string(models.HostRoleBootstrap)}
		if !funk.ContainsString(validRoles, ret.Role) {
			println("high-availability-mode is set to None, but host role is %s. should be one of: %s", ret.Role, validRoles)
			printHelpAndExit()
		}
	}
	if ret.InfraEnvID == "" {
		ret.InfraEnvID = ret.ClusterID
	}
}
