module github.com/akuity/kargo/hack/testing/openapi-generator-spike/fidelity

go 1.26.0

require (
	github.com/akuity/kargo-spike v0.0.0
	github.com/akuity/kargo/api v0.0.0
	github.com/akuity/kargo/pkg/client/generated v0.0.0
	github.com/stretchr/testify v1.11.1
	k8s.io/api v0.34.3
	k8s.io/apiextensions-apiserver v0.34.3
	k8s.io/apimachinery v0.34.3
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/fxamacker/cbor/v2 v2.9.2 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-openapi/analysis v0.25.0 // indirect
	github.com/go-openapi/errors v0.22.7 // indirect
	github.com/go-openapi/jsonpointer v0.23.1 // indirect
	github.com/go-openapi/jsonreference v0.21.5 // indirect
	github.com/go-openapi/loads v0.23.3 // indirect
	github.com/go-openapi/spec v0.22.4 // indirect
	github.com/go-openapi/strfmt v0.26.2 // indirect
	github.com/go-openapi/swag v0.26.0 // indirect
	github.com/go-openapi/swag/cmdutils v0.26.0 // indirect
	github.com/go-openapi/swag/conv v0.26.0 // indirect
	github.com/go-openapi/swag/fileutils v0.26.0 // indirect
	github.com/go-openapi/swag/jsonname v0.26.0 // indirect
	github.com/go-openapi/swag/jsonutils v0.26.0 // indirect
	github.com/go-openapi/swag/loading v0.26.0 // indirect
	github.com/go-openapi/swag/mangling v0.26.0 // indirect
	github.com/go-openapi/swag/netutils v0.26.0 // indirect
	github.com/go-openapi/swag/stringutils v0.26.0 // indirect
	github.com/go-openapi/swag/typeutils v0.26.0 // indirect
	github.com/go-openapi/swag/yamlutils v0.26.0 // indirect
	github.com/go-openapi/validate v0.25.2 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/oklog/ulid/v2 v2.1.1 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/klog/v2 v2.130.1 // indirect
	k8s.io/utils v0.0.0-20250604170112-4c0f3b243397 // indirect
	sigs.k8s.io/json v0.0.0-20250730193827-2d320260d730 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.3.0 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)

replace (
	github.com/akuity/kargo-spike => ../generated
	github.com/akuity/kargo/api => ../../../../api
	github.com/akuity/kargo/pkg/client/generated => ../../../../pkg/client/generated
)
