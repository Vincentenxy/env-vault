# Env Vault Kubernetes Deployment

## Image

The root `Dockerfile` builds a static Linux binary and runs it as UID/GID `10001` on Alpine. The image exposes port `8090`. Runtime configuration is deliberately not copied into the image; mount `configs/config.yaml` from a ConfigMap and inject credentials from Kubernetes Secrets.

Build and push an image before applying the StatefulSet:

```bash
docker build -t registry.example.com/env-vault:0.1.0 .
docker push registry.example.com/env-vault:0.1.0
```

## StatefulSet

`deploy/k8s/env-vault-statefulset.yaml` installs:

- Namespace and ServiceAccount with automatic ServiceAccount token mounting disabled.
- Three Env Vault replicas managed by a StatefulSet.
- `topologySpreadConstraints` on `kubernetes.io/hostname`, so the three replicas are placed on three nodes when three nodes are available.
- A normal ClusterIP Service (`env-vault`) for business traffic.
- A Headless Service (`env-vault-headless`) for stable StatefulSet DNS.
- A bootstrap Headless Service (`env-vault-bootstrap`) selecting only `env-vault-0`. It is intended for the initial three-share unlock flow and must remain internal.
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

Until one replica is unlocked, use the bootstrap Service for the startup UI/API route. Once the master key is active, use the normal `env-vault` Service for business traffic. The current Pod-to-Pod key transfer implementation does not yet perform peer discovery or automatic activation; a subsequent bootstrap controller/client will use the stable Headless Service DNS.
