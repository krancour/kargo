trigger_mode(TRIGGER_MODE_MANUAL)
allow_k8s_contexts('orbstack')

load('ext://namespace', 'namespace_create')

local_resource(
  'back-end-compile',
  'CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -o bin/controlplane/kargo ./cmd/controlplane',
  deps=[
    'api/',
    'cmd/',
    'internal/',
    'pkg/',
    'go.mod',
    'go.sum'
  ],
  labels = ['native-processes'],
  trigger_mode = TRIGGER_MODE_AUTO
)
docker_build(
  'ghcr.io/akuity/kargo',
  '.',
  only = ['bin/controlplane/kargo'],
  target = 'back-end-dev', # Just the back end, built natively, copied to the image
)

namespace_create('kargo')
k8s_resource(
  new_name = 'namespace',
  objects = ['kargo:namespace'],
  labels = ['kargo']
)

k8s_yaml(
  helm(
    './charts/kargo',
    name = 'kargo',
    namespace = 'kargo',
  )
)

k8s_resource(
  workload = 'kargo-api',
  new_name = 'api',
  port_forwards = [
    '30081:8080'
  ],
  labels = ['kargo'],
  resource_deps=['back-end-compile']
)
