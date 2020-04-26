package config

import (
	"flag"
	"os"
)

type Config struct {
	Role          string
	ClusterID     string
	Device        string
	S3EndpointURL string
	S3Bucket      string
	Verbose       bool
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
	flag.StringVar(&ret.Device, "boot-device", "", "The boot device")
	flag.StringVar(&ret.S3EndpointURL, "s3-endpoint", "", "The s3 endpoint url for fetching files")
	flag.StringVar(&ret.S3Bucket, "s3-bucket", "test", "The s3 bucket for fetching files")
	flag.BoolVar(&ret.Verbose, "verbose", false, "Increase verbosity, set log level to debug")
	h := flag.Bool("help", false, "Help message")
	flag.Parse()
	if h != nil && *h {
		printHelpAndExit()
	}
}
