package utils

import "fmt"

const (
	rhcos = "rhcos"
	mco   = "mco"
)

var getOpenshiftMapping = func(openshiftVersion string) map[string]string {
	openshiftVersionMap := map[string]map[string]string{
		"4.4": {
			rhcos: "https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.4/latest/rhcos-4.4.3-x86_64-metal.x86_64.raw.gz",
			mco:   "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:301586e92bbd07ead7c5d3f342899e5923d4ef2e0f1c0cf08ecaae96568d16ed"},
		"4.5": {
			rhcos: "https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.5/4.5.1/rhcos-4.5.1-x86_64-metal.x86_64.raw.gz",       // 4.5.1 image in the openshift mirror
			mco:   "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:58bed0f5c4fbe453b994f6b606fefe3e2aaf1d10dcbdf5debb73b93007c7bee5"}, // MCO from latest 4.5 nightly https://mirror.openshift.com/pub/openshift-v4/clients/ocp-dev-preview/4.5.0-0.nightly-2020-05-14-021132/
		"4.6": {
			rhcos: "https://releases-art-rhcos.svc.ci.openshift.org/art/storage/releases/rhcos-4.6/46.82.202007212240-0/x86_64/rhcos-46.82.202007212240-0-metal.x86_64.raw.gz",
			mco:   "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:460a2e0e44610ea6b88080020d1a2f3a04b1cd0f5826f9d93354eab9a768c347"},
	}

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
