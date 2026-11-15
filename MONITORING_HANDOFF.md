# Handoff — monitoring des workspaces (Poddy)

> Dernière mise à jour : 2026-11-15
>
> Objectif produit : faire du monitoring par workspace un vrai différenciateur :
> CPU/mémoire live, historique Prometheus, IOPS/FS, réseau, et identification des
> services externes (S3, EFS, endpoints cloud) y compris quand ils passent par un
> CSI driver, un FUSE mount ou un tunnel local. Tout doit rester activable ou
> désactivable, sans casser une installation qui n'a ni metrics-server ni
> Prometheus.

---

## 1. État actuel

### Fait et validé

- Dépendance ajoutée : `k8s.io/metrics v0.37.2`.
- Backend live metrics via metrics-server, avec cache namespace (TTL 10 s, cache
  d'erreur 3 s) pour éviter le N+1.
- Backend historique via Prometheus, requêtes whitelistées côté serveur.
- Agrégation multi-région pour l'overview live et pour l'historique global.
- Routes API ajoutées et protégées par les flags serveur + ownership/admin.
- `/api/config` expose les flags monitoring au frontend.
- UI admin **Monitoring** : KPI cards, graphiques SVG, tableau par workspace,
  sélecteur 1h/6h/24h, polling 15 s.
- UI détail workspace : panneau **Monitoring** avec CPU/mémoire/disque live,
  requests/limits, santé, redémarrages, tableau containers et graphiques.
- Helm : values, schéma, RBAC `metrics.k8s.io`, env vars.
- Tests Go ajoutés pour live metrics, Prometheus, merge multi-région et routes.
- `go build ./...`, `go vet ./...`, `go test ./...` passent.
- `helm lint` et `helm template` passent (avec et sans Prometheus).
- `node --check` passe sur les JS.
- `npm run build` est bloqué localement par Node 22.18.0 ; Astro exige
  `>=22.23.4`. À relancer avec une version Node conforme.

### Résultats du smoke test cluster (2026-11-15)

Cluster k3s local + metrics-server + `kube-prometheus-stack` (Grafana et
Alertmanager désactivés) :

- `/api/config` expose bien `live=true`, `history=true` avec Prometheus.
- `/api/monitoring/overview` agrège les 3 workspaces, y compris les anciens pods
  legacy nommés `poddy-<workspace>` sans suffixe `-0`.
- `/api/workspaces/{name}/metrics` renvoie CPU/mémoire, requests/limits, statuts
  containers et redémarrages.
- `/api/monitoring/history` et l'historique workspace renvoient bien les 12
  séries + stockage avec `stepSeconds` et des points réels.
- Corrigé pendant le smoke test :
  - mapping pod → workspace par label `poddy.io/workspace` au lieu du nom ;
  - `reason` global vide quand au moins une métrique est disponible ;
  - lecture des points JSON `{t,v}` dans les graphiques SVG (les courbes
    étaient vides sans downsample) ;
  - capacité de stockage basée sur la requête PVC (`kube-state-metrics`) avec
    fallback kubelet pour éviter les capacités FS locales aberrantes ;
  - `range=0s` / `step=0s` renvoient désormais HTTP 400 ;
  - panneau **Ports** remonté en premier dans le détail workspace.
- Reste à vérifier dans un vrai navigateur : rendu mobile, 1h/6h/24h, et mode
  hub multi-région.

### Séries historiques déjà exposées

Par workspace (12 + stockage) :

| Clé | Unité | Source |
|-----|-------|--------|
| `cpu` | cores | cAdvisor |
| `memory` | bytes | cAdvisor |
| `networkRx` / `networkTx` | bytes/s | cAdvisor |
| `fsRead` / `fsWrite` | bytes/s | cAdvisor |
| `fsReadIOPS` / `fsWriteIOPS` | iops | cAdvisor |
| `networkErrors` / `networkDrops` | /s | cAdvisor |
| `cpuThrottle` | ratio | cAdvisor |
| `restarts` | count | kube-state-metrics |
| `storageUsed` / `storageCapacity` | bytes | kubelet / PVC |

Global admin (11 séries + stockage) : `cpu`, `memory`, `networkRx`,
`networkTx`, `networkErrors`, `networkDrops`, `fsRead`, `fsWrite`,
`fsReadIOPS`, `fsWriteIOPS`, `restarts`, plus `storageUsed` et
`storageCapacity` via les PVC du namespace. Le filtre global ne garde que les
pods workspace `poddy-*-0` pour ne pas compter le portail ou le bastion.

### Fichiers principaux

- `internal/monitoring/types.go` : types API, `ResourceUsage`.
- `internal/monitoring/live.go` : metrics-server, cache, statuts containers.
- `internal/monitoring/prometheus.go` : client Prometheus, requêtes, historique.
- `internal/monitoring/merge.go` : fusion multi-région de séries historiques.
- `internal/server/monitoring.go` : handlers, config, overview local.
- `internal/server/server.go` : routes + payload `/api/config`.
- `internal/app/app.go` : création provider live / client Prometheus.
- `internal/k8s/workspaces.go` : `Manager.ListPods`.
- `internal/region/client.go` / `internal/region/hub.go` : overview/history
  multi-région.
- `internal/config/config.go` : flags/env.
- `web/monitoring.js` : toute la logique frontend monitoring.
- `web/index.html`, `web/core.js`, `web/app.js`, `web/workspaces.js`,
  `web/style.css` : intégration UI.
- `deploy/helm/poddy/*` : values, schéma, RBAC, deployment.
- `internal/server/monitoring_test.go`,
  `internal/monitoring/{live,prometheus,merge}_test.go` : tests.

### Routes

| Méthode | Route | Accès |
|---------|-------|-------|
| GET | `/api/workspaces/{name}/metrics` | owner ou admin |
| GET | `/api/workspaces/{name}/metrics/history?range=1h` | owner ou admin |
| GET | `/api/monitoring/overview` | admin + `monitoring.global` |
| GET | `/api/monitoring/history?range=1h` | admin + `monitoring.global` |

En mode hub, les routes workspace sont proxyfiées vers la région ; l'overview et
l'historique global agrègent les régions.

### Flags

| Flag | Env | Helm | Défaut |
|------|-----|------|--------|
| monitoring master | `PODDY_MONITORING_ENABLED` | `monitoring.enabled` | true |
| live | `PODDY_MONITORING_LIVE` | `monitoring.live` | true |
| history | `PODDY_MONITORING_HISTORY` | `monitoring.history` | true |
| global | `PODDY_MONITORING_GLOBAL` | `monitoring.global` | true |
| costs | `PODDY_MONITORING_COSTS` | `monitoring.costs.enabled` | false |
| costs users | `PODDY_MONITORING_COSTS_FOR_USERS` | `monitoring.costs.visibleToUsers` | false |
| Prometheus URL | `PODDY_PROMETHEUS_URL` | `monitoring.prometheus.url` | vide |
| Prometheus token | `PODDY_PROMETHEUS_TOKEN` | secret existant | vide |

Quand `PODDY_PROMETHEUS_URL` est vide, `monitoring.history` est rapporté `false`
dans `/api/config` même si le flag est `true`.

- `/api/config` renvoie :
  `monitoring: {enabled, live, history, global, costs, costsForUsers}`.

---

## 2. Ce qu'il reste à faire avant de considérer le socle terminé

### P0 — finir et durcir le socle

1. **Smoke test navigateur réel** : lancer le portail avec un cluster kind/k3s +
   metrics-server + kube-prometheus-stack, créer un workspace, vérifier :
   - live CPU/mémoire dans le détail ;
   - graphiques 1h/6h/24h ;
   - vue admin globale ;
   - états `disabled`, `metrics-server absent`, `Prometheus absent`,
     `pas encore de sample`, `RBAC metrics interdit`.
2. **Vérifier le rendu des nouveaux graphiques IOPS / erreurs / drops** et la
   lisibilité sur mobile.
3. **Vérifier le comportement sans Prometheus** : le panneau détail doit
   afficher un message propre, pas une erreur bloquante.
4. **Vérifier le mode hub** : un workspace d'une région doit afficher live +
   history ; les graphiques globaux doivent agréger les régions.
5. **Ajouter un test de non-régression** sur le filtre global
   (`poddy-*-0`) et sur `MergeHistories`.
6. **Documentation** : `src/content/docs/deploy.md` et `operations.md` sont à
   jour ; ajouter éventuellement une page publique “Workspace monitoring” si le
   produit doit la mettre en avant.
7. **Build docs** : `npm run build`.
8. **Nettoyage** : vérifier `gofmt`, `go vet`, `go test ./...`, `node --check`
   sur les JS, `helm lint`.

### P1 — qualité produit

- Ajouter un lien direct “Voir le monitoring” depuis la carte workspace.
- Ajouter un état vide différencié :
  - monitoring désactivé par l'admin ;
  - metrics-server non installé ;
  - Prometheus non configuré ;
  - pas encore de données.
- Ajouter un `aria-label` et une accessibilité correcte sur les graphiques.
- Vérifier les seuils de couleur (requests/limits) : ne pas afficher de faux
  “100 %” quand la limite est absente.
- Éviter le polling inutile quand l'onglet est caché (déjà partiellement géré).
- Ajouter un bouton “Copier le lien” vers `?view=monitoring` pour les admins.

---

## 3. Vision eBPF — le vrai différenciateur

### Pourquoi eBPF

cAdvisor + kube-state-metrics donnent :

- CPU/mémoire/IO block device ;
- réseau bytes/packets au niveau pod ;
- redémarrages, throttling, capacité PVC.

Ils ne répondent pas bien à :

- **quel service externe** un workspace utilise (S3, EFS, API interne, etc.) ;
- **IOPS et latence FS** réels pour les montages réseau / FUSE / CSI ;
- **trafic via tunnel local** (stunnel, proxy CSI, sidecar) ;
- **attribution** quand le trafic réseau sort du pod ou du netns du workspace.

Le but n'est pas de reproduire un APM complet. Le but est de donner au platform
team et au owner une réponse fiable à :

> “Ce workspace consomme quoi, vers quel service, à quelle intensité, et avec
> quelle confiance ?”

### Principe directeur

1. **Baseline sans eBPF** : metrics-server + Prometheus. Si l'agent eBPF n'est
   pas déployé, tout le reste continue de fonctionner.
2. **eBPF optionnel** : DaemonSet séparé, feature flag, désactivé par défaut.
3. **Attribution explicite** : on préfère “inconnu” à une fausse attribution.
4. **Confiance visible** : chaque service externe a un niveau de confiance
   (`high`, `medium`, `low`).
5. **Aucune donnée sensible** : pas de payload, pas de clé S3, pas de nom
   d'objet, pas d'email/owner en label Prometheus.

### Architecture recommandée

```text
Workspace pods
  │  cgroup v2 / netns / mounts
  ▼
[eBPF programs]  (par node)
  ├─ cgroup_skb egress/ingress : bytes, packets, drops
  ├─ tcp_connect / inet_sock_set_state : connexions, dst IP:port
  ├─ udp_sendmsg/recvmsg : DNS, QUIC
  ├─ vfs/fuse/nfs tracepoints : IOPS, bytes, latence, mount/device
  └─ sched_process_exit : cleanup
  ▼
[agent userspace Go]  (DaemonSet, par node)
  ├─ résout cgroup_id → pod UID → workspace
  ├─ résout mount id / device → PV/PVC → workspace
  ├─ corrèle DNS → hostname → service
  ├─ classe les endpoints (S3, EFS, NFS, Kubernetes, externe)
  └─ expose /metrics ou pousse en gRPC au portail
  ▼
Prometheus / portail
  ▼
API monitoring + UI
```

### Options techniques

| Option | Avantages | Limites |
|--------|-----------|---------|
| Cilium Hubble | flows/DNS/HTTP déjà prêts si Cilium | dépend du CNI, pas de FS |
| Pixie | riche, eBPF packagé | lourd, stockage, périmètre |
| Retina | réseau Kubernetes | AKS/CNI, pas FS produit |
| Agent maison `cilium/ebpf` CO-RE | contrôle total, diff produit | à construire, privilèges noyau |
| Agent maison + Hubble optionnel | meilleur des deux | double chemin à maintenir |

**Recommandation** : commencer par un agent maison minimal axé réseau + FS, avec
un adaptateur Hubble optionnel plus tard si Cilium est détecté. Ne pas rendre
Cilium obligatoire.

### Hooks eBPF à viser

Priorité 1 — réseau et services :

- `cgroup_skb/egress` et `cgroup_skb/ingress` : volume par cgroup, sans capturer
  le payload.
- `tracepoint/sock/inet_sock_set_state` ou `kprobe/tcp_connect` : nouvelles
  connexions, `dst_ip`, `dst_port`, `protocol`.
- `kprobe/udp_sendmsg` + `udp_recvmsg` : DNS et UDP.
- Ring buffer pour les nouveaux flux uniquement ; agrégation par CPU en maps.

Priorité 2 — FS / IOPS :

- `fentry/vfs_read`, `vfs_write`, `vfs_fsync` si BTF disponible.
- Tracepoints `fuse_request` / `fuse_reply` pour Mountpoint/S3 CSI.
- Tracepoints NFS (`nfs_*`, `rpc_*`) pour EFS CSI.
- Tracepoints `block_rq_issue` / `block_rq_complete` pour block devices.
- Mesurer : opérations, bytes, latence (histogramme), device/mount, fstype.

À éviter en premier :

- kprobe sur chaque `read`/`write` syscall sans sampling : trop cher.
- capture de payloads, TLS, HTTP bodies.
- résolution reverse DNS à la volée dans le datapath.

### Attribution : le cœur du problème

Ordre de résolution :

1. **cgroup id → pod → workspace**
   - `bpf_get_current_cgroup_id()`.
   - L'agent scanne `/sys/fs/cgroup` (cgroup v2) et/ou interroge le runtime
     (CRI) pour associer cgroup → container ID → pod UID.
   - Fallback : `/proc/<pid>/cgroup` + `/proc/<pid>/mountinfo`.
2. **mount id / device → PV/PVC → workspace**
   - Lire `/proc/<pid>/mountinfo` et les PV/PVC Kubernetes.
   - Pour un montage CSI, mapper `volumeHandle` / `PV name` → PVC → workspace.
   - Indispensable pour NFS/EFS et FUSE où le socket réseau n'est pas dans le
     netns du workspace.
3. **netns inode → pod**
   - Fallback si le cgroup id n'est pas résolu.
4. **proxy local (localhost/stunnel)**
   - Détecter le socket loopback et le processus proxy via
     `/proc/net/tcp` + inodes + `/proc/<pid>/fd`.
   - Corréler avec le socket sortant du proxy.
   - Si la corrélation est impossible, marquer `confidence=low` et
     `service=unknown-proxy`, jamais inventer.
5. **bucket inconnu**
   - `service=unknown`, `confidence=low`, visible admin seulement.

### S3 / EFS / CSI : cas concrets

- **Mountpoint for S3 (FUSE)** : le workspace fait des I/O FUSE ; le réseau sort
  du pod du driver/daemon. Il faut attribuer via le mount FUSE et le PV, pas via
  le netns du workspace. Tracepoints FUSE + mountinfo + PV.
- **EFS CSI (NFS)** : le client NFS est souvent dans le netns hôte / du driver.
  Attribuer via le superblock/mount NFS et le PV, puis le PVC → workspace.
  Les IOPS/latence NFS viennent des tracepoints NFS/RPC, pas de cAdvisor.
- **CSI avec tunneling / stunnel** : le workspace parle à `127.0.0.1:PORT`.
  L'agent doit relier le socket local au processus proxy puis à sa destination
  externe. Si le proxy est partagé, la confiance est `medium` et on agrège par
  volume/PV, pas par pod.
- **Ceph RBD / iSCSI** : block layer + cgroup ; généralement plus simple que
  NFS/FUSE.
- **S3 direct (sans FUSE)** : DNS + SNI + endpoints AWS ; `service=s3`,
  `confidence=high` si hostname `*.s3.*.amazonaws.com`, sinon `medium`.

### Classification des destinations

Ordre :

1. **DNS** : parser les réponses DNS (qname → IP), TTL court, cache borné.
2. **SNI TLS** : parser le ClientHello (sans déchiffrer) pour les connexions
   TLS sans DNS visible ; optionnel et à documenter.
3. **Kubernetes** : watch Services/Endpoints pour mapper ClusterIP → service.
4. **Cloud** : télécharger les plages IP AWS/GCP/Azure (ex. `ip-ranges.json`)
   et mapper les endpoints connus (`s3`, `efs`, `sts`, `ecr`, etc.).
5. **Statique** : config `monitoring.egress.services` pour les endpoints
   internes de l'entreprise.
6. **Inconnu** : `unknown` + `confidence=low`.

Ne jamais faire de reverse DNS bloquant dans le datapath. Le userspace enrichit
après coup.

### Schéma de métriques proposé

```text
poddy_workspace_network_bytes_total{
  workspace, region, direction, protocol, service, endpoint, port
}
poddy_workspace_network_connections_total{
  workspace, region, protocol, service, endpoint, port
}
poddy_workspace_network_dropped_packets_total{workspace, region, direction}
poddy_workspace_fs_bytes_total{
  workspace, region, operation, device, fstype
}
poddy_workspace_fs_operations_total{
  workspace, region, operation, device, fstype
}
poddy_workspace_fs_latency_seconds_bucket{
  workspace, region, operation, device, fstype, le
}
poddy_workspace_external_service_info{
  workspace, region, service, endpoint, confidence
}
```

Règles cardinalité :

- endpoint = hostname résolu ou `ip:port` agrégé, jamais un chemin S3 ;
- cap du nombre d'endpoints par workspace (ex. top 32 + `other`) ;
- labels courts, pas d'owner/email/namespace user dans Prometheus ;
- `service` normalisé (`s3`, `efs`, `nfs`, `https`, `dns`, `kubernetes`,
  `external`, `unknown`, `unknown-proxy`).

### API et UI cibles

API :

```text
GET /api/workspaces/{name}/monitoring/egress?range=1h
GET /api/workspaces/{name}/monitoring/fs?range=1h
GET /api/monitoring/egress?range=1h          (admin)
GET /api/monitoring/fs?range=1h              (admin)
```

Réponse egress :

```json
{
  "available": true,
  "source": "ebpf",
  "confidence": "high",
  "services": [
    {"service": "s3", "endpoint": "bucket.s3.eu-west-3.amazonaws.com",
     "bytes": 123456, "connections": 12, "confidence": "high"}
  ]
}
```

UI :

- Panneau **Services externes** : classement par octets, badge service,
  confiance, sparkline.
- Panneau **Stockage** : IOPS lecture/écriture, débit, latence p50/p95,
  block vs NFS vs FUSE.
- Vue admin : top services toutes régions, top workspaces egress.
- Toujours afficher “source” et “confidence”, jamais un chiffre sans contexte.

### Sécurité et vie privée

- Agent DaemonSet privilégié : `CAP_BPF`, `CAP_PERFMON`, `CAP_SYS_ADMIN`,
  `CAP_NET_ADMIN` selon hooks ; `hostPID`, `hostNetwork` si nécessaire.
- Monter `/sys/fs/cgroup`, `/proc`, `/sys/kernel/debug` en lecture seule quand
  possible ; maps épinglées dans `/sys/fs/bpf`.
- Pas de capture de payload, pas de TLS secrets, pas de noms d'objets S3.
- Les noms DNS/endpoints peuvent être sensibles : affichage admin par défaut,
  owner seulement si `monitoring.egress.visibleToUsers=true`.
- Rate limit, LRU, cap mémoire, feature flag, kill switch.
- Documenter les prérequis noyau et les risques d'un DaemonSet privilégié.

### Performance

Cibles :

- < 1–2 % CPU par nœud à 10k flux/s ;
- mémoire agent < 100–150 MiB ;
- ring buffer borné ;
- agrégation par CPU toutes les 1–10 s ;
- pas de kprobe sur les hot paths sans sampling ;
- exposition Prometheus toutes les 15 s, pas de scrape par pod.

### Tests eBPF

- Unitaires userspace : parse DNS, mapping cgroup, mount/PV, classification
  cloud, merge multi-région.
- Intégration kind/VM avec BTF : `fio`, `dd`, `curl`, `aws s3 cp`, montage EFS
  simulé, FUSE local.
- Vérifier les métriques exposées et l'attribution.
- Bench CPU/mémoire, cardinalité, comportement sous rafale.
- Tests de panne : kernel sans BTF, caps manquantes, agent crash, CNI
  incompatible, Prometheus down.
- Sécurité : pas d'évasion de privilèges, pas de fuite de données, NetworkPolicy.

### Phasage conseillé

1. **P0 — socle** : finir le monitoring metrics-server/Prometheus/UI/tests.
2. **P1 — spike eBPF réseau** : un hook `cgroup_skb` + mapping cgroup → pod →
   workspace sur kind/VM ; mesurer le coût.
3. **P2 — services externes** : DNS + classification cloud + endpoint
   Kubernetes + UI “Services externes”.
4. **P3 — FS/CSI** : FUSE, NFS/EFS, block layer, mount → PV → workspace,
   IOPS/latence, panneau Stockage.
5. **P4 — FinOps** : OpenCost + egress bytes + price book, showback/chargeback,
   snapshots agrégés.
6. **P5 — multi-région** : agrégation egress/FS par hub, cache et réconciliation.

Chaque phase doit être indépendamment désactivable.

---

## 4. Pièges à éviter

- Ne pas ajouter une TSDB maison dans PostgreSQL pour les métriques brutes.
- Ne pas utiliser `kubectl top` ou `kubectl exec` pour lire les métriques.
- Ne pas retourner `0` quand une métrique est absente : retourner `null` +
  `reason`.
- Ne pas accepter de PromQL arbitraire depuis le navigateur.
- Ne pas mettre owner/email dans les labels Prometheus.
- Ne pas dédupliquer cAdvisor/KSM dans `/metrics` du portail.
- Ne pas réécrire l'historique financier avec les teams actuelles.
- Ne pas attribuer un service externe sans preuve : `unknown` vaut mieux qu'un
  faux positif.
- Ne pas oublier le mode hub/région : live et history par workspace passent par
  la région, le global agrège.
- Ne pas oublier les installations sans metrics-server / sans Prometheus.

---

## 5. Décisions encore ouvertes

- Agent eBPF maison vs Hubble/Pixie en première intention.
- Exposition des services externes aux owners ou admin seulement (défaut
  recommandé : admin, flag pour owners).
- Faut-il parser SNI ou se limiter à DNS + plages cloud ?
- Rétention des agrégats egress (7 j ? 30 j ? snapshots quotidiens ?).
- Modèle de coût : OpenCost seul ou price book interne pour les services non
  couverts.
- Showback vs chargeback.
- Cardinalité maximale acceptée par workspace.
- Support noyaux anciens : refuser l'agent ou fallback cAdvisor uniquement ?

---

## 6. Commandes utiles

```bash
# Backend
go build ./...
go vet ./...
go test ./...

# JS
node --check web/monitoring.js
node --check web/core.js
node --check web/app.js
node --check web/workspaces.js

# Helm
helm lint deploy/helm/poddy \
  --set auth.token=test \
  --set database.url=postgres://x \
  --set ssh.publicHost=example.com

helm template poddy deploy/helm/poddy \
  --namespace poddy-system \
  --set auth.token=test \
  --set database.url=postgres://x \
  --set ssh.publicHost=example.com \
  --set monitoring.prometheus.url=http://prometheus:9090 \
  >/tmp/poddy-monitoring.yaml

# Docs
npm install
npm run build
```

---

## 7. Checklist avant merge final

- [ ] Smoke test navigateur détail + vue admin.
- [ ] Test sans Prometheus et sans metrics-server.
- [ ] Test mode hub avec 2 régions.
- [ ] Vérifier les graphiques IOPS / réseau / disque.
- [ ] `go test ./...` et `go vet ./...`.
- [ ] `helm lint` + `helm template`.
- [ ] `npm run build` pour la doc.
- [ ] Aucune valeur `0` inventée, tous les états `unavailable` sont explicites.
- [ ] Les coûts restent invisibles aux users par défaut.
- [ ] Le plan eBPF est validé avant d'ajouter un DaemonSet privilégié.
