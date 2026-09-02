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
- A bootstrap Headless Service (`env-vault-bootstrap`) selecting only `env-vault-0`. It handles startup login, health and master-key routes before the normal Service has ready endpoints. The Ingress exposes only those explicit routes and does not provide general access to the bootstrap Service.
- A PodDisruptionBudget that keeps at least two replicas available during voluntary disruption.

The stable per-Pod DNS names are:

```text
env-vault-0.env-vault-headless.env-vault.svc.cluster.local
env-vault-1.env-vault-headless.env-vault.svc.cluster.local
env-vault-2.env-vault-headless.env-vault.svc.cluster.local
```

The current master-key transfer endpoint is reached through an internal Service and requires `X-Env-Vault-Internal-Token`. The token is injected from `env-vault-runtime`; it is not placed in a URL or image. The readiness probe calls `/internal/v1/masterKey/ready` with the same token and only adds a Pod to the normal business Service after `Manager.Ready()` is true.

## Installation prerequisites

Before applying the manifest:

1. Replace all `REPLACE_ME` values in the two Secret definitions. Use the existing Base64 AES-256 key for `encryption-key`; do not generate a new key when the database already contains encrypted values.
2. Replace the JWT placeholder files with the same local JWT key pair on every replica. The private key must be PKCS#8 PEM and the public key must be PKIX PEM.
3. Change the PostgreSQL and Redis addresses in the ConfigMap to the actual services. The example names are not created by this manifest.
4. Change the StatefulSet image to a registry accessible by the target cluster. Add `imagePullSecrets` when required.
5. Apply the manifest and check `kubectl -n env-vault get pods -o wide`.

Until one replica is unlocked, the Ingress routes `/api/v1/pub/**` and `/api/v1/masterKey/**` to the bootstrap Service. Once the master key is active, all business routes use the normal `env-vault` Service. The current Pod-to-Pod key transfer implementation does not yet perform peer discovery or automatic activation; a subsequent bootstrap controller/client will use the stable Headless Service DNS.
