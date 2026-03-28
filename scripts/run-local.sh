#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
KIND_CLUSTER_NAME=${KIND_CLUSTER_NAME:-cell-router}
GATEWAY_API_VERSION=${GATEWAY_API_VERSION:-v1.2.0}
GATEWAY_API_URL="https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"
IMG_NAME=${IMG_NAME:-cell-router-operator:local}

cd "$ROOT_DIR"

echo "[cell-router] ensuring kind cluster ${KIND_CLUSTER_NAME}"
if ! kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
  kind create cluster --name "${KIND_CLUSTER_NAME}"
fi

echo "[cell-router] installing Gateway API CRDs from ${GATEWAY_API_URL}"
kubectl apply -f "${GATEWAY_API_URL}"

echo "[cell-router] running unit tests with coverage"
go test ./api/... ./internal/... -coverprofile=coverage.out

echo "[cell-router] building controller image ${IMG_NAME}"
docker build -t "${IMG_NAME}" .

echo "[cell-router] loading image into kind"
kind load docker-image "${IMG_NAME}" --name "${KIND_CLUSTER_NAME}"

echo "[cell-router] installing CRDs"
make install

echo "[cell-router] deploying controller"
make deploy IMG="${IMG_NAME}"

echo "[cell-router] waiting for controller manager to become available"
kubectl -n cell-router-operator-system wait deploy/cell-router-operator-controller-manager \
  --for condition=Available --timeout=120s

echo "[cell-router] applying sample resources"
kubectl apply -f config/samples/cell_v1alpha1_cell.yaml
kubectl apply -f config/samples/cell_v1alpha1_cellrouter.yaml

echo "[cell-router] setup complete"
echo "Check resource status via: kubectl get cells, kubectl get cellrouters, kubectl get httproutes -A"
