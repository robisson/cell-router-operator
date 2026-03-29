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

echo "[cell-router] installing Envoy Gateway ${ENVOY_GATEWAY_VERSION} via Helm"
retry 5 5 helm upgrade --install eg "${ENVOY_GATEWAY_CHART}" \
  --version "${ENVOY_GATEWAY_VERSION}" \
  --namespace envoy-gateway-system \
  --create-namespace \
  --wait \
  --timeout 5m
kubectl -n envoy-gateway-system wait deploy/envoy-gateway \
  --for condition=Available --timeout=300s

echo "[cell-router] applying local GatewayClass manifest"
kubectl apply -f "${ENVOY_GATEWAY_CLASS_MANIFEST}"
wait_for_jsonpath "gatewayclass/eg" '{.spec.controllerName}' "gateway.envoyproxy.io/gatewayclass-controller"

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
wait_for_jsonpath "cell/payments" '{.status.conditions[?(@.type=="Ready")].status}' "True"

echo "[cell-router] applying second sample cell"
kubectl apply -f config/samples/cell_v1alpha1_orders_cell.yaml
wait_for_jsonpath "cell/orders" '{.status.conditions[?(@.type=="Ready")].status}' "True"

echo "[cell-router] deploying sample workload in the cell namespace"
kubectl apply -f config/samples/payments-workload.yaml
kubectl -n payments wait deploy/payments-gateway --for condition=Available --timeout=120s

echo "[cell-router] deploying second sample workload in the cell namespace"
kubectl apply -f config/samples/orders-workload.yaml
kubectl -n orders wait deploy/orders-gateway --for condition=Available --timeout=120s

echo "[cell-router] applying sample cell router"
kubectl apply -f config/samples/cell_v1alpha1_cellrouter.yaml
wait_for_jsonpath "gateway -n cell-router-system cell-router-gateway" '{.status.conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-route" '{.status.parents[0].conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system orders-route" '{.status.parents[0].conditions[?(@.type=="Accepted")].status}' "True" 240
wait_for_jsonpath "cellrouter/default-router" '{.status.conditions[?(@.type=="Ready")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system payments-route" '{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' "True" 240
wait_for_jsonpath "httproute -n cell-router-system orders-route" '{.status.parents[0].conditions[?(@.type=="ResolvedRefs")].status}' "True" 240

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

RESPONSE=$(curl -fsS \
  -H 'Host: payments.example.com' \
  -H 'X-Tenant: premium' \
  'http://127.0.0.1:8888/payments?plan=gold')

if [[ "${RESPONSE}" != *"payments backend"* ]]; then
  echo "[cell-router] unexpected routed response: ${RESPONSE}" >&2
  exit 1
fi

ORDERS_RESPONSE=$(curl -fsS \
  -H 'Host: orders.example.com' \
  'http://127.0.0.1:8888/orders')

if [[ "${ORDERS_RESPONSE}" != *"orders backend"* ]]; then
  echo "[cell-router] unexpected routed response: ${ORDERS_RESPONSE}" >&2
  exit 1
fi

echo "[cell-router] setup complete"
echo "[cell-router] routing validated successfully"
echo "Check resource status via: kubectl get cells, kubectl get cellrouters, kubectl get gateways -A, kubectl get httproutes -A"
