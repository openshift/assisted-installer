module github.com/openshift/assisted-installer

go 1.14

require (
	github.com/ajeddeloh/go-json v0.0.0-20200220154158-5ae607161559 // indirect
	github.com/coreos/ignition/v2 v2.6.0
	github.com/go-openapi/strfmt v0.19.5
	github.com/golang/mock v1.4.4
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/metal3-io/baremetal-operator v0.0.0-20200828204955-fc35b7691a8e
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	github.com/openshift/assisted-installer-agent v0.0.0-20200811180147-bc9c7b899b8a
	github.com/openshift/assisted-service v0.0.0-20200830102625-2f0da134c290
	github.com/openshift/client-go v0.0.0-20200422192633-6f6c07fc2a70
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/thoas/go-funk v0.6.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/sync v0.0.0-20200317015054-43a5402ce75a
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	k8s.io/api => k8s.io/api v0.18.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.18.2
	k8s.io/client-go => k8s.io/client-go v0.18.2
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20200214080538-dc8f3adce97c
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.5.1-0.20200330174416-a11a908d91e0
)
