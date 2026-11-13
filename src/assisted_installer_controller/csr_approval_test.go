package assisted_installer_controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"time"

	"github.com/go-openapi/strfmt"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/csr_approver"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	certificatesv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func controllerTestSnapshot() csr_approver.Snapshot {
	snapshot, err := csr_approver.NewSnapshot("cluster-id", []csr_approver.HostInput{{
		ID:        "host-1",
		Names:     []string{"node-1"},
		Addresses: []string{"10.0.0.1"},
	}}, time.Now().UTC())
	if err != nil {
		panic(err)
	}
	return snapshot
}

func controllerTestClientCSR() certificatesv1.CertificateSigningRequest {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   "system:node:node-1",
			Organization: []string{"system:nodes"},
		},
	}, key)
	if err != nil {
		panic(err)
	}
	return certificatesv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{Name: "valid-client"},
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request}),
			SignerName: certificatesv1.KubeAPIServerClientKubeletSignerName,
			Username:   csr_approver.NodeBootstrapperUsername,
			Groups: []string{
				"system:authenticated",
				"system:serviceaccounts:openshift-machine-config-operator",
				"system:serviceaccounts",
			},
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageKeyEncipherment,
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageClientAuth,
			},
		},
	}
}

var _ = Describe("CSR approval snapshot", func() {
	It("survives controller restart", func() {
		store := &memoryCSRApprovalStore{}
		config := ControllerConfig{ClusterID: "cluster-id", Namespace: "assisted-installer"}
		logger := logrus.New()
		first := newController(logger, config, nil, nil, nil)
		first.csrStore = store

		hostID := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		hosts := map[string]inventory_client.HostData{
			"node-1": {
				Host: &models.Host{ID: &hostID, RequestedHostname: "node-1"},
				IPs:  []string{"10.0.0.1"},
			},
		}
		Expect(first.refreshCSRApprovalSnapshot(hosts)).To(Succeed())

		second := newController(logger, config, nil, nil, nil)
		second.csrStore = store
		second.loadCSRApprovalSnapshot()
		loaded, ok := second.csrState.get()
		Expect(ok).To(BeTrue())
		Expect(loaded.ClusterID).To(Equal(config.ClusterID))
		Expect(loaded.Hosts).To(HaveLen(1))
	})

	It("uses requested hostname before discovered hostname and falls back when needed", func() {
		requestedID := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		discoveredID := strfmt.UUID("eb82821f-bf21-4614-9a3b-ecb07929f238")
		hosts := map[string]inventory_client.HostData{
			"requested": {
				Host:      &models.Host{ID: &requestedID, RequestedHostname: "requested"},
				Inventory: &models.Inventory{Hostname: "discovered"},
			},
			"discovered": {
				Host:      &models.Host{ID: &discoveredID},
				Inventory: &models.Inventory{Hostname: "discovered-only"},
			},
		}

		snapshot, err := buildCSRApprovalSnapshot("cluster-id", hosts)
		Expect(err).NotTo(HaveOccurred())
		requested, ok := snapshot.HostForName("requested")
		Expect(ok).To(BeTrue())
		Expect(requested.Names).To(Equal([]string{"requested"}))
		discovered, ok := snapshot.HostForName("discovered-only")
		Expect(ok).To(BeTrue())
		Expect(discovered.Names).To(Equal([]string{"discovered-only"}))
	})

	It("rejects incomplete host data", func() {
		validID := strfmt.UUID("7916fa89-ea7a-443e-a862-b3e930309f65")
		snapshot, err := buildCSRApprovalSnapshot("cluster-id", map[string]inventory_client.HostData{
			"valid":   {Host: &models.Host{ID: &validID, RequestedHostname: "valid"}},
			"invalid": {},
		})
		Expect(err).To(HaveOccurred())
		Expect(snapshot.Version).To(Equal(0))
	})
})

type memoryCSRApprovalStore struct {
	snapshot csr_approver.Snapshot
	has      bool
}

func (s *memoryCSRApprovalStore) Load() (csr_approver.Snapshot, error) {
	if !s.has {
		return csr_approver.Snapshot{}, fmt.Errorf("not initialized")
	}
	return s.snapshot, nil
}

func (s *memoryCSRApprovalStore) Save(snapshot csr_approver.Snapshot) error {
	s.snapshot = snapshot
	s.has = true
	return nil
}
