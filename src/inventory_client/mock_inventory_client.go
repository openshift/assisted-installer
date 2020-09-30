// Code generated by MockGen. DO NOT EDIT.
// Source: inventory_client.go

// Package inventory_client is a generated GoMock package.
package inventory_client

import (
	io "io"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
	models "github.com/openshift/assisted-service/models"
)

// MockInventoryClient is a mock of InventoryClient interface
type MockInventoryClient struct {
	ctrl     *gomock.Controller
	recorder *MockInventoryClientMockRecorder
}

// MockInventoryClientMockRecorder is the mock recorder for MockInventoryClient
type MockInventoryClientMockRecorder struct {
	mock *MockInventoryClient
}

// NewMockInventoryClient creates a new mock instance
func NewMockInventoryClient(ctrl *gomock.Controller) *MockInventoryClient {
	mock := &MockInventoryClient{ctrl: ctrl}
	mock.recorder = &MockInventoryClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockInventoryClient) EXPECT() *MockInventoryClientMockRecorder {
	return m.recorder
}

// DownloadFile mocks base method
func (m *MockInventoryClient) DownloadFile(filename, dest string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DownloadFile", filename, dest)
	ret0, _ := ret[0].(error)
	return ret0
}

// DownloadFile indicates an expected call of DownloadFile
func (mr *MockInventoryClientMockRecorder) DownloadFile(filename, dest interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DownloadFile", reflect.TypeOf((*MockInventoryClient)(nil).DownloadFile), filename, dest)
}

// UpdateHostInstallProgress mocks base method
func (m *MockInventoryClient) UpdateHostInstallProgress(hostId string, newStage models.HostStage, info string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UpdateHostInstallProgress", hostId, newStage, info)
	ret0, _ := ret[0].(error)
	return ret0
}

// UpdateHostInstallProgress indicates an expected call of UpdateHostInstallProgress
func (mr *MockInventoryClientMockRecorder) UpdateHostInstallProgress(hostId, newStage, info interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UpdateHostInstallProgress", reflect.TypeOf((*MockInventoryClient)(nil).UpdateHostInstallProgress), hostId, newStage, info)
}

// GetEnabledHostsNamesHosts mocks base method
func (m *MockInventoryClient) GetEnabledHostsNamesHosts() (map[string]HostData, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetEnabledHostsNamesHosts")
	ret0, _ := ret[0].(map[string]HostData)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetEnabledHostsNamesHosts indicates an expected call of GetEnabledHostsNamesHosts
func (mr *MockInventoryClientMockRecorder) GetEnabledHostsNamesHosts() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetEnabledHostsNamesHosts", reflect.TypeOf((*MockInventoryClient)(nil).GetEnabledHostsNamesHosts))
}

// UploadIngressCa mocks base method
func (m *MockInventoryClient) UploadIngressCa(ingressCA, clusterId string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UploadIngressCa", ingressCA, clusterId)
	ret0, _ := ret[0].(error)
	return ret0
}

// UploadIngressCa indicates an expected call of UploadIngressCa
func (mr *MockInventoryClientMockRecorder) UploadIngressCa(ingressCA, clusterId interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UploadIngressCa", reflect.TypeOf((*MockInventoryClient)(nil).UploadIngressCa), ingressCA, clusterId)
}

// GetCluster mocks base method
func (m *MockInventoryClient) GetCluster() (*models.Cluster, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetCluster")
	ret0, _ := ret[0].(*models.Cluster)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetCluster indicates an expected call of GetCluster
func (mr *MockInventoryClientMockRecorder) GetCluster() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetCluster", reflect.TypeOf((*MockInventoryClient)(nil).GetCluster))
}

// CompleteInstallation mocks base method
func (m *MockInventoryClient) CompleteInstallation(clusterId string, isSuccess bool, errorInfo string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "CompleteInstallation", clusterId, isSuccess, errorInfo)
	ret0, _ := ret[0].(error)
	return ret0
}

// CompleteInstallation indicates an expected call of CompleteInstallation
func (mr *MockInventoryClientMockRecorder) CompleteInstallation(clusterId, isSuccess, errorInfo interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "CompleteInstallation", reflect.TypeOf((*MockInventoryClient)(nil).CompleteInstallation), clusterId, isSuccess, errorInfo)
}

// GetHosts mocks base method
func (m *MockInventoryClient) GetHosts(skippedStatuses []string) (map[string]HostData, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetHosts", skippedStatuses)
	ret0, _ := ret[0].(map[string]HostData)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetHosts indicates an expected call of GetHosts
func (mr *MockInventoryClientMockRecorder) GetHosts(skippedStatuses interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetHosts", reflect.TypeOf((*MockInventoryClient)(nil).GetHosts), skippedStatuses)
}

// UploadLogs mocks base method
func (m *MockInventoryClient) UploadLogs(clusterId string, logsType models.LogsType, upfile io.ReadCloser) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "UploadLogs", clusterId, logsType, upfile)
	ret0, _ := ret[0].(error)
	return ret0
}

// UploadLogs indicates an expected call of UploadLogs
func (mr *MockInventoryClientMockRecorder) UploadLogs(clusterId, logsType, upfile interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "UploadLogs", reflect.TypeOf((*MockInventoryClient)(nil).UploadLogs), clusterId, logsType, upfile)
}
