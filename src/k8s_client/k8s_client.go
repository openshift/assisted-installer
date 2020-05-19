package k8s_client

import (
	"context"
	"fmt"
	"time"

	"github.com/eranco74/assisted-installer/src/ops"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	operatorv1 "github.com/openshift/client-go/operator/clientset/versioned"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

//go:generate mockgen -source=k8s_client.go -package=k8s_client -destination=mock_k8s_client.go
type K8SClient interface {
	ListMasterNodes() (*v1.NodeList, error)
	WaitForMasterNodes(context context.Context, minMasterNodes int) error
	PatchEtcd() error
	ListNodes() (*v1.NodeList, error)
	RunOCctlCommand(args []string, kubeconfigPath string, o ops.Ops) (string, error)
}

type K8SClientBuilder func(configPath string, logger *logrus.Logger) (K8SClient, error)

type k8sClient struct {
	log      *logrus.Logger
	client   *kubernetes.Clientset
	ocClient *operatorv1.Clientset
}

func NewK8SClient(configPath string, logger *logrus.Logger) (K8SClient, error) {
	config, err := clientcmd.BuildConfigFromFlags("", configPath)
	if err != nil {
		return &k8sClient{}, errors.Wrap(err, "loading kubeconfig")
	}
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return &k8sClient{}, errors.Wrap(err, "creating a Kubernetes client")
	}
	ocClient, err := operatorv1.NewForConfig(config)
	if err != nil {
		return &k8sClient{}, errors.Wrap(err, "creating a Kubernetes client")
	}
	return &k8sClient{logger, client, ocClient}, nil
}

func (c *k8sClient) ListMasterNodes() (*v1.NodeList, error) {
	nodes, err := c.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/master"})
	if err != nil {
		return &v1.NodeList{}, err
	}
	return nodes, nil
}

func (c *k8sClient) ListNodes() (*v1.NodeList, error) {
	nodes, err := c.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return &v1.NodeList{}, err
	}
	return nodes, nil
}

func (c *k8sClient) WaitForMasterNodes(ctx context.Context, minMasterNodes int) error {
	nodesTimeout := 60 * time.Minute
	logrus.Infof("Waiting up to %v for %d master nodes", nodesTimeout, minMasterNodes)
	apiContext, cancel := context.WithTimeout(ctx, nodesTimeout)
	defer cancel()
	wait.Until(func() {
		nodes, err := c.ListMasterNodes()
		if err != nil {
			logrus.Warnf("Still waiting for master nodes: %v", err)
		} else {
			nodeNameAndCondition := map[string][]v1.NodeCondition{}
			for _, node := range nodes.Items {
				nodeNameAndCondition[node.Name] = node.Status.Conditions
			}
			logrus.Infof("Found %d master nodes: %+v", len(nodes.Items), nodeNameAndCondition)
			if len(nodes.Items) >= minMasterNodes {
				logrus.Infof("WaitForMasterNodes - Done")
				cancel()
			}
		}
	}, 5*time.Second, apiContext.Done())
	err := apiContext.Err()
	if err != nil && err != context.Canceled {
		return errors.Wrap(err, "Waiting for master nodes")
	}
	return nil
}

func (c *k8sClient) PatchEtcd() error {
	c.log.Info("Patching etcd")
	data := []byte(`{"spec": {"unsupportedConfigOverrides": {"useUnsupportedUnsafeNonHANonProductionUnstableEtcd": true}}}`)
	result, err := c.ocClient.OperatorV1().Etcds().Patch(context.Background(), "cluster", types.MergePatchType, data, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "Failed to patch etcd")
	}
	c.log.Info(result)
	return nil
}

func (c *k8sClient) RunOCctlCommand(args []string, kubeconfigPath string, o ops.Ops) (string, error) {
	c.log.Infof("Running oc command with args %v", args)
	args = append([]string{fmt.Sprintf("--kubeconfig=%s", kubeconfigPath)}, args...)
	outPut, err := o.ExecPrivilegeCommand("oc", args...)
	if err != nil {
		return "", err
	}
	return outPut, nil
}
