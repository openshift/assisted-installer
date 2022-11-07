// Code generated by MockGen. DO NOT EDIT.
// Source: k8s_client.go

// Package k8s_client is a generated GoMock package.
package k8s_client

import (
	bytes "bytes"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	v1 "github.com/openshift/api/config/v1"
	ops "github.com/openshift/assisted-installer/src/ops"
	v1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	v1alpha10 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	v10 "k8s.io/api/batch/v1"
	v11 "k8s.io/api/certificates/v1"
	v12 "k8s.io/api/core/v1"
	types "k8s.io/apimachinery/pkg/types"
)

// MockK8SClient is a mock of K8SClient interface
type MockK8SClient struct {
	ctrl     *gomock.Controller
	recorder *MockK8SClientMockRecorder
}

// MockK8SClientMockRecorder is the mock recorder for MockK8SClient
type MockK8SClientMockRecorder struct {
	mock *MockK8SClient
}

// NewMockK8SClient creates a new mock instance
func NewMockK8SClient(ctrl *gomock.Controller) *MockK8SClient {
	mock := &MockK8SClient{ctrl: ctrl}
	mock.recorder = &MockK8SClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockK8SClient) EXPECT() *MockK8SClientMockRecorder {
	return m.recorder
}

// ListMasterNodes mocks base method
func (m *MockK8SClient) ListMasterNodes() (*v12.NodeList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListMasterNodes")
	ret0, _ := ret[0].(*v12.NodeList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListMasterNodes indicates an expected call of ListMasterNodes
func (mr *MockK8SClientMockRecorder) ListMasterNodes() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListMasterNodes", reflect.TypeOf((*MockK8SClient)(nil).ListMasterNodes))
}

// EnableRouterAccessLogs mocks base method
func (m *MockK8SClient) EnableRouterAccessLogs() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "EnableRouterAccessLogs")
	ret0, _ := ret[0].(error)
	return ret0
}

// EnableRouterAccessLogs indicates an expected call of EnableRouterAccessLogs
func (mr *MockK8SClientMockRecorder) EnableRouterAccessLogs() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "EnableRouterAccessLogs", reflect.TypeOf((*MockK8SClient)(nil).EnableRouterAccessLogs))
}

// ListNodes mocks base method
func (m *MockK8SClient) ListNodes() (*v12.NodeList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListNodes")
	ret0, _ := ret[0].(*v12.NodeList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListNodes indicates an expected call of ListNodes
func (mr *MockK8SClientMockRecorder) ListNodes() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListNodes", reflect.TypeOf((*MockK8SClient)(nil).ListNodes))
}

// ListMachines mocks base method
func (m *MockK8SClient) ListMachines() (*v1beta1.MachineList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListMachines")
	ret0, _ := ret[0].(*v1beta1.MachineList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListMachines indicates an expected call of ListMachines
func (mr *MockK8SClientMockRecorder) ListMachines() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListMachines", reflect.TypeOf((*MockK8SClient)(nil).ListMachines))
}

// RunOCctlCommand mocks base method
func (m *MockK8SClient) RunOCctlCommand(args []string, kubeconfigPath string, o ops.Ops) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RunOCctlCommand", args, kubeconfigPath, o)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// RunOCctlCommand indicates an expected call of RunOCctlCommand
func (mr *MockK8SClientMockRecorder) RunOCctlCommand(args, kubeconfigPath, o interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RunOCctlCommand", reflect.TypeOf((*MockK8SClient)(nil).RunOCctlCommand), args, kubeconfigPath, o)
}

// ApproveCsr mocks base method
func (m *MockK8SClient) ApproveCsr(csr *v11.CertificateSigningRequest) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ApproveCsr", csr)
	ret0, _ := ret[0].(error)
	return ret0
}

// ApproveCsr indicates an expected call of ApproveCsr
func (mr *MockK8SClientMockRecorder) ApproveCsr(csr interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ApproveCsr", reflect.TypeOf((*MockK8SClient)(nil).ApproveCsr), csr)
}

// ListCsrs mocks base method
func (m *MockK8SClient) ListCsrs() (*v11.CertificateSigningRequestList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListCsrs")
	ret0, _ := ret[0].(*v11.CertificateSigningRequestList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListCsrs indicates an expected call of ListCsrs
func (mr *MockK8SClientMockRecorder) ListCsrs() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListCsrs", reflect.TypeOf((*MockK8SClient)(nil).ListCsrs))
}

// GetConfigMap mocks base method
func (m *MockK8SClient) GetConfigMap(namespace, name string) (*v12.ConfigMap, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetConfigMap", namespace, name)
	ret0, _ := ret[0].(*v12.ConfigMap)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetConfigMap indicates an expected call of GetConfigMap
func (mr *MockK8SClientMockRecorder) GetConfigMap(namespace, name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetConfigMap", reflect.TypeOf((*MockK8SClient)(nil).GetConfigMap), namespace, name)
}

// GetPodLogs mocks base method
func (m *MockK8SClient) GetPodLogs(namespace, podName string, sinceSeconds int64) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPodLogs", namespace, podName, sinceSeconds)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPodLogs indicates an expected call of GetPodLogs
func (mr *MockK8SClientMockRecorder) GetPodLogs(namespace, podName, sinceSeconds interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPodLogs", reflect.TypeOf((*MockK8SClient)(nil).GetPodLogs), namespace, podName, sinceSeconds)
}

// GetPodLogsAsBuffer mocks base method
func (m *MockK8SClient) GetPodLogsAsBuffer(namespace, podName string, sinceSeconds int64) (*bytes.Buffer, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPodLogsAsBuffer", namespace, podName, sinceSeconds)
	ret0, _ := ret[0].(*bytes.Buffer)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPodLogsAsBuffer indicates an expected call of GetPodLogsAsBuffer
func (mr *MockK8SClientMockRecorder) GetPodLogsAsBuffer(namespace, podName, sinceSeconds interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPodLogsAsBuffer", reflect.TypeOf((*MockK8SClient)(nil).GetPodLogsAsBuffer), namespace, podName, sinceSeconds)
}

// GetPods mocks base method
func (m *MockK8SClient) GetPods(namespace string, labelMatch map[string]string, fieldSelector string) ([]v12.Pod, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPods", namespace, labelMatch, fieldSelector)
	ret0, _ := ret[0].([]v12.Pod)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPods indicates an expected call of GetPods
func (mr *MockK8SClientMockRecorder) GetPods(namespace, labelMatch, fieldSelector interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPods", reflect.TypeOf((*MockK8SClient)(nil).GetPods), namespace, labelMatch, fieldSelector)
}

// GetCSV mocks base method
func (m *MockK8SClient) GetCSV(namespace, name string) (*v1alpha10.ClusterServiceVersion, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetCSV", namespace, name)
	ret0, _ := ret[0].(*v1alpha10.ClusterServiceVersion)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetCSV indicates an expected call of GetCSV
func (mr *MockK8SClientMockRecorder) GetCSV(namespace, name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetCSV", reflect.TypeOf((*MockK8SClient)(nil).GetCSV), namespace, name)
}

// GetCSVFromSubscription mocks base method
func (m *MockK8SClient) GetCSVFromSubscription(namespace, name string) (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetCSVFromSubscription", namespace, name)
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetCSVFromSubscription indicates an expected call of GetCSVFromSubscription
func (mr *MockK8SClientMockRecorder) GetCSVFromSubscription(namespace, name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetCSVFromSubscription", reflect.TypeOf((*MockK8SClient)(nil).GetCSVFromSubscription), namespace, name)
}

// GetSubscription mocks base method
func (m *MockK8SClient) GetSubscription(subscription types.NamespacedName) (*v1alpha10.Subscription, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetSubscription", subscription)
	ret0, _ := ret[0].(*v1alpha10.Subscription)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetSubscription indicates an expected call of GetSubscription
func (mr *MockK8SClientMockRecorder) GetSubscription(subscription interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetSubscription", reflect.TypeOf((*MockK8SClient)(nil).GetSubscription), subscription)
}

// GetAllInstallPlansOfSubscription mocks base method
func (m *MockK8SClient) GetAllInstallPlansOfSubscription(subscription types.NamespacedName) ([]v1alpha10.InstallPlan, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetAllInstallPlansOfSubscription", subscription)
	ret0, _ := ret[0].([]v1alpha10.InstallPlan)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetAllInstallPlansOfSubscription indicates an expected call of GetAllInstallPlansOfSubscription
func (mr *MockK8SClientMockRecorder) GetAllInstallPlansOfSubscription(subscription interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetAllInstallPlansOfSubscription", reflect.TypeOf((*MockK8SClient)(nil).GetAllInstallPlansOfSubscription), subscription)
}

// DeleteInstallPlan mocks base method
func (m *MockK8SClient) DeleteInstallPlan(installPlan types.NamespacedName) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteInstallPlan", installPlan)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteInstallPlan indicates an expected call of DeleteInstallPlan
func (mr *MockK8SClientMockRecorder) DeleteInstallPlan(installPlan interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteInstallPlan", reflect.TypeOf((*MockK8SClient)(nil).DeleteInstallPlan), installPlan)
}

// IsMetalProvisioningExists mocks base method
func (m *MockK8SClient) IsMetalProvisioningExists() (bool, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IsMetalProvisioningExists")
	ret0, _ := ret[0].(bool)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// IsMetalProvisioningExists indicates an expected call of IsMetalProvisioningExists
func (mr *MockK8SClientMockRecorder) IsMetalProvisioningExists() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IsMetalProvisioningExists", reflect.TypeOf((*MockK8SClient)(nil).IsMetalProvisioningExists))
}

// ListBMHs mocks base method
func (m *MockK8SClient) ListBMHs() (v1alpha1.BareMetalHostList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListBMHs")
	ret0, _ := ret[0].(v1alpha1.BareMetalHostList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListBMHs indicates an expected call of ListBMHs
func (mr *MockK8SClientMockRecorder) ListBMHs() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListBMHs", reflect.TypeOf((*MockK8SClient)(nil).ListBMHs))
}

// GetBMH mocks base method
func (m *MockK8SClient) GetBMH(name string) (*v1alpha1.BareMetalHost, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetBMH", name)
	ret0, _ := ret[0].(*v1alpha1.BareMetalHost)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetBMH indicates an expected call of GetBMH
func (mr *MockK8SClientMockRecorder) GetBMH(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetBMH", reflect.TypeOf((*MockK8SClient)(nil).GetBMH), name)
}

// UpdateBMHStatus mocks base method
func (m *MockK8SClient) UpdateBMHStatus(bmh *v1alpha1.BareMetalHost) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateBMHStatus", bmh)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateBMHStatus indicates an expected call of UpdateBMHStatus
func (mr *MockK8SClientMockRecorder) UpdateBMHStatus(bmh interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateBMHStatus", reflect.TypeOf((*MockK8SClient)(nil).UpdateBMHStatus), bmh)
}

// UpdateBMH mocks base method
func (m *MockK8SClient) UpdateBMH(bmh *v1alpha1.BareMetalHost) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateBMH", bmh)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateBMH indicates an expected call of UpdateBMH
func (mr *MockK8SClientMockRecorder) UpdateBMH(bmh interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateBMH", reflect.TypeOf((*MockK8SClient)(nil).UpdateBMH), bmh)
}

// SetProxyEnvVars mocks base method
func (m *MockK8SClient) SetProxyEnvVars() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "SetProxyEnvVars")
	ret0, _ := ret[0].(error)
	return ret0
}

// SetProxyEnvVars indicates an expected call of SetProxyEnvVars
func (mr *MockK8SClientMockRecorder) SetProxyEnvVars() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "SetProxyEnvVars", reflect.TypeOf((*MockK8SClient)(nil).SetProxyEnvVars))
}

// GetClusterVersion mocks base method
func (m *MockK8SClient) GetClusterVersion(name string) (*v1.ClusterVersion, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetClusterVersion", name)
	ret0, _ := ret[0].(*v1.ClusterVersion)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetClusterVersion indicates an expected call of GetClusterVersion
func (mr *MockK8SClientMockRecorder) GetClusterVersion(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetClusterVersion", reflect.TypeOf((*MockK8SClient)(nil).GetClusterVersion), name)
}

// GetServiceNetworks mocks base method
func (m *MockK8SClient) GetServiceNetworks() ([]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetServiceNetworks")
	ret0, _ := ret[0].([]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetServiceNetworks indicates an expected call of GetServiceNetworks
func (mr *MockK8SClientMockRecorder) GetServiceNetworks() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetServiceNetworks", reflect.TypeOf((*MockK8SClient)(nil).GetServiceNetworks))
}

// GetControlPlaneReplicas mocks base method
func (m *MockK8SClient) GetControlPlaneReplicas() (int, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetControlPlaneReplicas")
	ret0, _ := ret[0].(int)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetControlPlaneReplicas indicates an expected call of GetControlPlaneReplicas
func (mr *MockK8SClientMockRecorder) GetControlPlaneReplicas() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetControlPlaneReplicas", reflect.TypeOf((*MockK8SClient)(nil).GetControlPlaneReplicas))
}

// ListServices mocks base method
func (m *MockK8SClient) ListServices(namespace string) (*v12.ServiceList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListServices", namespace)
	ret0, _ := ret[0].(*v12.ServiceList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListServices indicates an expected call of ListServices
func (mr *MockK8SClientMockRecorder) ListServices(namespace interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListServices", reflect.TypeOf((*MockK8SClient)(nil).ListServices), namespace)
}

// ListEvents mocks base method
func (m *MockK8SClient) ListEvents(namespace string) (*v12.EventList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListEvents", namespace)
	ret0, _ := ret[0].(*v12.EventList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListEvents indicates an expected call of ListEvents
func (mr *MockK8SClientMockRecorder) ListEvents(namespace interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListEvents", reflect.TypeOf((*MockK8SClient)(nil).ListEvents), namespace)
}

// ListClusterOperators mocks base method
func (m *MockK8SClient) ListClusterOperators() (*v1.ClusterOperatorList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListClusterOperators")
	ret0, _ := ret[0].(*v1.ClusterOperatorList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListClusterOperators indicates an expected call of ListClusterOperators
func (mr *MockK8SClientMockRecorder) ListClusterOperators() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListClusterOperators", reflect.TypeOf((*MockK8SClient)(nil).ListClusterOperators))
}

// GetClusterOperator mocks base method
func (m *MockK8SClient) GetClusterOperator(name string) (*v1.ClusterOperator, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetClusterOperator", name)
	ret0, _ := ret[0].(*v1.ClusterOperator)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetClusterOperator indicates an expected call of GetClusterOperator
func (mr *MockK8SClientMockRecorder) GetClusterOperator(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetClusterOperator", reflect.TypeOf((*MockK8SClient)(nil).GetClusterOperator), name)
}

// CreateEvent mocks base method
func (m *MockK8SClient) CreateEvent(namespace, name, message, component string) (*v12.Event, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CreateEvent", namespace, name, message, component)
	ret0, _ := ret[0].(*v12.Event)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// CreateEvent indicates an expected call of CreateEvent
func (mr *MockK8SClientMockRecorder) CreateEvent(namespace, name, message, component interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CreateEvent", reflect.TypeOf((*MockK8SClient)(nil).CreateEvent), namespace, name, message, component)
}

// DeleteService mocks base method
func (m *MockK8SClient) DeleteService(namespace, name string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteService", namespace, name)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteService indicates an expected call of DeleteService
func (mr *MockK8SClientMockRecorder) DeleteService(namespace, name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteService", reflect.TypeOf((*MockK8SClient)(nil).DeleteService), namespace, name)
}

// DeletePods mocks base method
func (m *MockK8SClient) DeletePods(namespace string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeletePods", namespace)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeletePods indicates an expected call of DeletePods
func (mr *MockK8SClientMockRecorder) DeletePods(namespace interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeletePods", reflect.TypeOf((*MockK8SClient)(nil).DeletePods), namespace)
}

// PatchNamespace mocks base method
func (m *MockK8SClient) PatchNamespace(namespace string, data []byte) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PatchNamespace", namespace, data)
	ret0, _ := ret[0].(error)
	return ret0
}

// PatchNamespace indicates an expected call of PatchNamespace
func (mr *MockK8SClientMockRecorder) PatchNamespace(namespace, data interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PatchNamespace", reflect.TypeOf((*MockK8SClient)(nil).PatchNamespace), namespace, data)
}

// GetNode mocks base method
func (m *MockK8SClient) GetNode(name string) (*v12.Node, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNode", name)
	ret0, _ := ret[0].(*v12.Node)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNode indicates an expected call of GetNode
func (mr *MockK8SClientMockRecorder) GetNode(name interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNode", reflect.TypeOf((*MockK8SClient)(nil).GetNode), name)
}

// PatchNodeLabels mocks base method
func (m *MockK8SClient) PatchNodeLabels(nodeName, nodeLabels string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "PatchNodeLabels", nodeName, nodeLabels)
	ret0, _ := ret[0].(error)
	return ret0
}

// PatchNodeLabels indicates an expected call of PatchNodeLabels
func (mr *MockK8SClientMockRecorder) PatchNodeLabels(nodeName, nodeLabels interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "PatchNodeLabels", reflect.TypeOf((*MockK8SClient)(nil).PatchNodeLabels), nodeName, nodeLabels)
}

// ListJobs mocks base method
func (m *MockK8SClient) ListJobs(namespace string) (*v10.JobList, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "ListJobs", namespace)
	ret0, _ := ret[0].(*v10.JobList)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// ListJobs indicates an expected call of ListJobs
func (mr *MockK8SClientMockRecorder) ListJobs(namespace interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "ListJobs", reflect.TypeOf((*MockK8SClient)(nil).ListJobs), namespace)
}

// DeleteJob mocks base method
func (m *MockK8SClient) DeleteJob(job types.NamespacedName) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DeleteJob", job)
	ret0, _ := ret[0].(error)
	return ret0
}

// DeleteJob indicates an expected call of DeleteJob
func (mr *MockK8SClientMockRecorder) DeleteJob(job interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DeleteJob", reflect.TypeOf((*MockK8SClient)(nil).DeleteJob), job)
}
