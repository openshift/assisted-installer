module github.com/eranco74/assisted-installer

go 1.14

require (
	github.com/ajeddeloh/go-json v0.0.0-20200220154158-5ae607161559 // indirect
	github.com/coreos/ignition v0.35.0
	github.com/filanov/bm-inventory v0.0.0-20200503085645-1f0552ea36c3
	github.com/go-openapi/strfmt v0.19.5
	github.com/golang/mock v1.4.0
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.9.0
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.6.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	go4.org v0.0.0-20200411211856-f5505b9728dd // indirect
	google.golang.org/appengine v1.6.5
	k8s.io/api v0.17.3
	k8s.io/apimachinery v0.17.3
	k8s.io/client-go v11.0.0+incompatible
)

replace (
	k8s.io/api => k8s.io/api v0.0.0-20200214081623-ecbd4af0fc33
	k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20200214081019-7490b3ed6e92
	k8s.io/client-go => k8s.io/client-go v0.0.0-20200214082307-e38a84523341
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20200214080538-dc8f3adce97c
)
