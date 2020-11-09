module github.com/openshift/assisted-installer

go 1.14

require (
	github.com/PuerkitoBio/rehttp v1.0.0
	github.com/aybabtme/iocontrol v0.0.0-20150809002002-ad15bcfc95a0 // indirect
	github.com/benbjohnson/clock v1.0.3 // indirect
	github.com/coreos/ignition/v2 v2.6.0
	github.com/go-openapi/runtime v0.19.20
	github.com/go-openapi/strfmt v0.19.5
	github.com/golang/mock v1.4.4
	github.com/jpillora/backoff v1.0.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/metal3-io/baremetal-operator v0.0.0-20200828204955-fc35b7691a8e
	github.com/onsi/ginkgo v1.14.0
	github.com/onsi/gomega v1.10.1
	github.com/openshift/api v0.0.0-20200326152221-912866ddb162
	github.com/openshift/assisted-installer-agent v0.0.0-20200811180147-bc9c7b899b8a
	github.com/openshift/assisted-service v1.0.10-0.20200929140121-f9066dc89ae2
	github.com/openshift/client-go v0.0.0-20200422192633-6f6c07fc2a70
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/thoas/go-funk v0.6.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/net v0.0.0-20200822124328-c89045814202
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208
	gopkg.in/yaml.v2 v2.3.0
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.6.2
)

replace (
	github.com/metal3-io/baremetal-operator => github.com/openshift/baremetal-operator v0.0.0-20200715132148-0f91f62a41fe // Use OpenShift fork
	k8s.io/api => k8s.io/api v0.18.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.18.2
	k8s.io/client-go => k8s.io/client-go v0.18.2
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20200214080538-dc8f3adce97c
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.5.1-0.20200330174416-a11a908d91e0
)
