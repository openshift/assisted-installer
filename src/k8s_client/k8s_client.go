package k8s_client

import (
	"context"
	"time"

	"k8s.io/client-go/tools/clientcmd"

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
	WaitForMasterNodes(minMasterNodes int) error
}

type k8sClient struct {
	log    *logrus.Logger
	client *kubernetes.Clientset
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
	return &k8sClient{logger, client}, nil
}

func (c *k8sClient) ListMasterNodes() (*v1.NodeList, error) {
	nodes, err := c.client.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{LabelSelector: "node-role.kubernetes.io/master"})
	if err != nil {
		return &v1.NodeList{}, err
	}
	return nodes, nil
}

func (c *k8sClient) WaitForMasterNodes(minMasterNodes int) error {
	nodesTimeout := 60 * time.Minute
	logrus.Infof("Waiting up to %v for %d master nodes", nodesTimeout, minMasterNodes)
	apiContext, cancel := context.WithTimeout(context.Background(), nodesTimeout)
	defer cancel()
	wait.Until(func() {
		nodes, err := c.ListMasterNodes()
		if err != nil {
			logrus.Warnf("Still waiting for master nodes: %v", err)
		} else {
			logrus.Infof("Found %d master nodes: %v", len(nodes.Items), nodes.Items)
			if len(nodes.Items) >= minMasterNodes {
				logrus.Infof("WaitForMasterNodes - Done")
				cancel()
			}
		}
	}, 5*time.Second, apiContext.Done())
	err := apiContext.Err()
	if err != nil && err != context.Canceled {
		return errors.Wrap(err, "waiting for master nodes")
	}
	return nil
}
