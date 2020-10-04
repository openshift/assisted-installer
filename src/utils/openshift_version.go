package utils

import "fmt"

const (
	mco = "mco"
)

var getOpenshiftMapping = func(openshiftVersion string) map[string]string {
	openshiftVersionMap := map[string]map[string]string{
		"4.4": {
			mco: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:301586e92bbd07ead7c5d3f342899e5923d4ef2e0f1c0cf08ecaae96568d16ed"},
		"4.5": {
			mco: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:58bed0f5c4fbe453b994f6b606fefe3e2aaf1d10dcbdf5debb73b93007c7bee5"}, // MCO from latest 4.5 nightly https://mirror.openshift.com/pub/openshift-v4/clients/ocp-dev-preview/4.5.0-0.nightly-2020-05-14-021132/
		"4.6": {
			mco: "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221"},
	}

	return openshiftVersionMap[openshiftVersion]
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
