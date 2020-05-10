package config

import (
	"flag"
	"os"
)

type Config struct {
	Role             string
	ClusterID        string
	HostID           string
	Device           string
	Host             string
	Port             int
	Verbose          bool
	OpenshiftVersion string
}

var GlobalConfig Config

func printHelpAndExit() {
	flag.CommandLine.Usage()
	os.Exit(0)
}

func ProcessArgs() {
	ret := &GlobalConfig
	flag.StringVar(&ret.Role, "role", "master", "The node role")
	flag.StringVar(&ret.ClusterID, "cluster-id", "", "The cluster id")
	flag.StringVar(&ret.HostID, "host-id", "", "This host id")
	flag.StringVar(&ret.Device, "boot-device", "", "The boot device")
	flag.StringVar(&ret.Host, "host", "", "The BM inventory host address")
	flag.IntVar(&ret.Port, "port", 80, "The BM inventory port")
	flag.StringVar(&ret.OpenshiftVersion, "openshift-version", "4.4", "Openshift version to install")
	flag.BoolVar(&ret.Verbose, "verbose", false, "Increase verbosity, set log level to debug")
	h := flag.Bool("help", false, "Help message")
	flag.Parse()
	if h != nil && *h {
		printHelpAndExit()
	}
}
