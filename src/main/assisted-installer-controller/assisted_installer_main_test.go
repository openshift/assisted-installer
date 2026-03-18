package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/go-openapi/swag"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	assistedinstallercontroller "github.com/openshift/assisted-installer/src/assisted_installer_controller"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-service/client/installer"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	gomock "go.uber.org/mock/gomock"
	v1 "k8s.io/api/core/v1"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "controller_main_test")
}

var (
	availableClusterVersionCondition = &configv1.ClusterVersion{
		Status: configv1.ClusterVersionStatus{
			Conditions: []configv1.ClusterOperatorStatusCondition{{Type: configv1.OperatorAvailable,
				Status:  configv1.ConditionTrue,
				Message: "done"}},
		},
	}
)

var _ = Describe("installer HostRoleMaster role", func() {
	var (
		l              = logrus.New()
		ctrl           *gomock.Controller
		mockbmclient   *inventory_client.MockInventoryClient
		mockController *assistedinstallercontroller.MockController
		status         *assistedinstallercontroller.ControllerStatus
	)

	l.SetOutput(io.Discard)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockbmclient = inventory_client.NewMockInventoryClient(ctrl)
		mockController = assistedinstallercontroller.NewMockController(ctrl)
		waitForInstallationInterval = 10 * time.Millisecond
		status = assistedinstallercontroller.NewControllerStatus()
		mockController.EXPECT().GetStatus().Return(status).AnyTimes()
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	It("Waiting for cluster installed - first cluster error then installed", func() {
		// fail to connect to assisted and then succeed
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, fmt.Errorf("dummy")).Times(1)
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
	})

	It("Waiting for cluster cancelled", func() {
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusCancelled)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
	})

	It("Waiting for cluster error - should set error status", func() {
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusError)},
			nil).Times(1)
		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(true))
	})

	It("Waiting for cluster Unauthorized - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)
		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Not found - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterNotFound()).Times(maximumErrorsBeforeExit)

		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Not found  - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterNotFound()).Times(1)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Unauthorized  - should exit 0 ", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(0))
	})

	It("Waiting for cluster Unauthorized less then needed for exit and then installed", func() {
		exitCode := 1
		exit = func(code int) {
			exitCode = code
		}
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(nil, installer.NewV2GetClusterUnauthorized()).Times(maximumErrorsBeforeExit - 2)
		// added to make waitForInstallation exit
		mockbmclient.EXPECT().GetCluster(gomock.Any(), false).Return(&models.Cluster{Status: swag.String(models.ClusterStatusInstalled)}, nil).Times(1)

		waitForInstallation(mockbmclient, l, mockController)
		Expect(status.HasError()).Should(Equal(false))
		Expect(exitCode).Should(Equal(1))
	})

})

var _ = Describe("installer HostRoleMaster role agent-based installation", func() {
	var (
		l             = logrus.New()
		ctrl          *gomock.Controller
		mockk8sclient *k8s_client.MockK8SClient
		kubeNamesIds  map[string]string
	)

	l.SetOutput(io.Discard)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockk8sclient = k8s_client.NewMockK8SClient(ctrl)
	})
	AfterEach(func() {
		ctrl.Finish()
	})

	It("remove uninitialized taint when inventory client cannot contact assisted-service", func() {
		kubeNamesIds = map[string]string{"node0": "6d6f00e8-70dd-48a5-859a-0f1459485ad9"}
		// generate a list of nodes with name and id from kubeNamesIds
		nodeList := GetKubeNodes(kubeNamesIds)
		Expect(len(nodeList.Items)).Should(Equal(len(kubeNamesIds)))

		for _, node := range nodeList.Items {
			for i, cond := range node.Status.Conditions {
				if cond.Type == v1.NodeReady {
					node.Status.Conditions[i].Status = v1.ConditionFalse
				}
			}
			node.Spec.Taints = []v1.Taint{
				{
					Key:    k8s_client.UNINITIALIZED_TAINT_KEY,
					Effect: v1.TaintEffectNoExecute,
					Value:  "true",
				},
			}
		}

		mockk8sclient.EXPECT().ListNodes().Return(nodeList, nil).Times(1)
		mockk8sclient.EXPECT().UntaintNode(gomock.Any()).Return(nil).Times(1)
		mockk8sclient.EXPECT().GetPods(gomock.Any(), gomock.Any(), "").Return([]v1.Pod{}, nil).AnyTimes()
		mockk8sclient.EXPECT().GetClusterVersion().Return(availableClusterVersionCondition, nil).Times(1)
		waitForInstallationAgentBasedInstaller(mockk8sclient, l, true)
	})
})

var _ = Describe("parsePullSecretToken", func() {
	const pullSecretEnvKey = "PULL_SECRET_TOKEN"

	var logger = logrus.New()

	AfterEach(func() {
		os.Unsetenv(pullSecretEnvKey)
	})

	createPullSecretFile := func(content string) string {
		f, err := os.CreateTemp("", "pull-secret-*.txt")
		Expect(err).ToNot(HaveOccurred())

		_, err = f.WriteString(content)
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}

		Expect(err).ToNot(HaveOccurred())
		Expect(f.Close()).To(Succeed())

		return f.Name()
	}

	It("returns token from file when file contains non-empty content", func() {
		path := createPullSecretFile("my-secret-token")
		defer os.Remove(path)

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("my-secret-token"))
	})

	It("returns trimmed token when file has leading and trailing whitespace", func() {
		path := createPullSecretFile("  \t\n my-token \n\t  ")
		defer os.Remove(path)

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("my-token"))
	})

	It("returns token from env when file does not exist and PULL_SECRET_TOKEN is set", func() {
		os.Setenv(pullSecretEnvKey, "env-when-file-missing")

		token, err := parsePullSecretToken(logger, "/nonexistent/path/pull-secret-token")
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(Equal("env-when-file-missing"))
	})

	It("returns empty string when file does not exist and PULL_SECRET_TOKEN is unset", func() {
		token, err := parsePullSecretToken(logger, "/nonexistent/path/pull-secret-token")
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeEmpty())
	})

	It("returns empty string when file is empty (env fallback only when file missing)", func() {
		path := createPullSecretFile("")
		defer os.Remove(path)
		os.Setenv(pullSecretEnvKey, "env-fallback-token")

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeEmpty()) // file exists, so file content wins (empty)
	})

	It("returns empty string when file has only whitespace (env fallback only when file missing)", func() {
		path := createPullSecretFile("   \n\t  ")
		defer os.Remove(path)
		os.Setenv(pullSecretEnvKey, "  env-token \n")

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeEmpty()) // file exists, trimmed content is empty
	})

	It("returns empty string when file is empty and PULL_SECRET_TOKEN is unset", func() {
		path := createPullSecretFile("")
		defer os.Remove(path)

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeEmpty())
	})

	It("returns empty string when file has only whitespace and PULL_SECRET_TOKEN is unset", func() {
		path := createPullSecretFile("   \n\t  ")
		defer os.Remove(path)

		token, err := parsePullSecretToken(logger, path)
		Expect(err).ToNot(HaveOccurred())
		Expect(token).To(BeEmpty())
	})
})

// from assisted_installer_controller_test.go
func GetKubeNodes(kubeNamesIds map[string]string) *v1.NodeList {
	file, _ := os.ReadFile("../../../test_files/node.json")
	var node v1.Node
	err := json.Unmarshal(file, &node)
	Expect(err).ToNot(HaveOccurred())
	nodeList := &v1.NodeList{}
	for name, id := range kubeNamesIds {
		node.Status.NodeInfo.SystemUUID = id
		node.Name = name
		newNode := node.DeepCopy()
		nodeList.Items = append(nodeList.Items, *newNode)
	}
	return nodeList
}
