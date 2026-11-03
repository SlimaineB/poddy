# Local Dex demo

A small Dex instance for trying poddy OIDC and RBAC locally. It is not a production setup.

## Files

- `config.yaml`: Dex issuer, in-memory storage, poddy client, eight static users, and groups.
- `docker-compose.yaml`: runs `ghcr.io/dexidp/dex:v2.45.0` on port 5556.
- `rbac.yaml`: demo RBAC ConfigMap (admin, `backend` team, `go` owner).

## Users

| User | Password | Role |
|------|----------|------|
| `admin@example.com` | `admin` | global admin |
| `ops@example.com` | `ops` | global admin |
| `dev@example.com` | `poddy` | owner of the `go` template |
| `alice@example.com` | `alice` | member of the `backend` team |
| `bob@example.com` | `bob` | member of the `backend` team |
| `carol@example.com` | `carol` | member of the `frontend` team |
| `dave@example.com` | `dave` | direct user of the `go` template |
| `erin@example.com` | `erin` | regular user (public `base` only) |

## Run

From the repository root:

```bash
kubectl --context local create namespace poddy   # once
./scripts/dev-oidc.sh
```

The script starts Dex, waits for discovery, applies `rbac.yaml`, builds the portal, and starts it with:

- issuer `http://127.0.0.1:5556/dex`
- client `poddy` / `poddy-secret`
- redirect `http://127.0.0.1:8090/oidc/callback`
- shared token `secret` (still a global admin)

Open `http://127.0.0.1:8090`, choose **Continue with SSO**, and use the users above.

## Demo flow

1. Sign in as `dev@example.com` / `poddy`.
2. **Templates** → `go` → **Manage access** → tick `backend` → save.
3. Sign out, sign in as `alice@example.com` or `bob@example.com`; `go` is now available.
4. `dave@example.com` has direct access to `go`; `carol@example.com` and `erin@example.com` see only `base`.
5. Sign in as `admin@example.com` or `ops@example.com` to manage teams or see all workspaces.

## Stop

```bash
# Stop the portal with Ctrl+C, then:
docker compose -f deploy/dex/docker-compose.yaml down
```

Dex uses in-memory storage: restarting it resets users to `config.yaml`.

## Notes

- Plain HTTP and known passwords: local only.
- The issuer and redirect URLs are fixed to `127.0.0.1`; if WSL localhost forwarding is disabled, use the WSL IP and update both `config.yaml` and the portal flags consistently.
- To change the demo namespace, set `PODDY_NAMESPACE`; the script rewrites the namespace in `rbac.yaml`.
