package config

import (
	"flag"
	"os"
	"time"

	"github.com/filanov/bm-inventory/models"
)

type Config struct {
	Role                string
	ClusterID           string
	HostID              string
	Device              string
	Host                string
	Port                int
	Verbose             bool
	OpenshiftVersion    string
	Hostname            string
	ControllerImage     string
	InstallationTimeout time.Duration
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
	flag.StringVar(&ret.HostID, "host-id", "", "This host id")
	flag.StringVar(&ret.Device, "boot-device", "", "The boot device")
	flag.StringVar(&ret.Host, "host", "", "The BM inventory host address")
	flag.IntVar(&ret.Port, "port", 80, "The BM inventory port")
	flag.StringVar(&ret.OpenshiftVersion, "openshift-version", "4.4", "Openshift version to install")
	flag.StringVar(&ret.Hostname, "host-name", "", "hostname to be set for this node")
	flag.BoolVar(&ret.Verbose, "verbose", false, "Increase verbosity, set log level to debug")
	flag.StringVar(&ret.ControllerImage, "controller-image", "quay.io/ocpmetal/assisted-installer-controller:latest",
		"Assisted Installer Controller image URL")
	flag.DurationVar(&ret.InstallationTimeout, "installation-timeout", 120, "Installation timeout in minutes")
	h := flag.Bool("help", false, "Help message")
	flag.Parse()
	if h != nil && *h {
		printHelpAndExit()
	}
}
