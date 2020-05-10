package utils

import "fmt"

const (
	rhcos = "rhcos"
	mco   = "mco"
)

var getOpenshiftMapping = func(openshiftVersion string) map[string]string {
	openshiftVersionMap := map[string]map[string]string{"4.4": {rhcos: "https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.4/latest/rhcos-4.4.3-x86_64-metal.x86_64.raw.gz",
		mco: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:301586e92bbd07ead7c5d3f342899e5923d4ef2e0f1c0cf08ecaae96568d16ed"}}

	return openshiftVersionMap[openshiftVersion]
}

func GetRhcosImageByOpenshiftVersion(openshiftVersion string) (string, error) {
	rhcosImage := getOpenshiftMapping(openshiftVersion)[rhcos]
	if rhcosImage == "" {
		return "", fmt.Errorf("failed to find rhcos image for openshift version %s", openshiftVersion)
	}
	return rhcosImage, nil
}

func GetMCOByOpenshiftVersion(openshiftVersion string) (string, error) {
	mcoImage := getOpenshiftMapping(openshiftVersion)[mco]
	if mcoImage == "" {
		return "", fmt.Errorf("failed to find mco image for openshift version %s", openshiftVersion)
	}
	return mcoImage, nil
}

func IsOpenshiftVersionIsSupported(openshiftVersion string) bool {
	return len(getOpenshiftMapping(openshiftVersion)) != 0
}
