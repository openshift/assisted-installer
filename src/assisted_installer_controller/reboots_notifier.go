package assisted_installer_controller

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/openshift/assisted-installer/src/common"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
)

const (
	eventName                       = "reboots_for_node"
	eventMessageTemplate            = "Node %s has been rebooted %d times before completing installation"
	getNumRebootsRetries            = 10
	getNumRebootsTimeout            = 6 * time.Minute
	getNumRebootsRetrySleepDuration = 15 * time.Second
)

//go:generate mockgen -source=reboots_notifier.go -package=assisted_installer_controller -destination=mock_reboots_notifier.go
type RebootsNotifier interface {
	Start(ctx context.Context, nodeName string, hostId, infraenvId, clusterId *strfmt.UUID)
	Finalize()
}

type rebootsNotifier struct {
	cancelers      []context.CancelFunc
	log            logrus.FieldLogger
	wg             sync.WaitGroup
	kubeconfigPath string
	ops            ops.Ops
	ic             inventory_client.InventoryClient
	mu             sync.Mutex
}

func NewRebootsNotifier(ops ops.Ops, ic inventory_client.InventoryClient, log logrus.FieldLogger) RebootsNotifier {
	return &rebootsNotifier{
		log: log,
		ops: ops,
		ic:  ic,
	}
}

func (r *rebootsNotifier) getKubeconfigPath(ctx context.Context) (string, error) {
	if r.kubeconfigPath != "" {
		return r.kubeconfigPath, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.kubeconfigPath == "" {
		dir, err := os.MkdirTemp("", "kubedir")
		if err != nil {
			return "", err
		}
		kubeconfigPath, err := common.DownloadKubeconfigNoingress(ctx, dir, r.ic, r.log)
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		r.kubeconfigPath = kubeconfigPath
	}
	return r.kubeconfigPath, nil
}

func (r *rebootsNotifier) run(ctx context.Context, nodeName string, hostId, infraenvId, clusterId *strfmt.UUID) {
	defer r.wg.Done()
	kubeconfigPath, err := r.getKubeconfigPath(ctx)
	if err != nil {
		r.log.Warningf("failed to get kubeconfig.  aborting notifying reboots for %s", nodeName)
		return
	}
	var numberOfReboots int
	err = utils.RetryWithContext(ctx, getNumRebootsRetries, getNumRebootsRetrySleepDuration, r.log, func() (err error) {
		numberOfReboots, err = r.ops.GetNumberOfReboots(ctx, nodeName, kubeconfigPath)
		return err
	})
	if err != nil {
		r.log.WithError(err).Errorf("failed to get number of reboots for node %s", nodeName)
		return
	}

	r.log.Infof("number of reboots for node %s is %d", nodeName, numberOfReboots)
	ev := &models.Event{
		HostID:     hostId,
		InfraEnvID: infraenvId,
		ClusterID:  clusterId,
		Name:       eventName,
		Category:   models.EventCategoryUser,
		Severity:   swag.String(models.EventSeverityInfo),
		Message:    swag.String(fmt.Sprintf(eventMessageTemplate, nodeName, numberOfReboots)),
	}

	if err = r.ic.TriggerEvent(ctx, ev); err != nil {
		r.log.WithError(err).Errorf("failed to trigger number of reboots event for node %s", nodeName)
	}
}

func (r *rebootsNotifier) Start(ctx context.Context, nodeName string, hostId, infraenvId, clusterId *strfmt.UUID) {
	execCtx, cancel := context.WithTimeout(ctx, getNumRebootsTimeout)
	r.cancelers = append(r.cancelers, cancel)
	r.wg.Add(1)
	go r.run(execCtx, nodeName, hostId, infraenvId, clusterId)
}

func (r *rebootsNotifier) Finalize() {
	r.wg.Wait()
	funk.ForEach(r.cancelers, func(c context.CancelFunc) { c() })
	if r.kubeconfigPath != "" {
		_ = os.RemoveAll(r.kubeconfigPath)
	}
}
