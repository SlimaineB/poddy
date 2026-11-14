# Scaling et HA des régions — notes de travail

État : propositions, rien n'est implémenté ici.
Périmètre : (1) tunnels multi-région, (2) mise à l'échelle du control plane.
Le build multi-arch CI et la licence sont hors scope.

## 0. Modèle cible (validé)

- **Une seule base et un seul portail**, dans la région primaire.
- **Chaque région est indépendante** : son region manager possède l'API
  Kubernetes, les pods, les PVC et le bastion SSH, et ouvre un WSS sortant vers
  le primaire.
- Le portail garde les utilisateurs, le RBAC, les templates et l'audit.
- La région peut appeler l'API interne du portail (accès, quotas). C'est le
  sens région → portail, pas le trafic utilisateur.

Le **seul** point qui empêche la HA du portail : pour le terminal, les IDE et
les ports, le portail doit joindre la région (portail → région). Un WSS sortant
atterrit sur un pod précis. Deux réponses simples, sans mesh :

1. **Portail à 1 replica** : zéro code, on assume que le control plane n'est
   pas HA.
2. **Region gateway séparé** : un petit Deployment (1 pod, sans BDD) qui porte
   les WSS ; le portail HA lui forwarde le trafic région via un Service
   interne. Les régions ne changent pas.

Le mesh WireGuard/DERP de Coder répond à un autre besoin (P2P, latence,
multi-cloud). Ce n'est pas nécessaire pour ce modèle.

### Cible si aucun composant mono-replica n'est acceptable

Le portail et le region gateway tournent à N replicas. La seule chose à
partager est **qui possède quel tunnel** :

- Chaque region manager garde un WSS sortant vers le Service du gateway.
- À l'acceptation, le pod gateway enregistre la propriété de la région dans un
  Lease Kubernetes (`poddy-region-<region>`, `holderIdentity` = nom du pod) ou
  une table PG (`region_gateway_owners`) avec heartbeat.
- Une requête pour la région R peut arriver sur n'importe quel pod gateway :
  il lit le Lease. S'il est propriétaire, il sert localement ; sinon il
  forwarde vers le pod propriétaire (DNS headless/StatefulSet ou IP stockée
  dans le Lease).
- Mort du propriétaire : le Lease expire, la région se reconnecte à un autre
  pod et reprend la propriété. Les connexions en cours tombent, le client
  reconnecte (même comportement qu'un restart actuel).
- Résultat : portail HA + gateway HA, sans sticky et sans mesh.

Variante encore plus simple : si l'inbound vers les régions est acceptable
(VPN/peering/private link), supprimer le gateway et appeler directement l'API
du region manager. C'est le moins de code et le plus HA ; le WSS n'existe que
pour éviter l'inbound.

### Variante « N portails, un par région » (transparente pour le user)

Principe : le trafic d'un workspace est servi par le portail de **sa** région.
Plus de tunnel inter-région, donc plus d'affinité. Le user garde une seule URL.

Deux façons de rendre ça transparent :

1. **Edge router stateless (recommandé)** : un hostname unique, N replicas,
   sans BDD. Il résout `workspace → région` (cache + table PG) et forwarde
   HTTP/WSS au portail régional en mTLS. Les pages globales (liste, users,
   quotas) vont au portail primaire. Auth terminée à l'edge ou au primaire :
   un seul cookie, pas de re-login.
2. **Hostnames régionaux + redirect** : `eu.`, `us.`, etc. Cookie sur
   `.example.com` et même clé de session. La liste globale lit le miroir PG et
   redirige vers la région du workspace. Plus simple, mais l'URL change.

Prérequis : l'edge doit pouvoir joindre chaque région (inbound, VPN, peering).
Si les régions sont sortantes-only, l'état des tunnels remonte à l'edge et il
faut le registre partagé + forwarding ; l'edge seul ne supprime pas le problème,
il le déplace.

Coût : la BDD centrale devient la dépendance globale. Chaque portail régional
qui écrit présence/quotas/accès paie la latence inter-région. Mitigation :
router seulement le **data plane** (terminal, ports, IDE, bastion) vers la
région ; garder le **control plane** (UI, RBAC, quotas, templates) au primaire,
avec un jeton court signé vérifié localement par le data plane.

SSH est le seul flux qui ne se route pas comme HTTP : soit hostnames bastion
régionaux visibles dans l'UI, soit un routeur SSH global qui lit
`workspace@` et forward en TCP.

Cible incrémentale : **un portail central HA + N data-planes régionaux + edge
router**. « N portails complets » marche aussi mais ajoute l'agrégation
globale et la latence BDD.

## 1. Regions : le problème

« Région » dans poddy = un cluster enregistré (nom + `hubUrl` + clé), pas
forcément une zone géographique. Plusieurs clusters dans la même région sont
donc plusieurs entrées `regions` : **même problème d'affinité**, puisque le
WSS est par cluster et atterrit sur un pod.

Ce qui change en same-region :

- La latence est faible. Si les clusters sont joignables (VPC/peering), la
  variante « appel direct, sans tunnel » est quasi gratuite : le portail appelle
  l'API du manager de chaque cluster. C'est le cas où il faut la préférer.
- Si les clusters sont isolés (pas de peering), même contrainte sortante-only
  → même gateway/registre.
- La BDD centrale n'est plus un problème de latence, mais l'agrégation globale
  reste si l'UI couvre plusieurs clusters.
- Aujourd'hui il n'existe pas de mode « direct » : tout cluster déclaré dans
  `regions` passe par le WSS. Le pivot reste la joignabilité réseau, pas la
  géographie.

Chaque region manager ouvre un WSS sortant vers le hub. La session vit dans le
process d'**un** pod hub (`internal/region/hub.go`, `internal/tunnel`). Les
autres replicas ne savent pas router vers cette région, d'où la consigne
actuelle « hub à 1 replica ou sticky routing ». Un ingress L7 classique ne
permet pas de cibler le pod qui possède le tunnel.

### Option A — court terme : assumer le hub mono-replica

- `fail` Helm si `replicaCount > 1` et `regions` non vide, avec override
  explicite (`regions.allowMultipleHubReplicas: true`).
- Documenter que la HA du hub et les tunnels région sont incompatibles
  aujourd'hui.
- Ajouter des métriques : tunnel connecté par région, dernier message,
  nombre de reconnexions, erreurs de routage. Alerte si un tunnel est absent.
- Effort : ~1 PR. Risque : aucun changement d'archi.

### Option B — cible recommandée : region gateway séparé

- Nouveau Deployment `poddy-region-gateway` (1 replica), exposé par l'ingress
  sur `/api/internal/region/tunnel` (WSS) et par un Service interne. Pas de
  BDD, pas d'état métier : uniquement les WSS vivants.
- Les region managers continuent de dialer `hubUrl` ; le portail (toutes
  replicas) forwarde les requêtes région vers le gateway via un Service
  interne.
- Portail stateless et HA ; un seul composant porte les tunnels. Restart du
  gateway = reconnexion WSS des régions, coupure de quelques secondes, pas de
  perte de données.
- HA du gateway si nécessaire : sharding par région (chaque région pointe vers
  un gateway donné) plutôt qu'un Service multi-replica, sinon on retombe sur
  le problème d'affinité.
- Effort : moyen. C'est un composant séparé, pas un mesh.

**Variante sans tunnel** : si chaque région est joignable en entrée depuis le
primaire (VPN, peering, private link), le portail appelle directement l'API du
region manager avec l'identité signée. Plus de WSS ni de gateway, portail HA.
Le tunnel actuel n'existe que pour éviter l'inbound vers les régions.

### Option C — non retenue : tunnels multi-hub / mesh

- Chaque region manager ouvrirait un WSS vers chaque pod hub, ou un mesh
  WireGuard/DERP type Coder.
- Suppose un accès L4/DNS vers tous les pods ou une infra DERP. Surdimensionné
  par rapport au modèle cible.
- À ne réévaluer que si le multi-région devient central (P2P, latence,
  multi-cloud).

### Recommandation

1. Option A tout de suite (garde-fou + métriques).
2. Option B comme cible si la HA du portail est nécessaire.

## 2. Scale : points chauds

Constats :

- `internal/server/workspaces.go:19` liste **tous** les workspaces à chaque
  requête, puis pagine en mémoire. `web/app.js:36` poll toutes les 5 s par
  onglet visible.
- `internal/server/access.go:347` throttle la présence 30 s **en mémoire par
  replica**.
- `internal/k8s/state.go:29` stocke le `corev1.PodSpec` complet de chaque
  workspace arrêté dans un seul ConfigMap `poddy-state` (limite 1 MiB).

### 2a. Read model via informers (le vrai fix)

- `SharedIndexInformers` sur les Pods et le ConfigMap `poddy-state`, index en
  mémoire par nom/owner/région/phase.
- `/api/workspaces` lit le cache ; recherche, tri et pagination serveur
  deviennent gratuits. Plus d'appels API par poll.
- Effort : moyen. C'est le prérequis pour des centaines de workspaces
  confortables.

### 2b. Polling moins coûteux

- Ajouter `ETag` / `If-None-Match` sur `/api/workspaces`, basé sur la
  resourceVersion de l'index ; répondre 304 quand rien n'a changé.
- Intervalle adaptatif : 5 s après une action, 30–60 s sinon. Le pause quand
  l'onglet est caché existe déjà.
- Option ultérieure : SSE (`/api/workspaces/events`) pour pousser les
  changements et supprimer le polling.
- Effort : faible.

### 2c. Présence partagée

- Déplacer le throttle dans PostgreSQL :
  `ON CONFLICT ... DO UPDATE ... WHERE access_presence.last_seen < now() - interval '25 seconds'`.
- Le cache mémoire devient une optimisation, pas une condition de correction.
- Garder le prune périodique (`maybePrune`).
- Effort : faible.

### 2d. Limite 1 MiB de `poddy-state`

- Option simple : sharder en N ConfigMaps (`poddy-state-<hash>` par nom ou
  owner), migration par lecture de tous les shards. Conserve le mode sans DB.
- Option radicale : PostgreSQL comme source des specs arrêtés, ConfigMap en
  bootstrap/cache. Plus invasif, casse la persistance sans DB.
- Effort : moyen.

## 3. Ordre conseillé

| Priorité | Chantier | Effort | Gain |
|----------|----------|--------|------|
| P0 | Garde-fou régions + métriques tunnel | faible | évite une config cassée |
| P0 | ETag/304 + intervalle adaptatif | faible | moins d'appels API |
| P0 | Throttle présence dans PG | faible | moins d'écritures, correct en HA |
| P1 | Informers pour la liste | moyen | scale horizontal réel |
| P2 | Sharding de `poddy-state` | moyen | dépasse la limite 1 MiB |
| P3 | Tunnel gateway dédié | moyen | hub HA avec régions |

## 4. Ce que fait Coder (référence)

Coder a remplacé son ancien stack WebRTC par **Tailscale OSS sur WireGuard**
(issue coder/coder#2779, 2022). Conséquences :

- Chaque workspace exécute un agent qui se connecte en sortant au control plane
  (`CODER_ACCESS_URL`) et rejoint un tailnet. Les connexions sont directes
  (P2P, NAT traversal via STUN) et basculent sur des **relais DERP** quand le
  direct échoue. Tout est chiffré de bout en bout.
- `coderd` embarque un serveur DERP. Pour la HA, `--derp-server-relay-url`
  permet aux replicas de se relayer le trafic DERP entre eux.
- Les **workspace proxies** (édition Premium) sont déployés près des users,
  sans connexion à la base, et relaient SSH/HTTP/WebSocket vers les workspaces
  via le tailnet. Comme c'est un mesh, n'importe quel proxy peut joindre
  n'importe quel workspace : pas de session « possédée » par un pod.
- Multi-région : un seul control plane, des workspace proxies régionaux et des
  relais DERP. Les appels dashboard/API ne passent pas par les proxies.

Références :

- https://coder.com/docs/admin/networking
- https://coder.com/docs/admin/infrastructure/architecture
- https://coder.com/docs/admin/networking/workspace-proxies
- https://github.com/coder/coder/issues/2779

### Implication pour poddy

Le problème du tunnel « possédé par un pod hub » vient du modèle WSS centralisé.
Coder l'évite par un mesh WireGuard + DERP. Deux stratégies possibles :

1. Court/moyen terme : garder WSS mais isoler les tunnels dans un gateway
   (option B), ou accepter le hub mono-replica (option A).
2. Long terme : passer à un modèle mesh (WireGuard/tsnet ou équivalent) pour
   supprimer la notion de tunnel attaché à un replica. C'est un changement
   d'architecture majeur, à ne faire que si le multi-région devient central.

## 5. Décisions ouvertes

- [ ] Option A, B ou C pour les régions ?
- [ ] Informers : Pods seulement, ou aussi `poddy-state` / volumes ?
- [ ] Sharding `poddy-state` : par owner (isolation) ou par hash (équilibrage) ?
- [ ] Faut-il un endpoint SSE ou l'ETag suffit-il ?
- [ ] Le tunnel gateway doit-il être un composant du chart `poddy` ou un chart
      séparé ?
