package drymock

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/golang/mock/gomock"
	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/pkg/apis/metal3/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/k8s_client"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/sirupsen/logrus"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func mockNodeList(mockk8sclient *k8s_client.MockK8SClient, hostnames string) v1.NodeList {
	nodeListPopulated := v1.NodeList{}
	for _, hostname := range strings.Split(hostnames, ",") {
		nodeListPopulated.Items = append(nodeListPopulated.Items, v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: hostname,
			},
			Status: v1.NodeStatus{
				Conditions: []v1.NodeCondition{
					{
						Type:   v1.NodeReady,
						Status: v1.ConditionTrue,
					},
				},
			},
		})
	}

	return nodeListPopulated
}

func mockControllerPodLogs(mockk8sclient *k8s_client.MockK8SClient) {
	myselfName := "dry-controller"
	podListMyself := []v1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: myselfName}}}
	mockk8sclient.EXPECT().GetPods(gomock.Any(), map[string]string{"job-name": "assisted-installer-controller"}, gomock.Any()).Return(podListMyself, nil).AnyTimes()

	b := bytes.NewBufferString(`Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry
Dry`)

	mockk8sclient.EXPECT().GetPodLogsAsBuffer(gomock.Any(), gomock.Any(), gomock.Any()).Return(b, nil).AnyTimes()
}

// PrepareControllerDryMock utilizes k8s_client.MockK8SClient to fake the k8s API to make the
// controller think it's running on an actual cluster, just enough to make it pass an installation.
// Used in dry mode.
func PrepareControllerDryMock(mockk8sclient *k8s_client.MockK8SClient, logger *logrus.Logger, hostnames string, mcsAccessIps string) {
	// Called by main
	mockk8sclient.EXPECT().SetProxyEnvVars().Return(nil).AnyTimes()

	// Called a lot
	mockk8sclient.EXPECT().CreateEvent(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(args ...string) {
		logger.Infof("Fake creating event %+v", args)
	}).AnyTimes()

	// Called by GetReadyState to make sure we're online
	csrs := certificatesv1.CertificateSigningRequestList{}
	mockk8sclient.EXPECT().ListCsrs().Return(&csrs, nil).AnyTimes()

	bmhs := metal3v1alpha1.BareMetalHostList{}
	mockk8sclient.EXPECT().ListBMHs().Return(bmhs, nil).AnyTimes()

	machines := machinev1beta1.MachineList{}
	mockk8sclient.EXPECT().ListMachines().Return(&machines, nil).AnyTimes()

	mockk8sclient.EXPECT().IsMetalProvisioningExists().Return(true, nil).AnyTimes()

	// The controller looks at MCS pod logs to determine whether hosts downloaded ignition or not, so we fake the MCS pod logs
	fakeMcsName := "dry-mcs"
	podListMcs := []v1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: fakeMcsName}}}
	mockk8sclient.EXPECT().GetPods(gomock.Any(), map[string]string{"k8s-app": "machine-config-server"}, gomock.Any()).Return(podListMcs, nil).AnyTimes()

	mcsLogs := ""
	for _, ip := range strings.Split(mcsAccessIps, ",") {
		// Add IP access log for each IP, this is how the controller determines which node has downloaded the ignition
		mcsLogs += fmt.Sprintf("%s.(Ignition)\n", ip)
	}
	mockk8sclient.EXPECT().GetPodLogs(gomock.Any(), fakeMcsName, gomock.Any()).Return(mcsLogs, nil).AnyTimes()

	// The controller compares AI host objects to cluster Node objects (Either by name or by IP) to check which AI hosts are already
	// joined as nodes. This fakes the node list so that check will pass
	nodeListPopulated := mockNodeList(mockk8sclient, hostnames)
	mockk8sclient.EXPECT().ListNodes().Return(&nodeListPopulated, nil).AnyTimes()

	mockControllerPodLogs(mockk8sclient)

	availableConditions := []configv1.ClusterOperatorStatusCondition{
		{
			Type:    configv1.OperatorAvailable,
			Status:  configv1.ConditionTrue,
			Message: "All is well",
		},
		{
			Type:    configv1.OperatorProgressing,
			Status:  configv1.ConditionTrue,
			Message: "All is well",
		},
		{
			Type:   configv1.OperatorDegraded,
			Status: "All is well",
		},
	}

	clusterOperator := configv1.ClusterOperator{
		Status: configv1.ClusterOperatorStatus{
			Conditions: availableConditions,
		},
	}
	mockk8sclient.EXPECT().GetClusterOperator(gomock.Any()).Return(&clusterOperator, nil).AnyTimes()

	clusterVersion := configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: availableConditions,
		},
	}
	mockk8sclient.EXPECT().GetClusterVersion(gomock.Any()).Return(&clusterVersion, nil).AnyTimes()

	configMap := v1.ConfigMap{
		Data: map[string]string{
			"ca-bundle.crt": `-----BEGIN CERTIFICATE-----
MIIExTCCAq0CFCqc5fg5zMGmG6yY/PsVukKCsTWiMA0GCSqGSIb3DQEBCwUAMB8x
CzAJBgNVBAYTAlVTMRAwDgYDVQQKDAdSZWQgSGF0MB4XDTIxMTAyOTIyNTY0MVoX
DTIyMTAyOTIyNTY0MVowHzELMAkGA1UEBhMCVVMxEDAOBgNVBAoMB1JlZCBIYXQw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQDqR5zsx+2WpNTO0RBYFrQd
swo5ALC9XIj1EfcxXECdtZE/6ZBXS/bxkN7DsB5ych9GVgBrmX0093Qng9US/CXF
5vTthG/+BhH0u3+6x6bRLagqayuRWiD/mQRZ10X1EswIebH6pMXXKweLK/Sg9PlB
FtD2JNIQhirdUSMkF4ud0yoW66YE+vJGFyHBAEB5A2ws+4ymaxyVYBJVfdS8nCfU
gPee1h6DjmUO8GyeF3kY2eVERqTW6E+BhrjOB1DOcHacxj4t2CQMUJRXNqL2QMLz
n1tRyBPoVI8BjQNzI8+Hb6g6vKIFvtVoJrqeT/ASgZkEaPJt4sP1ss2ODhGUuLNB
fz/ZzFgglswJEFhBtv4J2zXkoI69KdFAxaa37qULL6MxNQ+y4LhfBc5WzqHdg4EI
5xtbuCpHErpc1VIDe1Ok3NXsFHN99tH+vwA6fcYmz9VjE/HKMHRzwNRjmDW1cdNO
uAgZTkslhfiu1rjxlIetU6LH2lBKy9UtoRjNw014F2IJeje8j1WHuR/ih705qkVf
wYG2NKMRRUV12tpKwuqX/TQFa++aB95vhjZsAtrB2P66CWROtFCjd8woHEkZEGHt
Gh8R4UXxk2VvHlglb8tvEr+n3Fuz41dLZeepZR2CzaySgjLUAqOahO3ZmEitUtiw
GB5Q3+bhB9lUVFd0IGuQEwIDAQABMA0GCSqGSIb3DQEBCwUAA4ICAQBi507wwqP+
Yc8xEeKXxazheIUuf1o9WH1XTdUJPklRdwZj7HxZa49FzammW7MWhVNbqsD6bZdp
5Iy9JCsJBP5Z6gWbP3LgypcWw4xmNiPXZw+9pbnRmIiObGvWEnHmtI6MTvAHZttd
sEnrvH7LV2Dr7TZzfV7mrOh2JgDlQ5yOvXx9x9sV9GaqGbx5tK11S//Th5TfGkXQ
CoygE/SwZPAHM4jcU//j5/QbYegtJIVFK/JQrMcc37ecwcYf0f3q5GZ/c4zUBQ7Z
BZMSGMGObxNqIIW3QsB+sZyfpZxWUanxJiKy9Uw8jBq+zswWep8WXnFtL1wyHeiB
MpYEls0yPGsCeEF3vFlpFR1Aob0nLAimAEyxf4GUZiI1CCqWzhIQ8jaiSfnsyh9f
irj1Q/xTIEK4sbyl//QXLpW/OXgXUG6WIlyvg1LPdbngfU5S8DxSXse9JHIno+cD
7Ugdiw+3c32FQnX4vqKLhtT7IClWmyTN84tcKMJVKhreQ+Yz0+eCTIwV5JQFTsRp
tGxE/NUwbjuRib3HvsiuCUIcRQKJQerdAYWob47cnIA/YH0Hngq1Ci1GtcuYJQnP
dEFgad6P3hMZTOg7yVkMOd3QtgVQ9I8dXqS2nG9EMEh97WIhi6f5ztvcQvQ5tXjh
1OZbvvo716WbONeK0GuS3WbwVTQFSUBtCA==
-----END CERTIFICATE-----
-----BEGIN CERTIFICATE-----
MIIExTCCAq0CFCqc5fg5zMGmG6yY/PsVukKCsTWiMA0GCSqGSIb3DQEBCwUAMB8x
CzAJBgNVBAYTAlVTMRAwDgYDVQQKDAdSZWQgSGF0MB4XDTIxMTAyOTIyNTY0MVoX
DTIyMTAyOTIyNTY0MVowHzELMAkGA1UEBhMCVVMxEDAOBgNVBAoMB1JlZCBIYXQw
ggIiMA0GCSqGSIb3DQEBAQUAA4ICDwAwggIKAoICAQDqR5zsx+2WpNTO0RBYFrQd
swo5ALC9XIj1EfcxXECdtZE/6ZBXS/bxkN7DsB5ych9GVgBrmX0093Qng9US/CXF
5vTthG/+BhH0u3+6x6bRLagqayuRWiD/mQRZ10X1EswIebH6pMXXKweLK/Sg9PlB
FtD2JNIQhirdUSMkF4ud0yoW66YE+vJGFyHBAEB5A2ws+4ymaxyVYBJVfdS8nCfU
gPee1h6DjmUO8GyeF3kY2eVERqTW6E+BhrjOB1DOcHacxj4t2CQMUJRXNqL2QMLz
n1tRyBPoVI8BjQNzI8+Hb6g6vKIFvtVoJrqeT/ASgZkEaPJt4sP1ss2ODhGUuLNB
fz/ZzFgglswJEFhBtv4J2zXkoI69KdFAxaa37qULL6MxNQ+y4LhfBc5WzqHdg4EI
5xtbuCpHErpc1VIDe1Ok3NXsFHN99tH+vwA6fcYmz9VjE/HKMHRzwNRjmDW1cdNO
uAgZTkslhfiu1rjxlIetU6LH2lBKy9UtoRjNw014F2IJeje8j1WHuR/ih705qkVf
wYG2NKMRRUV12tpKwuqX/TQFa++aB95vhjZsAtrB2P66CWROtFCjd8woHEkZEGHt
Gh8R4UXxk2VvHlglb8tvEr+n3Fuz41dLZeepZR2CzaySgjLUAqOahO3ZmEitUtiw
GB5Q3+bhB9lUVFd0IGuQEwIDAQABMA0GCSqGSIb3DQEBCwUAA4ICAQBi507wwqP+
Yc8xEeKXxazheIUuf1o9WH1XTdUJPklRdwZj7HxZa49FzammW7MWhVNbqsD6bZdp
5Iy9JCsJBP5Z6gWbP3LgypcWw4xmNiPXZw+9pbnRmIiObGvWEnHmtI6MTvAHZttd
sEnrvH7LV2Dr7TZzfV7mrOh2JgDlQ5yOvXx9x9sV9GaqGbx5tK11S//Th5TfGkXQ
CoygE/SwZPAHM4jcU//j5/QbYegtJIVFK/JQrMcc37ecwcYf0f3q5GZ/c4zUBQ7Z
BZMSGMGObxNqIIW3QsB+sZyfpZxWUanxJiKy9Uw8jBq+zswWep8WXnFtL1wyHeiB
MpYEls0yPGsCeEF3vFlpFR1Aob0nLAimAEyxf4GUZiI1CCqWzhIQ8jaiSfnsyh9f
irj1Q/xTIEK4sbyl//QXLpW/OXgXUG6WIlyvg1LPdbngfU5S8DxSXse9JHIno+cD
7Ugdiw+3c32FQnX4vqKLhtT7IClWmyTN84tcKMJVKhreQ+Yz0+eCTIwV5JQFTsRp
tGxE/NUwbjuRib3HvsiuCUIcRQKJQerdAYWob47cnIA/YH0Hngq1Ci1GtcuYJQnP
dEFgad6P3hMZTOg7yVkMOd3QtgVQ9I8dXqS2nG9EMEh97WIhi6f5ztvcQvQ5tXjh
1OZbvvo716WbONeK0GuS3WbwVTQFSUBtCA==
-----END CERTIFICATE-----
`,
		},
	}
	mockk8sclient.EXPECT().GetConfigMap(gomock.Any(), gomock.Any()).Return(&configMap, nil).AnyTimes()

	clusterOperatorList := &configv1.ClusterOperatorList{}

	for _, operatorName := range []string{
		"authentication",
		"baremetal",
		"cloud-controller-manager",
		"cloud-credential",
		"cluster-autoscaler",
		"config-operator",
		"console",
		"csi-snapshot-controller",
		"dns",
		"etcd",
		"image-registry",
		"ingress",
		"insights",
		"kube-apiserver",
		"kube-controller-manager",
		"kube-scheduler",
		"kube-storage-version-migrator",
		"machine-api",
		"machine-approver",
		"machine-config",
		"marketplace",
		"monitoring",
		"network",
		"node-tuning",
		"openshift-apiserver",
		"openshift-controller-manager",
		"openshift-samples",
		"operator-lifecycle-manager",
		"operator-lifecycle-manager-catalog",
		"operator-lifecycle-manager-packageserver",
		"service-ca",
		"storage",
	} {
		clusterOperator := configv1.ClusterOperator{
			ObjectMeta: metav1.ObjectMeta{
				Name: operatorName,
			},
			Status: configv1.ClusterOperatorStatus{
				Conditions: []configv1.ClusterOperatorStatusCondition{
					{
						Type:   configv1.OperatorAvailable,
						Status: configv1.ConditionTrue,
					},
					{
						Type:   configv1.OperatorProgressing,
						Status: configv1.ConditionTrue,
					},
				},
			},
		}

		clusterOperatorList.Items = append(clusterOperatorList.Items, clusterOperator)
	}

	mockk8sclient.EXPECT().ListClusterOperators().Return(clusterOperatorList, nil).AnyTimes()
}

// PrepareInstallerDryK8sMock utilizes k8s_client.MockK8SClient to fake the k8s API to make the
// installer think it's talking with an actual cluster, just enough to make it pass an installation.
// Used in dry mode.
func PrepareInstallerDryK8sMock(mockk8sclient *k8s_client.MockK8SClient, logger *logrus.Logger, hostnames string) {
	// The installer compares AI host objects to cluster Node objects (either by name or by IP) to check which AI hosts are already
	// joined as nodes. This fakes the node list so that check will pass
	nodeListPopulated := mockNodeList(mockk8sclient, hostnames)
	mockk8sclient.EXPECT().ListMasterNodes().Return(&nodeListPopulated, nil).AnyTimes()

	events := v1.EventList{Items: []v1.Event{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: common.AssistedControllerIsReadyEvent,
			},
			Message: "The installer is going to be looking for an event with this name to check whether the controller started",
		},
	}}
	mockk8sclient.EXPECT().ListEvents(gomock.Any()).Return(&events, nil).AnyTimes()

	mockControllerPodLogs(mockk8sclient)
}
