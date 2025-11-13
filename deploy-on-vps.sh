
set -e

echo "======================================"
echo "🚀 AI Gateway Deployment Script (VPS)"
echo "======================================"

TAG="custom-v3"
CONTROLLER_TAR="/tmp/ai-gateway-controller-custom.tar"
EXTPROC_TAR="/tmp/ai-gateway-extproc-custom.tar"

echo ""
echo "📦 Loading Docker images from tar files..."
echo "  Loading controller..."
docker load -i "$CONTROLLER_TAR"

echo "  Loading extproc..."
docker load -i "$EXTPROC_TAR"

echo ""
echo "✅ Docker images loaded"
docker images | grep "ai-gateway.*$TAG"

echo ""
echo "📥 Importing images to K3s..."
echo "  Importing controller..."
docker save envoyproxy/ai-gateway-controller:$TAG | sudo k3s ctr images import -

echo "  Importing extproc..."
docker save envoyproxy/ai-gateway-extproc:$TAG | sudo k3s ctr images import -

echo ""
echo "✅ K3s images imported"
sudo k3s ctr images ls | grep "ai-gateway.*$TAG"

echo ""
echo "📝 Applying updated CRDs..."
kubectl apply -f /tmp/ai-gateway/manifests/charts/ai-gateway-crds-helm/templates/

echo ""
echo "� Upgrading Helm deployment..."
helm upgrade ai-gateway /tmp/ai-gateway/manifests/charts/ai-gateway-helm \
  --namespace envoy-ai-gateway-system \
  --set extProc.image.repository=envoyproxy/ai-gateway-extproc \
  --set extProc.image.tag=$TAG \
  --set extProc.enabled=true \
  --set extProc.logLevel=debug \
  --set extProc.imagePullPolicy=Never \
  --set controller.image.repository=envoyproxy/ai-gateway-controller \
  --set controller.image.tag=$TAG \
  --set controller.imagePullPolicy=Never \
  --set controller.logLevel=debug

echo ""
echo "⏳ Waiting for controller to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=ai-gateway-controller -n envoy-ai-gateway-system --timeout=60s

echo ""
echo "🔄 Restarting Envoy gateway pods..."
kubectl delete pod -n envoy-gateway-system -l app.kubernetes.io/name=envoy

echo ""
echo "⏳ Waiting for Envoy pods to be ready..."
sleep 5
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=envoy -n envoy-gateway-system --timeout=120s

echo ""
echo "✅ Deployment complete!"
echo ""
echo "📊 Pod Status:"
kubectl get pods -n envoy-ai-gateway-system
kubectl get pods -n envoy-gateway-system

echo ""
echo "======================================"
echo "✨ Ready to test!"
echo "======================================"
echo ""
