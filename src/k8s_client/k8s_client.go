package k8s_client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	metal3v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	configv1client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorv1 "github.com/openshift/client-go/operator/clientset/versioned"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	olmv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	operatorsv1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	olmv1client "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned/typed/operators/v1alpha1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	"gopkg.in/yaml.v2"
	batchV1 "k8s.io/api/batch/v1"
	certificatesv1 "k8s.io/api/certificates/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	certificatesClient "k8s.io/client-go/kubernetes/typed/certificates/v1"
	"k8s.io/client-go/tools/clientcmd"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	runtimeconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
)

//var AddToSchemes runtime.SchemeBuilder

//go:generate mockgen -source=k8s_client.go -package=k8s_client -destination=mock_k8s_client.go
type K8SClient interface {
	ListMasterNodes() (*v1.NodeList, error)
	EnableRouterAccessLogs() error
	ListNodes() (*v1.NodeList, error)
	ListMachines() (*machinev1beta1.MachineList, error)
	RunOCctlCommand(args []string, kubeconfigPath string, o ops.Ops) (string, error)
	ApproveCsr(csr *certificatesv1.CertificateSigningRequest) error
	ListCsrs() (*certificatesv1.CertificateSigningRequestList, error)
	GetConfigMap(namespace string, name string) (*v1.ConfigMap, error)
	GetPodLogs(namespace string, podName string, sinceSeconds int64) (string, error)
	GetPodLogsAsBuffer(namespace string, podName string, sinceSeconds int64) (*bytes.Buffer, error)
	GetPods(namespace string, labelMatch map[string]string, fieldSelector string) ([]v1.Pod, error)
	GetCSV(namespace string, name string) (*olmv1alpha1.ClusterServiceVersion, error)
	GetCSVFromSubscription(namespace string, name string) (string, error)
	GetSubscription(subscription types.NamespacedName) (*olmv1alpha1.Subscription, error)
	GetAllInstallPlansOfSubscription(subscription types.NamespacedName) ([]olmv1alpha1.InstallPlan, error)
	DeleteInstallPlan(installPlan types.NamespacedName) error
	IsMetalProvisioningExists() (bool, error)
	ListBMHs() (metal3v1alpha1.BareMetalHostList, error)
	GetBMH(name string) (*metal3v1alpha1.BareMetalHost, error)
	UpdateBMHStatus(bmh *metal3v1alpha1.BareMetalHost) error
	UpdateBMH(bmh *metal3v1alpha1.BareMetalHost) error
	SetProxyEnvVars() error
	GetClusterVersion() (*configv1.ClusterVersion, error)
	GetServiceNetworks() ([]string, error)
	GetControlPlaneReplicas() (int, error)
	ListServices(namespace string) (*v1.ServiceList, error)
	ListEvents(namespace string) (*v1.EventList, error)
	ListClusterOperators() (*configv1.ClusterOperatorList, error)
	GetClusterOperator(name string) (*configv1.ClusterOperator, error)
	CreateEvent(namespace, name, message, component string) (*v1.Event, error)
	DeleteService(namespace, name string) error
	DeletePods(namespace string) error
	PatchNamespace(namespace string, data []byte) error
	GetNode(name string) (*v1.Node, error)
	PatchNodeLabels(nodeName string, nodeLabels string) error
	ListJobs(namespace string) (*batchV1.JobList, error)
	DeleteJob(job types.NamespacedName) error
	IsClusterCapabilityEnabled(configv1.ClusterVersionCapability) (bool, error)
	UntaintNode(name string) error
	PatchMachineConfigPoolPaused(pause bool, mcpName string) error
}

type K8SClientBuilder func(configPath string, logger logrus.FieldLogger) (K8SClient, error)

type k8sClient struct {
	log           logrus.FieldLogger
	client        *kubernetes.Clientset
	ocClient      *operatorv1.Clientset
	olmClient     *olmv1client.OperatorsV1alpha1Client
	runtimeClient runtimeclient.Client
	// CertificateSigningRequestInterface is interface
	csrClient    certificatesClient.CertificateSigningRequestInterface
	proxyClient  configv1client.ProxyInterface
	configClient *configv1client.ConfigV1Client
}

const (
	KUBE_SYSTEM_NAMESPACE   = "kube-system"
	CLUSTER_CONFIG_V1_NAME  = "cluster-config-v1"
	UNINITIALIZED_TAINT_KEY = "node.cloudprovider.kubernetes.io/uninitialized"
)

func NewK8SClient(configPath string, logger logrus.FieldLogger) (K8SClient, error) {
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
	csvClient, err := olmv1client.NewForConfig(config)
	if err != nil {
		return &k8sClient{}, errors.Wrap(err, "creating a Kubernetes client")
	}
	csrClient := client.CertificatesV1().CertificateSigningRequests()
	configClient, err := configv1client.NewForConfig(config)
	if err != nil {
		return &k8sClient{}, errors.Wrap(err, "creating openshift config client")
	}
	var runtimeClient runtimeclient.Client
	if configPath == "" {
		scheme := runtime.NewScheme()
		err = clientgoscheme.AddToScheme(scheme)
		if err != nil {
			return &k8sClient{}, errors.Wrap(err, "failed to add scheme to")
		}

		err = metal3v1alpha1.AddToScheme(scheme)
		if err != nil {
			return &k8sClient{}, errors.Wrap(err, "failed to add BMH scheme")
		}
		err = machinev1beta1.AddToScheme(scheme)
		if err != nil {
			return &k8sClient{}, errors.Wrap(err, "failed to add Machine scheme")
		}

		err = mcfgv1.AddToScheme(scheme)
		if err != nil {
			return &k8sClient{}, errors.Wrap(err, "failed to add MCP scheme")
		}

		runtimeClient, err = runtimeclient.New(runtimeconfig.GetConfigOrDie(), runtimeclient.Options{Scheme: scheme})
		if err != nil {
			return &k8sClient{}, errors.Wrap(err, "failed to create runtime client")
		}
	}

	return &k8sClient{logger, client, ocClient, csvClient, runtimeClient, csrClient,
		configClient.Proxies(), configClient}, nil
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

func (c *k8sClient) ListServices(namespace string) (*v1.ServiceList, error) {
	services, err := c.client.CoreV1().Services(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return &v1.ServiceList{}, err
	}
	return services, nil
}

func (c *k8sClient) DeleteService(name, namespace string) error {
	return c.client.CoreV1().Services(namespace).Delete(context.TODO(), name, metav1.DeleteOptions{})
}

func (c *k8sClient) ListJobs(namespace string) (*batchV1.JobList, error) {
	jobs, err := c.client.BatchV1().Jobs(namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return &batchV1.JobList{}, err
	}
	return jobs, nil
}

func (c *k8sClient) DeleteJob(job types.NamespacedName) error {
	return c.client.BatchV1().Jobs(job.Namespace).Delete(context.TODO(), job.Name, metav1.DeleteOptions{})
}

func (c *k8sClient) DeletePods(namespace string) error {
	return c.client.CoreV1().Pods(namespace).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{})
}

// TODO: We should be passing the context to these functions
func (c *k8sClient) PatchNamespace(namespace string, data []byte) error {
	_, err := c.client.CoreV1().Namespaces().Patch(context.TODO(), namespace, types.StrategicMergePatchType, data, metav1.PatchOptions{})
	return err
}

func (c *k8sClient) ListMachines() (*machinev1beta1.MachineList, error) {
	machines := machinev1beta1.MachineList{}
	opts := &runtimeclient.ListOptions{
		Namespace: "openshift-machine-api",
	}

	err := c.runtimeClient.List(context.Background(), &machines, opts)
	if err != nil {
		c.log.Errorf("failed to list Machines, error %s", err)
		return &machinev1beta1.MachineList{}, err
	}
	return &machines, nil
}

func (c *k8sClient) EnableRouterAccessLogs() error {
	c.log.Info("Enabling router logs")
	data := []byte(`{"spec":{"logging":{"access":{"destination":{"type":"Container"}}}}}`)
	result, err := c.ocClient.OperatorV1().IngressControllers("openshift-ingress-operator").Patch(context.Background(),
		"default", types.MergePatchType, data, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "Failed to patch router")
	}
	c.log.Info(result)
	return nil
}

func (c *k8sClient) getInstallConfig() (string, error) {
	cm, err := c.GetConfigMap(KUBE_SYSTEM_NAMESPACE, CLUSTER_CONFIG_V1_NAME)
	if err != nil {
		return "", errors.Wrap(err, "Failed to get config map")
	}
	installConfig, found := cm.Data["install-config"]
	if !found {
		return "", errors.New("Failed to get install config")
	}
	return installConfig, nil
}

func (c *k8sClient) GetServiceNetworks() ([]string, error) {
	installConfig, err := c.getInstallConfig()
	if err != nil {
		return nil, err
	}
	var networkingDecoder struct {
		Networking struct {
			ServiceNetwork []string `yaml:"serviceNetwork"`
		} `yaml:"networking"`
	}
	err = yaml.Unmarshal([]byte(installConfig), &networkingDecoder)
	if err != nil {
		return nil, errors.Wrapf(err, "Failed to unmarshal %s", installConfig)
	}
	return networkingDecoder.Networking.ServiceNetwork, nil
}

func (c *k8sClient) GetControlPlaneReplicas() (int, error) {
	installConfig, err := c.getInstallConfig()
	if err != nil {
		return 0, err
	}

	var controlPlaneDecoder struct {
		ControlPlane struct {
			Replicas int `yaml:"replicas"`
		} `yaml:"controlPlane"`
	}
	err = yaml.Unmarshal([]byte(installConfig), &controlPlaneDecoder)
	if err != nil {
		return 0, errors.Wrapf(err, "Failed to unmarshal %s", installConfig)
	}
	return controlPlaneDecoder.ControlPlane.Replicas, nil
}

func updateItem(item *yaml.MapItem, path []string, value string) error {
	if len(path) == 0 {
		item.Value = value
		return nil
	}
	slice, ok := item.Value.(yaml.MapSlice)
	if !ok {
		return errors.New("Underlying is not a slice")
	}
	return updateSlice(slice, path, value)
}

func updateSlice(slice yaml.MapSlice, path []string, value string) error {
	for i := range slice {
		if slice[i].Key == interface{}(path[0]) {
			return updateItem(&slice[i], path[1:], value)
		}
	}
	return errors.Errorf("%s was not found", path[0])
}

func updateSliceValue(slice yaml.MapSlice, path string, value string) error {
	splitPath := strings.Split(path, ".")
	return updateSlice(slice, splitPath, value)
}

func (c *k8sClient) updateControlPlaneReplicas(value string) error {
	cm, err := c.GetConfigMap(KUBE_SYSTEM_NAMESPACE, CLUSTER_CONFIG_V1_NAME)
	if err != nil {
		return errors.Wrap(err, "Failed to get config map")
	}
	installConfigStr, found := cm.Data["install-config"]
	if !found {
		return errors.New("Failed to get install config")
	}
	var installConfig yaml.MapSlice
	if err = yaml.Unmarshal([]byte(installConfigStr), &installConfig); err != nil {
		return errors.Wrap(err, "Failed to unmarshal install-config")
	}
	if err = updateSliceValue(installConfig, "controlPlane.replicas", value); err != nil {
		return err
	}
	b, err := yaml.Marshal(&installConfig)
	if err != nil {
		return errors.Wrap(err, "Failed to marshal install-config yaml")
	}
	jsonPayload := map[string]map[string]string{
		"data": {
			"install-config": string(b),
		},
	}
	data, err := json.Marshal(&jsonPayload)
	if err != nil {
		return errors.Wrap(err, "Failed to JSON marshal")
	}
	result, err := c.client.CoreV1().ConfigMaps(KUBE_SYSTEM_NAMESPACE).Patch(context.TODO(), CLUSTER_CONFIG_V1_NAME, types.MergePatchType, data, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "Failed to patch control plane replicas")
	}
	c.log.Debug(result)
	c.log.Infof("Changed control plane replicas to %s", value)
	return nil
}

func (c *k8sClient) RunOCctlCommand(args []string, kubeconfigPath string, o ops.Ops) (string, error) {
	c.log.Infof("Running oc command with args %v", args)
	args = append([]string{fmt.Sprintf("--kubeconfig=%s", kubeconfigPath)}, args...)
	outPut, err := o.ExecPrivilegeCommand(utils.NewLogWriter(c.log), "oc", args...)
	if err != nil {
		return "", err
	}
	return outPut, nil
}

func (c k8sClient) ListCsrs() (*certificatesv1.CertificateSigningRequestList, error) {
	csrs, err := c.csrClient.List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		c.log.WithError(err).Errorf("Failed to get list of CSRs.")
		return nil, err
	}
	return csrs, nil
}

func (c k8sClient) ApproveCsr(csr *certificatesv1.CertificateSigningRequest) error {

	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Reason:         "NodeCSRApprove",
		Message:        "This CSR was approved by the assisted-installer-controller",
		Status:         v1.ConditionTrue,
		LastUpdateTime: metav1.Now(),
	})
	if _, err := c.csrClient.UpdateApproval(context.TODO(), csr.Name, csr, metav1.UpdateOptions{}); err != nil {
		c.log.WithError(err).Errorf("Failed to approve CSR %v", csr)
		return err
	}
	return nil
}

func (c *k8sClient) GetConfigMap(namespace string, name string) (*v1.ConfigMap, error) {
	cm, err := c.client.CoreV1().ConfigMaps(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return cm, nil
}

func (c *k8sClient) SetProxyEnvVars() error {
	options := metav1.GetOptions{}
	proxy, err := c.proxyClient.Get(context.TODO(), "cluster", options)
	if err != nil {
		return err
	}
	c.log.Infof("Using proxy %+v to set env-vars for installer-controller pod", proxy.Status)
	if proxy.Status.HTTPProxy != "" {
		os.Setenv("HTTP_PROXY", proxy.Status.HTTPProxy)
	}
	if proxy.Status.HTTPSProxy != "" {
		os.Setenv("HTTPS_PROXY", proxy.Status.HTTPSProxy)
	}
	if proxy.Status.NoProxy != "" {
		os.Setenv("NO_PROXY", proxy.Status.NoProxy)
	}
	return nil
}

func (c *k8sClient) GetCSV(namespace string, name string) (*operatorsv1alpha1.ClusterServiceVersion, error) {
	csv, err := c.olmClient.ClusterServiceVersions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return csv, nil
}

func (c *k8sClient) GetPods(namespace string, labelMatch map[string]string, fieldSelector string) ([]v1.Pod, error) {
	listOptions := metav1.ListOptions{}
	if labelMatch != nil {
		labelSelector := metav1.LabelSelector{MatchLabels: labelMatch}
		listOptions.LabelSelector = labels.Set(labelSelector.MatchLabels).String()
	}
	if fieldSelector != "" {
		listOptions.FieldSelector = fieldSelector
	}
	pod, err := c.client.CoreV1().Pods(namespace).List(context.TODO(), listOptions)
	if err != nil {
		return nil, err
	}

	return pod.Items, nil
}

func (c *k8sClient) ListEvents(namespace string) (*v1.EventList, error) {
	return c.client.CoreV1().Events(namespace).List(context.TODO(), metav1.ListOptions{})
}

func (c *k8sClient) GetPodLogs(namespace string, podName string, sinceSeconds int64) (string, error) {
	buf, err := c.GetPodLogsAsBuffer(namespace, podName, sinceSeconds)
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (c *k8sClient) GetPodLogsAsBuffer(namespace string, podName string, sinceSeconds int64) (*bytes.Buffer, error) {
	podLogOpts := v1.PodLogOptions{}
	if sinceSeconds > 0 {
		podLogOpts.SinceSeconds = &sinceSeconds
	}
	req := c.client.CoreV1().Pods(namespace).GetLogs(podName, &podLogOpts)
	podLogs, err := req.Stream(context.TODO())
	if err != nil {
		return nil, err
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func (c *k8sClient) IsMetalProvisioningExists() (bool, error) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "metal3.io",
		Kind:    "Provisioning",
		Version: "v1alpha1",
	})
	err := c.runtimeClient.Get(context.Background(), runtimeclient.ObjectKey{
		Name: "provisioning-configuration",
	}, u)

	if apierrors.IsNotFound(err) {
		c.log.Infof("Baremetal provisioning CR is not found")
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

func (c *k8sClient) ListBMHs() (metal3v1alpha1.BareMetalHostList, error) {
	hosts := metal3v1alpha1.BareMetalHostList{}
	opts := &runtimeclient.ListOptions{
		Namespace: "openshift-machine-api",
	}

	err := c.runtimeClient.List(context.Background(), &hosts, opts)
	if err != nil {
		c.log.Errorf("failed to list BMHs, error %s", err)
		return metal3v1alpha1.BareMetalHostList{}, err
	}
	return hosts, nil
}

func (c *k8sClient) GetBMH(name string) (*metal3v1alpha1.BareMetalHost, error) {
	host := metal3v1alpha1.BareMetalHost{}
	nn := types.NamespacedName{Namespace: "openshift-machine-api", Name: name}
	err := c.runtimeClient.Get(context.Background(), nn, &host)
	if err != nil {
		c.log.Errorf("failed to Get BMH %s, error %s", name, err)
		return &metal3v1alpha1.BareMetalHost{}, err
	}
	return &host, nil

}

func (c *k8sClient) UpdateBMHStatus(bmh *metal3v1alpha1.BareMetalHost) error {
	return c.runtimeClient.Status().Update(context.TODO(), bmh)
}

func (c *k8sClient) UpdateBMH(bmh *metal3v1alpha1.BareMetalHost) error {
	return c.runtimeClient.Update(context.TODO(), bmh)
}

func (c *k8sClient) GetClusterVersion() (*configv1.ClusterVersion, error) {
	result := &configv1.ClusterVersion{}
	err := c.client.RESTClient().Get().
		AbsPath("/apis/config.openshift.io/v1").
		Resource("clusterversions").
		Name("version").
		Do(context.Background()).
		Into(result)
	return result, err
}

func (c *k8sClient) ListClusterOperators() (*configv1.ClusterOperatorList, error) {
	return c.configClient.ClusterOperators().List(context.TODO(), metav1.ListOptions{})
}

func (c *k8sClient) GetClusterOperator(name string) (*configv1.ClusterOperator, error) {
	return c.configClient.ClusterOperators().Get(context.TODO(), name, metav1.GetOptions{})
}

func (c *k8sClient) CreateEvent(namespace, name, message, component string) (*v1.Event, error) {
	currentTime := metav1.Time{Time: time.Now()}
	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		InvolvedObject: v1.ObjectReference{
			Namespace: namespace,
			Name:      component,
		},
		Message:        message,
		Count:          1,
		FirstTimestamp: currentTime,
		LastTimestamp:  currentTime,
		Type:           v1.EventTypeNormal,
		Reason:         name,
	}

	return c.client.CoreV1().Events(namespace).Create(context.TODO(), event, metav1.CreateOptions{})
}

func (c *k8sClient) GetCSVFromSubscription(namespace string, name string) (string, error) {
	subscription, err := c.olmClient.Subscriptions(namespace).Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		c.log.WithError(err).Warnf("Failed to get subscription %s in %s", name, namespace)
		return "", err
	}
	if subscription.Status.CurrentCSV == "" {
		c.log.Warnf("subscription CurrentCSV is empty, subscription status: %+v", subscription.Status)
		return subscription.Status.CurrentCSV, fmt.Errorf("subscription CurrentCSV is empty")
	}

	return subscription.Status.CurrentCSV, nil
}

func (c *k8sClient) GetSubscription(subscription types.NamespacedName) (*olmv1alpha1.Subscription, error) {
	result, err := c.olmClient.Subscriptions(subscription.Namespace).Get(context.TODO(), subscription.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (c *k8sClient) GetAllInstallPlansOfSubscription(subscription types.NamespacedName) ([]olmv1alpha1.InstallPlan, error) {
	allInstallPlans, err := c.olmClient.InstallPlans(subscription.Namespace).List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	var installPlansOwnedBySubscription []olmv1alpha1.InstallPlan
	for _, plan := range allInstallPlans.Items {
		for _, owner := range plan.OwnerReferences {
			if owner.Name == subscription.Name {
				installPlansOwnedBySubscription = append(installPlansOwnedBySubscription, plan)
			}
		}
	}

	return installPlansOwnedBySubscription, nil
}

func (c *k8sClient) DeleteInstallPlan(installPlan types.NamespacedName) error {
	return c.olmClient.InstallPlans(installPlan.Namespace).Delete(context.TODO(),
		installPlan.Name, metav1.DeleteOptions{})
}

func (c *k8sClient) GetNode(name string) (*v1.Node, error) {
	node, err := c.client.CoreV1().Nodes().Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return &v1.Node{}, err
	}
	return node, nil
}

func (c *k8sClient) PatchNodeLabels(nodeName string, nodeLabels string) error {
	data := []byte(`{"metadata": {"labels": ` + nodeLabels + `}}`)
	_, err := c.client.CoreV1().Nodes().Patch(context.Background(), nodeName, types.MergePatchType, data, metav1.PatchOptions{})
	return err
}

func (c *k8sClient) IsClusterCapabilityEnabled(capability configv1.ClusterVersionCapability) (bool, error) {
	version, err := c.GetClusterVersion()
	if err != nil {
		return false, err
	}
	isExplicitlyEnabled := funk.Contains(version.Status.Capabilities.EnabledCapabilities, capability)
	isKnown := funk.Contains(version.Status.Capabilities.KnownCapabilities, capability)
	return isExplicitlyEnabled || !isKnown, nil
}

type taintRemovePatch struct {
	Op   string `json:"op"`
	Path string `json:"path"`
}

func (c *k8sClient) UntaintNode(name string) error {
	node, err := c.client.CoreV1().Nodes().Get(context.TODO(), name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	foundTaintIndex := -1
	for i, taint := range node.Spec.Taints {
		if taint.Key == UNINITIALIZED_TAINT_KEY {
			foundTaintIndex = i
			break
		}
	}
	if foundTaintIndex == -1 {
		c.log.Infof("No uninitialized taint found for node %s", name)
		return nil
	}
	c.log.Infof("Removing uninitialized taint on node %s", name)
	patch := []taintRemovePatch{{
		Op:   "remove",
		Path: fmt.Sprintf("/spec/taints/%d", foundTaintIndex),
	}}
	c.log.Debugf("Prepared patch: %+v", patch)
	data, err := json.Marshal(patch)
	if err != nil {
		return errors.Wrap(err, "Failed to convert patch to JSON")
	}
	c.log.Debugf("Marshaled patch into %+v", string(data))
	result, err := c.client.CoreV1().Nodes().Patch(context.TODO(), node.Name, types.JSONPatchType, data, metav1.PatchOptions{})
	if err != nil {
		return errors.Wrap(err, "Failed to patch node")
	}
	c.log.Debugf("Node patch result: %+v", result)
	c.log.Infof("Removed taint from node %s", node.Name)
	return err
}

func (c *k8sClient) PatchMachineConfigPoolPaused(pause bool, mcpName string) error {
	mcp := &mcfgv1.MachineConfigPool{}
	err := c.runtimeClient.Get(context.TODO(), types.NamespacedName{Name: mcpName}, mcp)
	if err != nil {
		return err
	}
	if mcp.Spec.Paused == pause {
		return nil
	}
	pausePatch := []byte(fmt.Sprintf("{\"spec\":{\"paused\":%t}}", pause))
	c.log.Infof("Setting pause MCP %s to %t", mcpName, pause)
	return c.runtimeClient.Patch(context.TODO(), mcp, runtimeclient.RawPatch(types.MergePatchType, pausePatch))
}
