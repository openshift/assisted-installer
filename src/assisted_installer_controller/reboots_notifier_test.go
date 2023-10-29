package assisted_installer_controller

import (
	"context"
	"fmt"

	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/swag"
	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/ops"
	"github.com/openshift/assisted-service/models"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var _ = Describe("Reboots notifier", func() {
	const nodeName = "node0"
	var (
		ctrl                          *gomock.Controller
		mockops                       *ops.MockOps
		mockclient                    *inventory_client.MockInventoryClient
		notifier                      RebootsNotifier
		hostId, infraenvId, clusterId strfmt.UUID
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		mockops = ops.NewMockOps(ctrl)
		mockclient = inventory_client.NewMockInventoryClient(ctrl)
		notifier = NewRebootsNotifier(mockops, mockclient, logrus.New())
		hostId = strfmt.UUID(uuid.New().String())
		infraenvId = strfmt.UUID(uuid.New().String())
		clusterId = strfmt.UUID(uuid.New().String())
	})
	AfterEach(func() {
		ctrl.Finish()
		notifier.Finalize()
	})
	It("retry kubeconfig", func() {
		notifierImpl := &rebootsNotifier{
			ic:  mockclient,
			log: logrus.New(),
		}
		mockclient.EXPECT().DownloadClusterCredentials(context.TODO(), gomock.Any(), gomock.Any()).Return(errors.New("error"))
		mockclient.EXPECT().DownloadClusterCredentials(context.TODO(), gomock.Any(), gomock.Any()).Return(nil)
		kc, err := notifierImpl.getKubeconfigPath(context.TODO())
		Expect(err).To(HaveOccurred())
		Expect(kc).To(BeEmpty())
		kc, err = notifierImpl.getKubeconfigPath(context.TODO())
		Expect(err).ToNot(HaveOccurred())
		Expect(kc).ToNot(BeEmpty())
		kc2, err := notifierImpl.getKubeconfigPath(context.TODO())
		Expect(err).ToNot(HaveOccurred())
		Expect(kc).To(Equal(kc2))
	})
	It("happy flow", func() {
		mockclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockops.EXPECT().GetNumberOfReboots(gomock.Any(), nodeName, gomock.Any()).Return(1, nil)
		mockclient.EXPECT().TriggerEvent(gomock.Any(), &models.Event{
			Category:   models.EventCategoryUser,
			ClusterID:  &clusterId,
			HostID:     &hostId,
			InfraEnvID: &infraenvId,
			Message:    swag.String(fmt.Sprintf(eventMessageTemplate, nodeName, 1)),
			Name:       eventName,
			Severity:   swag.String(models.EventSeverityInfo),
		}).Return(nil)
		notifier.Start(context.TODO(), nodeName, &hostId, &infraenvId, &clusterId)
		notifier.Finalize()
	})
	It("fail to download kubeconfig", func() {
		mockclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("error"))
		notifier.Start(context.TODO(), nodeName, &hostId, &infraenvId, &clusterId)
		notifier.Finalize()
	})
	It("fail to get number of reboots", func() {
		mockclient.EXPECT().DownloadClusterCredentials(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
		mockops.EXPECT().GetNumberOfReboots(gomock.Any(), nodeName, gomock.Any()).Return(1, errors.New("error"))
		notifier.Start(context.TODO(), nodeName, &hostId, &infraenvId, &clusterId)
		notifier.Finalize()
	})
})
