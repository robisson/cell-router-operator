#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
KIND_CLUSTER_NAME=${KIND_CLUSTER_NAME:-cell-router}
GATEWAY_API_VERSION=${GATEWAY_API_VERSION:-v1.5.0}
GATEWAY_API_URL="https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
ENVOY_GATEWAY_VERSION=${ENVOY_GATEWAY_VERSION:-v1.7.1}
ENVOY_GATEWAY_CHART=${ENVOY_GATEWAY_CHART:-oci://docker.io/envoyproxy/gateway-helm}
ENVOY_GATEWAY_CLASS_MANIFEST=${ENVOY_GATEWAY_CLASS_MANIFEST:-config/local/envoy-gatewayclass.yaml}
GO_IMAGE=${GO_IMAGE:-golang:1.25.3}
IMG_NAME=${IMG_NAME:-cell-router-operator:local}
GO_CACHE_DIR=${GO_CACHE_DIR:-${ROOT_DIR}/.cache/go-build}
GO_MOD_CACHE_DIR=${GO_MOD_CACHE_DIR:-${ROOT_DIR}/.cache/go-mod}
PORT_FORWARD_PID=""

cd "$ROOT_DIR"

mkdir -p "$GO_CACHE_DIR" "$GO_MOD_CACHE_DIR"

cleanup() {
  if [[ -n "$PORT_FORWARD_PID" ]]; then
    kill "$PORT_FORWARD_PID" >/dev/null 2>&1 || true
  fi
}

trap cleanup EXIT

docker_go() {
  docker run --rm \
    -v "${ROOT_DIR}:/workspace" \
    -w /workspace \
    -v "${GO_CACHE_DIR}:/root/.cache/go-build" \
    -v "${GO_MOD_CACHE_DIR}:/go/pkg/mod" \
    -e GOTOOLCHAIN=local \
    "${GO_IMAGE}" \
    go "$@"
}

make_with_docker_go() {
  local wrapper_dir
  wrapper_dir=$(mktemp -d)
  cat >"${wrapper_dir}/go" <<EOF
#!/usr/bin/env bash
set -euo pipefail
exec docker run --rm \\
  -v "${ROOT_DIR}:/workspace" \\
  -w /workspace \\
  -v "${GO_CACHE_DIR}:/root/.cache/go-build" \\
  -v "${GO_MOD_CACHE_DIR}:/go/pkg/mod" \\
  -e GOTOOLCHAIN=local \\
  "${GO_IMAGE}" \\
  go "\$@"
EOF
  chmod +x "${wrapper_dir}/go"
  PATH="${wrapper_dir}:${PATH}" make "$@"
  rm -rf "${wrapper_dir}"
}

retry() {
  local attempts="$1"
  local delay_seconds="$2"
  shift 2

  local try
  for try in $(seq 1 "${attempts}"); do
    if "$@"; then
      return 0
    fi

    if [[ "${try}" -lt "${attempts}" ]]; then
      sleep "${delay_seconds}"
    fi
  done

  return 1
}

wait_for_jsonpath() {
  local resource="$1"
  local jsonpath="$2"
  local expected="$3"
  local timeout="${4:-180}"
  local elapsed=0

  until [[ "$elapsed" -ge "$timeout" ]]; do
    local current
    current=$(kubectl get ${resource} -o "jsonpath=${jsonpath}" 2>/dev/null || true)
    if [[ "$current" == "$expected" ]]; then
      return 0
    fi
    sleep 2
    elapsed=$((elapsed + 2))
  done

  echo "[cell-router] timeout waiting for ${resource} ${jsonpath}=${expected}" >&2
  kubectl get ${resource} -o yaml || true
  return 1
}

echo "[cell-router] ensuring kind cluster ${KIND_CLUSTER_NAME}"
if ! kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
  kind create cluster --name "${KIND_CLUSTER_NAME}"
fi

echo "[cell-router] waiting for cluster nodes to become Ready"
kubectl wait --for=condition=Ready nodes --all --timeout=180s

echo "[cell-router] installing Gateway API CRDs from ${GATEWAY_API_URL}"
retry 5 5 kubectl apply -f "${GATEWAY_API_URL}"

if ! helm status eg -n envoy-gateway-system >/dev/null 2>&1 && \
  kubectl get namespace envoy-gateway-system >/dev/null 2>&1 && \
  kubectl -n envoy-gateway-system get serviceaccount envoy-gateway >/dev/null 2>&1; then
  echo "[cell-router] removing stale non-Helm Envoy Gateway installation"
  kubectl delete namespace envoy-gateway-system --wait=true
fi

if ! helm status eg -n envoy-gateway-system >/dev/null 2>&1; then
  kubectl delete clusterrole/eg-gateway-helm-envoy-gateway-role \
    clusterrole/eg-gateway-helm-certgen:envoy-gateway-system \
    clusterrolebinding/eg-gateway-helm-envoy-gateway-rolebinding \
    clusterrolebinding/eg-gateway-helm-certgen:envoy-gateway-system \
    --ignore-not-found=true >/dev/null
fi

echo "[cell-router] installing Envoy Gateway ${ENVOY_GATEWAY_VERSION} via Helm"
retry 5 5 helm upgrade --install eg "${ENVOY_GATEWAY_CHART}" \
  --version "${ENVOY_GATEWAY_VERSION}" \
  --namespace envoy-gateway-system \
  --create-namespace \
  --skip-crds \
  --wait \
  --timeout 5m
kubectl -n envoy-gateway-system wait deploy/envoy-gateway \
  --for condition=Available --timeout=300s

echo "[cell-router] applying local GatewayClass manifest"
kubectl apply -f "${ENVOY_GATEWAY_CLASS_MANIFEST}"
wait_for_jsonpath "gatewayclass/eg" '{.spec.controllerName}' "gateway.envoyproxy.io/gatewayclass-controller"

echo "[cell-router] labeling Envoy Gateway namespace for sample network policy access"
kubectl label namespace envoy-gateway-system cellrouter.io/cell-access=true --overwrite

echo "[cell-router] running unit tests with coverage"
docker_go test ./api/... ./internal/... -coverprofile=coverage.out
docker_go tool cover -func=coverage.out | tail -n1

echo "[cell-router] building controller image ${IMG_NAME}"
docker build -t "${IMG_NAME}" .

echo "[cell-router] loading image into kind"
kind load docker-image "${IMG_NAME}" --name "${KIND_CLUSTER_NAME}"

echo "[cell-router] installing CRDs"
kubectl apply -f config/crd/bases

echo "[cell-router] deploying controller"
make_with_docker_go deploy IMG="${IMG_NAME}"

echo "[cell-router] waiting for controller manager to become available"
kubectl -n cell-router-operator-system wait deploy/cell-router-operator-controller-manager \
  --for condition=Available --timeout=120s

echo "[cell-router] applying sample cell"
kubectl apply -f config/samples/cell_v1alpha1_cell.yaml

echo "[cell-router] applying second payments cell"
kubectl apply -f config/samples/cell_v1alpha1_payments_cell_2.yaml

echo "[cell-router] deploying sample workload in the cell namespace"
kubectl apply -f config/samples/payments-cell-1-workload.yaml
kubectl -n payments-cell-1 wait deploy/payments-cell-1-gateway --for condition=Available --timeout=120s

echo "[cell-router] deploying second payments workload in the cell namespace"
kubectl apply -f config/samples/payments-cell-2-workload.yaml
kubectl -n payments-cell-2 wait deploy/payments-cell-2-gateway --for condition=Available --timeout=120s

echo "[cell-router] waiting for sample cells to become traffic-ready"
wait_for_jsonpath "cell/payments-cell-1" '{.status.conditions[?(@.type=="Ready")].status}' "True"
wait_for_jsonpath "cell/payments-cell-2" '{.status.conditions[?(@.type=="Ready")].status}' "True"

echo "[cell-router] verifying sample policies were reconciled"
kubectl -n payments-cell-1 get resourcequota/cell-quota >/dev/null
kubectl -n payments-cell-1 get limitrange/cell-limits >/dev/null
kubectl -n payments-cell-1 get networkpolicy/cell-entrypoint >/dev/null

echo "[cell-router] applying sample cell router"
kubectl apply -f config/samples/cell_v1alpha1_cellrouter.yaml
echo "[cell-router] applying sample placements"
kubectl apply -f config/samples/cell_v1alpha1_cellplacement.yaml
wait_for_jsonpath "gateway -n cell-router-system cell-router-gateway" '{.status.conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-cell-1-route" '{.status.parents[0].conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-cell-2-route" '{.status.parents[0].conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "cellrouter/default-router" '{.status.conditions[?(@.type=="Ready")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-cell-1-route" '{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-cell-2-route" '{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' "True" 240

placements=(
  header-exact-placement
  header-prefix-placement
  header-suffix-placement
  header-range-placement
  query-exact-placement
  query-prefix-placement
  query-suffix-placement
  query-range-placement
  path-exact-placement
  path-prefix-placement
  path-suffix-placement
  path-range-placement
)

for placement in "${placements[@]}"; do
  wait_for_jsonpath "httproute -n cell-router-system ${placement}" '{.status.parents[0].conditions[?(@.type=="Accepted")].status}' "True" 240
  wait_for_jsonpath "httproute -n cell-router-system ${placement}" '{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' "True" 240
  wait_for_jsonpath "cellplacement/${placement}" '{.status.conditions[?(@.type=="Ready")].status}' "True" 240
done

echo "[cell-router] port-forwarding Envoy service to verify traffic"
ENVOY_SERVICE=$(kubectl get svc -n envoy-gateway-system \
  --selector=gateway.envoyproxy.io/owning-gateway-namespace=cell-router-system,gateway.envoyproxy.io/owning-gateway-name=cell-router-gateway \
  -o jsonpath='{.items[0].metadata.name}')

if [[ -z "${ENVOY_SERVICE}" ]]; then
  echo "[cell-router] failed to find Envoy service for the managed gateway" >&2
  kubectl get svc -n envoy-gateway-system -o wide
  exit 1
fi

kubectl -n envoy-gateway-system port-forward "service/${ENVOY_SERVICE}" 8888:80 >/tmp/cell-router-port-forward.log 2>&1 &
PORT_FORWARD_PID=$!
sleep 5

assert_contains() {
  local expected="$1"
  local response="$2"
  local context="$3"
  if [[ "${response}" != *"${expected}"* ]]; then
    echo "[cell-router] unexpected response for ${context}: ${response}" >&2
    exit 1
  fi
}

assert_status() {
  local expected="$1"
  local actual="$2"
  local context="$3"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "[cell-router] unexpected status for ${context}: ${actual}" >&2
    exit 1
  fi
}

RESPONSE=$(curl -fsS \
  -H 'Host: payments.example.com' \
  'http://127.0.0.1:8888/payments/cell-1')

assert_contains "payments cell 1 backend" "${RESPONSE}" "payments-cell-1-route"

CELL_2_RESPONSE=$(curl -fsS \
  -H 'Host: payments.example.com' \
  'http://127.0.0.1:8888/payments/cell-2')

assert_contains "payments cell 2 backend" "${CELL_2_RESPONSE}" "payments-cell-2-route"

HEADER_EXACT_RESPONSE=$(curl -fsS \
  -H 'Host: header-exact.payments.example.com' \
  -H 'X-Account: tenant-a' \
  'http://127.0.0.1:8888/')
assert_contains "payments cell 1 backend" "${HEADER_EXACT_RESPONSE}" "header-exact-placement"

HEADER_PREFIX_RESPONSE=$(curl -fsS \
  -H 'Host: header-prefix.payments.example.com' \
  -H 'X-Account: team-alpha' \
  'http://127.0.0.1:8888/')
assert_contains "payments cell 1 backend" "${HEADER_PREFIX_RESPONSE}" "header-prefix-placement"

HEADER_SUFFIX_RESPONSE=$(curl -fsS \
  -H 'Host: header-suffix.payments.example.com' \
  -H 'X-Account: account-west' \
  'http://127.0.0.1:8888/')
assert_contains "payments cell 2 backend" "${HEADER_SUFFIX_RESPONSE}" "header-suffix-placement"

HEADER_RANGE_RESPONSE=$(curl -fsS \
  -H 'Host: header-range.payments.example.com' \
  -H 'X-Shard: 150' \
  'http://127.0.0.1:8888/')
assert_contains "payments cell 1 backend" "${HEADER_RANGE_RESPONSE}" "header-range-placement"

QUERY_EXACT_RESPONSE=$(curl -fsS \
  -H 'Host: query-exact.payments.example.com' \
  'http://127.0.0.1:8888/?account=tenant-b')
assert_contains "payments cell 2 backend" "${QUERY_EXACT_RESPONSE}" "query-exact-placement"

QUERY_PREFIX_RESPONSE=$(curl -fsS \
  -H 'Host: query-prefix.payments.example.com' \
  'http://127.0.0.1:8888/?account=team-alpha')
assert_contains "payments cell 1 backend" "${QUERY_PREFIX_RESPONSE}" "query-prefix-placement"

QUERY_SUFFIX_RESPONSE=$(curl -fsS \
  -H 'Host: query-suffix.payments.example.com' \
  'http://127.0.0.1:8888/?account=account-west')
assert_contains "payments cell 2 backend" "${QUERY_SUFFIX_RESPONSE}" "query-suffix-placement"

QUERY_RANGE_RESPONSE=$(curl -fsS \
  -H 'Host: query-range.payments.example.com' \
  'http://127.0.0.1:8888/?shard=250')
assert_contains "payments cell 2 backend" "${QUERY_RANGE_RESPONSE}" "query-range-placement"

PATH_EXACT_RESPONSE=$(curl -fsS \
  -H 'Host: path-exact.payments.example.com' \
  'http://127.0.0.1:8888/accounts/tenant-a')
assert_contains "payments cell 1 backend" "${PATH_EXACT_RESPONSE}" "path-exact-placement"

PATH_PREFIX_RESPONSE=$(curl -fsS \
  -H 'Host: path-prefix.payments.example.com' \
  'http://127.0.0.1:8888/accounts/team-alpha')
assert_contains "payments cell 1 backend" "${PATH_PREFIX_RESPONSE}" "path-prefix-placement"

PATH_SUFFIX_RESPONSE=$(curl -fsS \
  -H 'Host: path-suffix.payments.example.com' \
  'http://127.0.0.1:8888/accounts/account-west')
assert_contains "payments cell 2 backend" "${PATH_SUFFIX_RESPONSE}" "path-suffix-placement"

PATH_RANGE_RESPONSE=$(curl -fsS \
  -H 'Host: path-range.payments.example.com' \
  'http://127.0.0.1:8888/orders/150')
assert_contains "payments cell 2 backend" "${PATH_RANGE_RESPONSE}" "path-range-placement"

HEADER_NEGATIVE_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
  -H 'Host: header-exact.payments.example.com' \
  -H 'X-Account: tenant-b' \
  'http://127.0.0.1:8888/')
assert_status "404" "${HEADER_NEGATIVE_STATUS}" "header exact negative"

QUERY_NEGATIVE_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
  -H 'Host: query-prefix.payments.example.com' \
  'http://127.0.0.1:8888/?account=alpha-team')
assert_status "404" "${QUERY_NEGATIVE_STATUS}" "query prefix negative"

PATH_NEGATIVE_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
  -H 'Host: path-suffix.payments.example.com' \
  'http://127.0.0.1:8888/accounts/account-east')
assert_status "404" "${PATH_NEGATIVE_STATUS}" "path suffix negative"

RANGE_NEGATIVE_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
  -H 'Host: path-range.payments.example.com' \
  'http://127.0.0.1:8888/orders/250')
assert_status "404" "${RANGE_NEGATIVE_STATUS}" "path range negative"

echo "[cell-router] setup complete"
echo "[cell-router] routing validated successfully"
echo "Check resource status via: kubectl get cells, kubectl get cellplacements, kubectl get cellrouters, kubectl get gateways -A, kubectl get httproutes -A"
