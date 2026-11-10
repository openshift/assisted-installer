package csr_approver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestValidator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "csr_approver")
}

var _ = Describe("CSR approval policy", func() {
	It("approves a valid client CSR", func() {
		csr := clientCSR("node-1")
		decision := Evaluate(&csr, &corev1.NodeList{}, testSnapshot())
		Expect(decision.Approve).To(BeTrue(), decision.Reason)
	})

	It("accepts the modern client usage set without key encipherment", func() {
		csr := clientCSR("node-1")
		csr.Spec.Usages = []certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageClientAuth,
		}
		decision := Evaluate(&csr, &corev1.NodeList{}, testSnapshot())
		Expect(decision.Approve).To(BeTrue(), decision.Reason)
	})

	Context("client CSR rejection", func() {
		It("rejects administrative identities", func() {
			csr := clientCSR("node-1")
			csr.Spec.Request = createCSR(pkix.Name{CommonName: "system:admin"}, nil, nil)
			Expect(Evaluate(&csr, &corev1.NodeList{}, testSnapshot()).Approve).To(BeFalse())
		})

		It("rejects unsupported signers", func() {
			csr := clientCSR("node-1")
			csr.Spec.SignerName = certificatesv1.KubeletServingSignerName
			Expect(Evaluate(&csr, &corev1.NodeList{}, testSnapshot()).Approve).To(BeFalse())
		})

		It("rejects an existing node", func() {
			csr := clientCSR("node-1")
			nodes := &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1ObjectMeta("node-1")}}}
			Expect(Evaluate(&csr, nodes, testSnapshot()).Approve).To(BeFalse())
		})

		It("rejects invalid bootstrap groups", func() {
			csr := clientCSR("node-1")
			csr.Spec.Groups = []string{"system:authenticated"}
			Expect(Evaluate(&csr, &corev1.NodeList{}, testSnapshot()).Approve).To(BeFalse())
		})

		It("rejects client SANs", func() {
			csr := clientCSR("node-1")
			csr.Spec.Request = createCSR(pkix.Name{
				CommonName:   "system:node:node-1",
				Organization: []string{NodeGroup},
			}, []string{"node-1"}, nil)
			Expect(Evaluate(&csr, &corev1.NodeList{}, testSnapshot()).Approve).To(BeFalse())
		})
	})

	It("accepts current serving usage set", func() {
		nodes := &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1ObjectMeta("node-1")}}}
		csr := servingCSR("node-1", []certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageServerAuth,
		}, []string{"node-1", "node-1.example"}, []net.IP{net.ParseIP("10.0.0.1")})
		decision := Evaluate(&csr, nodes, testSnapshot())
		Expect(decision.Approve).To(BeTrue(), decision.Reason)
	})

	It("accepts legacy serving usage set", func() {
		nodes := &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1ObjectMeta("node-1")}}}
		csr := servingCSR("node-1", []certificatesv1.KeyUsage{
			certificatesv1.UsageDigitalSignature,
			certificatesv1.UsageKeyEncipherment,
			certificatesv1.UsageServerAuth,
		}, []string{"node-1", "node-1.example"}, []net.IP{net.ParseIP("10.0.0.1")})
		decision := Evaluate(&csr, nodes, testSnapshot())
		Expect(decision.Approve).To(BeTrue(), decision.Reason)
	})

	Context("serving CSR rejection", func() {
		It("rejects unknown IP addresses", func() {
			nodes := &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1ObjectMeta("node-1")}}}
			csr := servingCSR("node-1", []certificatesv1.KeyUsage{
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageServerAuth,
			}, []string{"node-1"}, []net.IP{net.ParseIP("10.0.0.2")})
			Expect(Evaluate(&csr, nodes, testSnapshot()).Approve).To(BeFalse())
		})

		It("rejects unknown node identities", func() {
			nodes := &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1ObjectMeta("node-1")}}}
			csr := servingCSR("unknown", []certificatesv1.KeyUsage{
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageServerAuth,
			}, nil, nil)
			Expect(Evaluate(&csr, nodes, testSnapshot()).Approve).To(BeFalse())
		})
	})

	Context("client CSR enrollment window", func() {
		var (
			enrollmentStartedAt time.Time
			snapshot            Snapshot
		)

		BeforeEach(func() {
			enrollmentStartedAt = time.Now().UTC().Add(-time.Minute)
			var err error
			snapshot, err = NewSnapshot("cluster-id", []HostInput{{
				ID:                  "host-1",
				Names:               []string{"node-1"},
				EnrollmentStartedAt: enrollmentStartedAt,
			}}, time.Now().UTC())
			Expect(err).NotTo(HaveOccurred())
		})

		It("accepts a CSR created during enrollment", func() {
			csr := clientCSR("node-1")
			csr.CreationTimestamp = metav1.NewTime(enrollmentStartedAt.Add(time.Minute))
			Expect(Evaluate(&csr, &corev1.NodeList{}, snapshot).Approve).To(BeTrue())
		})

		It("rejects a CSR created before enrollment", func() {
			csr := clientCSR("node-1")
			csr.CreationTimestamp = metav1.NewTime(enrollmentStartedAt.Add(-time.Minute))
			Expect(Evaluate(&csr, &corev1.NodeList{}, snapshot).Approve).To(BeFalse())
		})

		It("rejects a CSR created after the enrollment window", func() {
			csr := clientCSR("node-1")
			csr.CreationTimestamp = metav1.NewTime(enrollmentStartedAt.Add(2*time.Hour + time.Minute))
			Expect(Evaluate(&csr, &corev1.NodeList{}, snapshot).Approve).To(BeFalse())
		})
	})

	It("validates and round-trips snapshots", func() {
		snapshot := testSnapshot()
		encoded, err := json.Marshal(snapshot)
		Expect(err).NotTo(HaveOccurred())
		var decoded Snapshot
		Expect(json.Unmarshal(encoded, &decoded)).To(Succeed())
		Expect(snapshot.Equivalent(decoded)).To(BeTrue())
		Expect(decoded.Validate("cluster-id")).To(Succeed())

		_, err = NewSnapshot("cluster-id", []HostInput{
			{ID: "host-1", Names: []string{"node-1"}},
			{ID: "host-2", Names: []string{"node-1"}},
		}, time.Now())
		Expect(err).To(HaveOccurred())
	})
})

func testSnapshot() Snapshot {
	snapshot, err := NewSnapshot("cluster-id", []HostInput{{
		ID:        "host-1",
		Names:     []string{"node-1", "node-1.example"},
		Addresses: []string{"10.0.0.1"},
	}}, time.Now().UTC())
	if err != nil {
		panic(err)
	}
	return snapshot
}

func clientCSR(nodeName string) certificatesv1.CertificateSigningRequest {
	return certificatesv1.CertificateSigningRequest{
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    createCSR(pkix.Name{CommonName: NodeUserPrefix + nodeName, Organization: []string{NodeGroup}}, nil, nil),
			SignerName: certificatesv1.KubeAPIServerClientKubeletSignerName,
			Username:   NodeBootstrapperUsername,
			Groups:     append([]string(nil), nodeBootstrapperGroups...),
			Usages: []certificatesv1.KeyUsage{
				certificatesv1.UsageKeyEncipherment,
				certificatesv1.UsageDigitalSignature,
				certificatesv1.UsageClientAuth,
			},
		},
	}
}

func servingCSR(nodeName string, usages []certificatesv1.KeyUsage, dnsNames []string, ipAddresses []net.IP) certificatesv1.CertificateSigningRequest {
	return certificatesv1.CertificateSigningRequest{
		Spec: certificatesv1.CertificateSigningRequestSpec{
			Request:    createCSR(pkix.Name{CommonName: NodeUserPrefix + nodeName, Organization: []string{NodeGroup}}, dnsNames, ipAddresses),
			SignerName: certificatesv1.KubeletServingSignerName,
			Username:   NodeUserPrefix + nodeName,
			Groups:     []string{"system:authenticated", NodeGroup},
			Usages:     usages,
		},
	}
}

func createCSR(subject pkix.Name, dnsNames []string, ipAddresses []net.IP) []byte {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	request, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:     subject,
		DNSNames:    dnsNames,
		IPAddresses: ipAddresses,
	}, key)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: request})
}

func metav1ObjectMeta(name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name}
}
