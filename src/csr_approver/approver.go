package csr_approver

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	SnapshotVersion = 1
	maxClockSkew    = 10 * time.Second
	maxHostWindow   = 2 * time.Hour

	NodeBootstrapperUsername = "system:serviceaccount:openshift-machine-config-operator:node-bootstrapper"
	NodeUserPrefix           = "system:node:"
	NodeGroup                = "system:nodes"

	SnapshotConfigMapName = "assisted-installer-csr-approval"
	SnapshotConfigMapKey  = "approval-context.json"
)

var nodeBootstrapperGroups = []string{
	"system:authenticated",
	"system:serviceaccounts:openshift-machine-config-operator",
	"system:serviceaccounts",
}

// HostInput contains only the host identity data needed for CSR approval.
type HostInput struct {
	ID                  string
	Names               []string
	Addresses           []string
	EnrollmentStartedAt time.Time
}

// ExpectedHost is the durable identity and address record for one install host.
type ExpectedHost struct {
	ID                  string     `json:"id"`
	Names               []string   `json:"names"`
	Addresses           []string   `json:"addresses,omitempty"`
	EnrollmentStartedAt *time.Time `json:"enrollment_started_at,omitempty"`
}

// Snapshot is persisted before the ephemeral Assisted Service can disappear.
type Snapshot struct {
	Version    int            `json:"version"`
	ClusterID  string         `json:"cluster_id"`
	CapturedAt time.Time      `json:"captured_at"`
	Hosts      []ExpectedHost `json:"hosts"`
}

type Decision struct {
	Approve bool
	Reason  string
}

func NewSnapshot(clusterID string, inputs []HostInput, capturedAt time.Time) (Snapshot, error) {
	if clusterID == "" {
		return Snapshot{}, fmt.Errorf("cluster ID is empty")
	}
	if capturedAt.IsZero() {
		return Snapshot{}, fmt.Errorf("capture time is empty")
	}

	hosts := make([]ExpectedHost, 0, len(inputs))
	for _, input := range inputs {
		host := ExpectedHost{
			ID:        input.ID,
			Names:     normalizeNames(input.Names),
			Addresses: normalizeAddresses(input.Addresses),
		}
		if !input.EnrollmentStartedAt.IsZero() {
			enrollmentStartedAt := input.EnrollmentStartedAt.UTC()
			host.EnrollmentStartedAt = &enrollmentStartedAt
		}
		if len(host.Names) == 0 {
			return Snapshot{}, fmt.Errorf("host %q has no node name", input.ID)
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 {
		return Snapshot{}, fmt.Errorf("host list is empty")
	}

	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].Names[0] < hosts[j].Names[0]
	})
	if err := validateHosts(hosts); err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		Version:    SnapshotVersion,
		ClusterID:  clusterID,
		CapturedAt: capturedAt.UTC(),
		Hosts:      hosts,
	}, nil
}

func (s Snapshot) Validate(clusterID string) error {
	if s.Version != SnapshotVersion {
		return fmt.Errorf("unsupported snapshot version %d", s.Version)
	}
	if s.ClusterID == "" || s.ClusterID != clusterID {
		return fmt.Errorf("snapshot cluster ID does not match")
	}
	if s.CapturedAt.IsZero() {
		return fmt.Errorf("snapshot capture time is empty")
	}
	if len(s.Hosts) == 0 {
		return fmt.Errorf("snapshot host list is empty")
	}
	return validateHosts(s.Hosts)
}

func (s Snapshot) Equivalent(other Snapshot) bool {
	return s.Version == other.Version &&
		s.ClusterID == other.ClusterID &&
		expectedHostsEqual(s.Hosts, other.Hosts)
}

func (s Snapshot) HostForName(name string) (ExpectedHost, bool) {
	name = normalizeName(name)
	for _, host := range s.Hosts {
		for _, candidate := range host.Names {
			if candidate == name {
				return host, true
			}
		}
	}
	return ExpectedHost{}, false
}

func Evaluate(csr *certificatesv1.CertificateSigningRequest, nodes *corev1.NodeList, snapshot Snapshot) Decision {
	if csr == nil {
		return reject("nil CSR")
	}
	if hasDecision(csr) {
		return reject("CSR already has a decision")
	}
	if err := snapshot.Validate(snapshot.ClusterID); err != nil {
		return reject("invalid approval snapshot: " + err.Error())
	}

	parsed, err := parseCertificateRequest(csr.Spec.Request)
	if err != nil {
		return reject("malformed certificate request")
	}

	if csr.Spec.SignerName == certificatesv1.KubeAPIServerClientKubeletSignerName {
		return evaluateClientCSR(csr, parsed, nodes, snapshot)
	}
	if csr.Spec.SignerName == certificatesv1.KubeletServingSignerName {
		return evaluateServingCSR(csr, parsed, nodes, snapshot)
	}
	return reject("unsupported signer")
}

func evaluateClientCSR(csr *certificatesv1.CertificateSigningRequest, parsed *x509.CertificateRequest, nodes *corev1.NodeList, snapshot Snapshot) Decision {
	if csr.Spec.Username != NodeBootstrapperUsername {
		return reject("invalid node bootstrapper username")
	}
	if !equalStrings(csr.Spec.Groups, nodeBootstrapperGroups) {
		return reject("invalid node bootstrapper groups")
	}
	if !validClientUsages(csr.Spec.Usages) {
		return reject("invalid client certificate usages")
	}
	if len(parsed.DNSNames) != 0 || len(parsed.IPAddresses) != 0 || len(parsed.EmailAddresses) != 0 || len(parsed.URIs) != 0 {
		return reject("client CSR contains SANs")
	}
	if !hasOrganization(parsed.Subject, NodeGroup) || len(parsed.Subject.Organization) != 1 {
		return reject("invalid client CSR organization")
	}
	if !strings.HasPrefix(parsed.Subject.CommonName, NodeUserPrefix) {
		return reject("invalid client CSR common name")
	}

	nodeName := strings.TrimPrefix(parsed.Subject.CommonName, NodeUserPrefix)
	if nodeName == "" {
		return reject("empty node name")
	}
	host, found := snapshot.HostForName(nodeName)
	if !found {
		return reject("node is not enrolled")
	}
	if !csrWithinEnrollmentWindow(csr, host) {
		return reject("CSR creation time is outside the enrollment window")
	}
	if nodeExists(nodes, nodeName) {
		return reject("node already exists")
	}
	return approve()
}

func evaluateServingCSR(csr *certificatesv1.CertificateSigningRequest, parsed *x509.CertificateRequest, nodes *corev1.NodeList, snapshot Snapshot) Decision {
	if !strings.HasPrefix(csr.Spec.Username, NodeUserPrefix) {
		return reject("invalid node serving username")
	}
	if !hasStrings(csr.Spec.Groups, "system:authenticated", NodeGroup) {
		return reject("invalid node serving groups")
	}
	if !validServingUsages(csr.Spec.Usages) {
		return reject("invalid serving certificate usages")
	}
	if parsed.Subject.CommonName != csr.Spec.Username {
		return reject("serving CSR common name does not match username")
	}
	if !hasOrganization(parsed.Subject, NodeGroup) {
		return reject("serving CSR organization does not contain system:nodes")
	}

	nodeName := strings.TrimPrefix(csr.Spec.Username, NodeUserPrefix)
	if nodeName == "" {
		return reject("empty node name")
	}
	host, found := snapshot.HostForName(nodeName)
	if !found {
		return reject("node is not enrolled")
	}
	node := findNode(nodes, nodeName)
	if node == nil {
		return reject("node does not exist")
	}
	if !servingSANsAllowed(parsed, host) {
		return reject("serving CSR contains an unknown SAN")
	}
	return approve()
}

func parseCertificateRequest(request []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(request)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("request is not a PEM certificate request")
	}
	parsed, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	if err := parsed.CheckSignature(); err != nil {
		return nil, err
	}
	return parsed, nil
}

func servingSANsAllowed(csr *x509.CertificateRequest, host ExpectedHost) bool {
	allowedNames := make(map[string]struct{}, len(host.Names))
	for _, name := range host.Names {
		allowedNames[normalizeName(name)] = struct{}{}
	}
	allowedAddresses := make(map[string]struct{}, len(host.Addresses))
	for _, address := range host.Addresses {
		allowedAddresses[normalizeAddress(address)] = struct{}{}
	}
	for _, name := range csr.DNSNames {
		if _, ok := allowedNames[normalizeName(name)]; !ok {
			return false
		}
	}
	for _, address := range csr.IPAddresses {
		if _, ok := allowedAddresses[address.String()]; !ok {
			return false
		}
	}
	return true
}

func validServingUsages(usages []certificatesv1.KeyUsage) bool {
	legacy := []certificatesv1.KeyUsage{
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageKeyEncipherment,
		certificatesv1.UsageServerAuth,
	}
	current := []certificatesv1.KeyUsage{
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageServerAuth,
	}
	return equalUsages(usages, legacy) || equalUsages(usages, current)
}

func validClientUsages(usages []certificatesv1.KeyUsage) bool {
	legacy := []certificatesv1.KeyUsage{
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageKeyEncipherment,
		certificatesv1.UsageClientAuth,
	}
	current := []certificatesv1.KeyUsage{
		certificatesv1.UsageDigitalSignature,
		certificatesv1.UsageClientAuth,
	}
	return equalUsages(usages, legacy) || equalUsages(usages, current)
}

func validateHosts(hosts []ExpectedHost) error {
	names := make(map[string]string)
	for _, host := range hosts {
		if host.ID == "" {
			return fmt.Errorf("host ID is empty")
		}
		if len(host.Names) == 0 {
			return fmt.Errorf("host %q has no node name", host.ID)
		}
		for _, name := range host.Names {
			name = normalizeName(name)
			if name == "" {
				return fmt.Errorf("host %q has an empty node name", host.ID)
			}
			if previous, exists := names[name]; exists && previous != host.ID {
				return fmt.Errorf("node name %q belongs to multiple hosts", name)
			}
			names[name] = host.ID
		}
	}
	return nil
}

func expectedHostsEqual(left, right []ExpectedHost) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID ||
			!equalStrings(left[i].Names, right[i].Names) ||
			!equalStrings(left[i].Addresses, right[i].Addresses) ||
			!sameTime(left[i].EnrollmentStartedAt, right[i].EnrollmentStartedAt) {
			return false
		}
	}
	return true
}

func csrWithinEnrollmentWindow(csr *certificatesv1.CertificateSigningRequest, host ExpectedHost) bool {
	if host.EnrollmentStartedAt == nil {
		return true
	}
	if csr.CreationTimestamp.IsZero() {
		return false
	}
	start := host.EnrollmentStartedAt.Add(-maxClockSkew)
	end := host.EnrollmentStartedAt.Add(maxHostWindow)
	return csr.CreationTimestamp.Time.After(start) && csr.CreationTimestamp.Time.Before(end)
}

func sameTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func nodeExists(nodes *corev1.NodeList, name string) bool {
	return findNode(nodes, name) != nil
}

func findNode(nodes *corev1.NodeList, name string) *corev1.Node {
	if nodes == nil {
		return nil
	}
	for i := range nodes.Items {
		if normalizeName(nodes.Items[i].Name) == normalizeName(name) {
			return &nodes.Items[i]
		}
	}
	return nil
}

func hasDecision(csr *certificatesv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1.CertificateApproved || condition.Type == certificatesv1.CertificateDenied {
			return true
		}
	}
	return false
}

func hasOrganization(subject pkix.Name, organization string) bool {
	for _, value := range subject.Organization {
		if value == organization {
			return true
		}
	}
	return false
}

func hasStrings(values []string, expected ...string) bool {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	for _, value := range expected {
		if _, ok := set[value]; !ok {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	if len(leftCopy) != len(rightCopy) {
		return false
	}
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

func equalUsages(left, right []certificatesv1.KeyUsage) bool {
	leftStrings := make([]string, len(left))
	rightStrings := make([]string, len(right))
	for i := range left {
		leftStrings[i] = string(left[i])
	}
	for i := range right {
		rightStrings[i] = string(right[i])
	}
	return equalStrings(leftStrings, rightStrings)
}

func normalizeNames(names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		name = normalizeName(name)
		if name != "" && !contains(result, name) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func normalizeAddresses(addresses []string) []string {
	result := make([]string, 0, len(addresses))
	for _, address := range addresses {
		address = normalizeAddress(address)
		if address != "" && !contains(result, address) {
			result = append(result, address)
		}
	}
	sort.Strings(result)
	return result
}

func normalizeName(name string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
}

func normalizeAddress(address string) string {
	if parsed := net.ParseIP(strings.TrimSpace(address)); parsed != nil {
		return parsed.String()
	}
	return normalizeName(address)
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func approve() Decision {
	return Decision{Approve: true, Reason: "approved"}
}

func reject(reason string) Decision {
	return Decision{Reason: reason}
}
