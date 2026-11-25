package assisted_installer_controller

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openshift/assisted-installer/src/csr_approver"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
)

type csrApprovalStore interface {
	Load() (csr_approver.Snapshot, error)
	Save(snapshot csr_approver.Snapshot) error
}

type k8sCSRApprovalStore struct {
	kc        k8s_client.K8SClient
	namespace string
	clusterID string
}

func (s *k8sCSRApprovalStore) Load() (csr_approver.Snapshot, error) {
	cm, err := s.kc.GetConfigMap(s.namespace, csr_approver.SnapshotConfigMapName)
	if err != nil {
		return csr_approver.Snapshot{}, err
	}
	data, ok := cm.Data[csr_approver.SnapshotConfigMapKey]
	if !ok || data == "" {
		return csr_approver.Snapshot{}, fmt.Errorf("approval snapshot is not initialized")
	}

	var snapshot csr_approver.Snapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return csr_approver.Snapshot{}, fmt.Errorf("decode approval snapshot: %w", err)
	}
	if err := snapshot.Validate(s.clusterID); err != nil {
		return csr_approver.Snapshot{}, err
	}
	return snapshot, nil
}

func (s *k8sCSRApprovalStore) Save(snapshot csr_approver.Snapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode approval snapshot: %w", err)
	}
	return s.kc.CreateOrPatchConfigMapData(s.namespace, csr_approver.SnapshotConfigMapName, map[string]string{
		csr_approver.SnapshotConfigMapKey: string(data),
	})
}

type csrApprovalState struct {
	lock     sync.RWMutex
	snapshot *csr_approver.Snapshot
}

func (s *csrApprovalState) set(snapshot csr_approver.Snapshot) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.snapshot = &snapshot
}

func (s *csrApprovalState) get() (csr_approver.Snapshot, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	if s.snapshot == nil {
		return csr_approver.Snapshot{}, false
	}
	return *s.snapshot, true
}

func buildCSRApprovalSnapshot(clusterID string, hosts map[string]inventory_client.HostData) (csr_approver.Snapshot, error) {
	inputs := make([]csr_approver.HostInput, 0, len(hosts))
	for _, hostData := range hosts {
		if hostData.Host == nil || hostData.Host.ID == nil {
			return csr_approver.Snapshot{}, fmt.Errorf("host is missing its identity")
		}

		var names []string
		if requestedHostname := strings.TrimSpace(hostData.Host.RequestedHostname); requestedHostname != "" {
			names = []string{requestedHostname}
		} else if hostData.Inventory != nil {
			names = []string{hostData.Inventory.Hostname}
		}
		var enrollmentStartedAt time.Time
		if hostData.Host.Progress != nil {
			enrollmentStartedAt = time.Time(hostData.Host.Progress.StageStartedAt)
		}
		if enrollmentStartedAt.IsZero() {
			enrollmentStartedAt = time.Time(hostData.Host.StageStartedAt)
		}
		inputs = append(inputs, csr_approver.HostInput{
			ID:                  hostData.Host.ID.String(),
			Names:               names,
			Addresses:           hostData.IPs,
			EnrollmentStartedAt: enrollmentStartedAt,
		})
	}
	return csr_approver.NewSnapshot(clusterID, inputs, time.Now().UTC())
}

func (c *controller) loadCSRApprovalSnapshot() {
	if c.csrStore == nil {
		return
	}
	snapshot, err := c.csrStore.Load()
	if err != nil {
		c.log.WithError(err).Warn("CSR approval snapshot is not available")
		return
	}
	c.csrState.set(snapshot)
	c.log.Infof("Loaded CSR approval snapshot for %d hosts", len(snapshot.Hosts))
}

func (c *controller) refreshCSRApprovalSnapshot(hosts map[string]inventory_client.HostData) error {
	if c.csrStore == nil {
		return nil
	}
	snapshot, err := buildCSRApprovalSnapshot(c.ClusterID, hosts)
	if err != nil {
		if _, hasCurrent := c.csrState.get(); hasCurrent {
			c.log.WithError(err).Warn("Failed to refresh CSR approval snapshot; retaining previous snapshot")
			return nil
		}
		return err
	}
	current, hasCurrent := c.csrState.get()
	if hasCurrent && current.Equivalent(snapshot) {
		return nil
	}
	if err := c.csrStore.Save(snapshot); err != nil {
		if hasCurrent {
			c.log.WithError(err).Warn("Failed to refresh CSR approval snapshot; retaining previous snapshot")
			return nil
		}
		return err
	}
	c.csrState.set(snapshot)
	return nil
}
