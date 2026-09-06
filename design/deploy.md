# Env Vault Kubernetes Deployment

## Image

The root `Dockerfile` builds a static Linux binary and runs it as UID/GID `10001` on Alpine. The image exposes port `8090`. Runtime configuration is deliberately not copied into the image; mount `configs/config.yaml` from a ConfigMap and inject credentials from Kubernetes Secrets.

Build and push an image before applying the StatefulSet:

```bash
docker buildx build \
  --platform linux/amd64 \
  --build-arg BASE_IMAGE_REGISTRY=m.daocloud.io/docker.io \
  --build-arg GOPROXY=https://goproxy.cn,direct \
  -t registry.example.com/env-vault:0.1.0 \
  --push \
  .
```

The platform must match the Kubernetes nodes. The current cluster uses `linux/amd64`; explicitly selecting it is required when building on an Apple Silicon Mac. The Dockerfile uses BuildKit target-platform arguments so the Go binary and Alpine runtime always use the same architecture. `BASE_IMAGE_REGISTRY` selects a Docker Hub mirror for the public build images and defaults to `docker.io` when it is omitted. `GOPROXY` selects the Go module proxy and defaults to `https://proxy.golang.org,direct`.

## StatefulSet

`deploy/k8s/env-vault-statefulset.yaml` installs:

- Namespace and ServiceAccount with automatic ServiceAccount token mounting disabled.
- Three Env Vault replicas managed by a StatefulSet.
- `topologySpreadConstraints` on `kubernetes.io/hostname`, so the three replicas are placed on three nodes when three nodes are available.
- A normal ClusterIP Service (`env-vault`) for business traffic.
- A Headless Service (`env-vault-headless`) for stable StatefulSet DNS.
- A bootstrap ClusterIP Service (`env-vault-bootstrap`) selecting only `env-vault-0` and publishing its NotReady endpoint. Web Nginx uses it as the fallback for startup login, health and master-key routes before the normal Service has ready endpoints. The Ingress does not expose the bootstrap Service directly.
- A PodDisruptionBudget that keeps at least two replicas available during voluntary disruption.
- Three Web Nginx replicas with startup, readiness and liveness probes, hostname topology spreading and a PodDisruptionBudget that keeps at least two Web replicas available during voluntary disruption.

The stable per-Pod DNS names are:

```text
env-vault-0.env-vault-headless.env-vault.svc.cluster.local
env-vault-1.env-vault-headless.env-vault.svc.cluster.local
env-vault-2.env-vault-headless.env-vault.svc.cluster.local
```

The current master-key transfer endpoint is reached through an internal Service and requires `X-Env-Vault-Internal-Token`. The token is injected from `env-vault-runtime`; it is not placed in a URL or image. The readiness probe calls `/internal/v1/masterKey/ready` with the same token and only adds a Pod to the normal business Service after `Manager.Ready()` is true.

## Installation prerequisites

Before applying the manifest:

1. Replace all `REPLACE_ME` values in the two Secret definitions. The committed `encryption-key` is only for local development and POC testing. Use the existing Base64 AES-256 key when validating old ciphertext; after production switches to share loading, remove this field and its environment-variable injection instead of committing the production master key.
2. Replace the JWT placeholder files with the same local JWT key pair on every replica. The private key must be PKCS#8 PEM and the public key must be PKIX PEM.
3. Change the PostgreSQL and Redis addresses in the ConfigMap to the actual services. The example names are not created by this manifest.
4. Change the StatefulSet image to a registry accessible by the target cluster. Add `imagePullSecrets` when required.
5. Apply the manifest and check `kubectl -n env-vault get pods -o wide`.

All external API requests enter the Web Nginx proxy, including `/api/v1/pub/**` and `/api/v1/masterKey/**`. Web Nginx prefers the normal `env-vault` Service and retries 502, 503, and 504 responses through the bootstrap Service. Before the first replica is unlocked, this fallback sends login, health, status, and share requests to Pod 0. During normal operation those routes use any Ready replica, so restarting Pod 0 does not make the frontend guard lose its status endpoint. This also prevents the readiness and EndpointSlice propagation interval after share recovery from leaking a transient 503 to clients. Once Pod 0 is unlocked, its one-second readiness probe adds it to the normal `env-vault` Service. The other replicas call that ClusterIP Service, receive an RSA-wrapped master key from any Ready peer, activate themselves, and then join the same Service. Rolling updates use the remaining Ready replicas and do not depend on Pod 0. The recovery client does not use the bootstrap Service, the Headless Service, or a configured Pod address list. If every replica loses its in-memory key, administrators must submit three shares to Pod 0 again. See [master-key-loading.md](master-key-loading.md) for the complete flow and implementation status.

`env-vault`, `env-vault-web`, and `env-vault-bootstrap` are internal Services and should remain `ClusterIP`. If the cluster later exposes the application through a cloud or hardware load balancer, set the Ingress Controller Service to `LoadBalancer`; do not expose the backend or bootstrap Service directly. The bootstrap selector still contains only Pod 0, so its ClusterIP provides a stable virtual IP without distributing requests to other Pods.

Web Nginx deliberately does not wait for Pod 0 or the bootstrap endpoint before starting. It can serve the login and waiting pages while Pod 0 is being created; API fallback may temporarily return a transport error until Pod 0 begins listening. Adding an init-container dependency would couple frontend availability to Pod 0 and would not solve Pod 0 address changes during later restarts.

Changing an existing Service from Headless to ordinary ClusterIP is immutable in Kubernetes. For an existing installation, recreate only the bootstrap Service and then restart Web Nginx so every worker resolves the new stable ClusterIP:

```bash
kubectl -n env-vault delete service env-vault-bootstrap
kubectl apply -f deploy/k8s/env-vault-statefulset.yaml
kubectl -n env-vault rollout restart deployment/env-vault-web
kubectl -n env-vault rollout status deployment/env-vault-web
```

During this operation, Ready backend replicas remain available through the normal `env-vault` Service. Perform the migration after at least one backend Pod is Ready.

In non-debug mode, the application access logger suppresses the high-frequency `/api/v1/pub/health` and `/internal/v1/masterKey/ready` probe requests. Debug mode retains these logs, while all business API access logs remain enabled in every mode.

## Ingress controller high availability

`deploy/k8s/ingress-nginx-ha.yaml` records the cluster-level ingress-nginx controller configuration used by the POC cluster. It keeps the existing controller image and arguments, runs three replicas across different nodes, and adds a PodDisruptionBudget with `minAvailable: 2`. This resource belongs to shared cluster infrastructure and must not be included in an Env Vault namespace uninstall operation.

```bash
kubectl apply --dry-run=server -f deploy/k8s/ingress-nginx-ha.yaml
kubectl apply -f deploy/k8s/ingress-nginx-ha.yaml
kubectl -n ingress-nginx rollout status deployment/ingress-nginx-controller
kubectl -n ingress-nginx get pods -o wide
```

The ingress controller Service remains the external `NodePort` or future `LoadBalancer`. Env Vault backend, Web, and bootstrap Services remain internal ClusterIP Services.

The current POC hostname resolves to a single Kubernetes node and reaches ingress-nginx through NodePort. Three controller replicas protect against controller Pod failure, but they do not protect against failure of that externally addressed node. Production must place a LoadBalancer or virtual IP in front of multiple nodes, or otherwise publish multiple healthy ingress entry addresses.


```shell
# restart pod
kubectl rollout restart statefulset/env-vault -n env-vault
```
