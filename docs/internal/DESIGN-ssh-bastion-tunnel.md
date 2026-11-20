# Bastions SSH derrière le portail — note de conception

> Dernière mise à jour : 2026-11-19
>
> État : implémenté sans bastion ni relais séparé. Le portail hub termine SSH
> en processus, sert le broker d'agents, et joint les régions par leur tunnel
> WSS sortant (`tcp:<workspace>:22`). Aucune exposition réseau par région.
> Objectif : faire du portail l'unique point d'entrée SSH. Bénéfice direct pour
> le multi-région : une seule règle firewall/NAT, `sshHost`/`sshPort` publics
> deviennent inutiles côté région.
> Contexte multi-région : voir `PLAN-scale-regions.md` §0 (« SSH est le seul
> flux qui ne se route pas comme HTTP »).

## Constat

- Le bastion ne sert qu'à du `direct-tcpip` et résout `poddy-ssh-<workspace>`
  vers un workspace (`internal/bastion/bastion.go:245`, `:509`). Les noms
  préfixés par la région (`poddy-ssh-eu-app`) existent déjà dans les tests
  (`internal/bastion/bastion_test.go`, `internal/server/desktop_test.go`).
- Le client reçoit un `ProxyCommand` généré côté UI/extension
  (`vscode-extension/extension.js:196`) : le format est centralisé, donc
  changeable sans casser les utilisateurs.
- Le portail a déjà l'auth (OIDC + jeton partagé), le RBAC par workspace et des
  tickets signés courts (`internal/server/desktop.go`, TTL 2 min) utilisables
  pour autoriser un tunnel.
- `ssh.requireAgent=true` fait déjà sortir les workspaces en outbound-only via
  l'agent (`:8091`). Seul le flux utilisateur → bastion reste entrant.

## Options

### Option A — Bastion du hub comme routeur SSH

- Seul le bastion hub est exposé. Sur `direct-tcpip` vers
  `poddy-ssh-<region>-<workspace>`, il recopie le flux TCP brut vers le
  **sshd du workspace**, pas vers le bastion régional : le handshake SSH du
  client est déjà terminé au hub, le payload du canal est destiné au pod.
- **Attention cross-cluster** : un Service ClusterIP n'est joignable que dans
  son cluster. Le hub ne peut donc pas dialer `poddy-ssh-<workspace>` d'un autre
  cluster EKS. Le saut passe par le tunnel WSS région existant :
  `RegionBroker.Dial("eu", "tcp:<workspace>:22")`, et l'agent région fait un
  `PortForward` local vers le pod. Aucun Service bastion régional n'est requis.
- Aucun changement client : le `ProxyCommand` actuel continue de fonctionner,
  il pointe juste sur `sshHost` = hub.
- Coût : le hub devient une dépendance de routage (SPOF si 1 replica) ; le
  relais ajoute une connexion WS interne bastion → portail.
- Sécurité : le portail vérifie le certificat SSH court (CA, principal, scope
  workspace, owner/admin + approbation) avant d'ouvrir le stream. Sans
  certificat, pas de forwarding cross-région (clé simple refusée en v1).

### Option B — Tunnel authentifié via le portail (recommandé à terme)

- Le portail expose un relais TCP multiplexé (WebSocket `wss://…/ssh/<region>`
  ou HTTP `CONNECT`) vers le bastion de la région, qui passe en ClusterIP.
  Comme en A, le portail ne joint pas le ClusterIP distant : le relais réutilise
  le tunnel WSS région (target `tcp:<bastion>`), l'agent région dialant le
  ClusterIP localement.
- Auth au relais : session OIDC ou ticket signé court (même infra que
  `desktop.go`), puis cert SSH court au bastion. Le portail audite chaque
  connexion.
- Client : `ProxyCommand` avec un helper. WS : tunnel embarqué dans l'extension
  (invisible pour VS Code) ; CLI : petit binaire ou `wstunnel`. CONNECT :
  `nc -X connect -x portal:443 %h %p`.
- Avantages : un seul port public, traverse Traefik/CDN (WS), régions
  sortantes-only, auth/audit centralisés.
- Coût : moyen. Hijack HTTP ou upgrade WS, désactiver les timeouts sur la
  connexion, keepalive, limitation de connexions, génération du ProxyCommand
  côté UI/extension, doc utilisateur.

### Option C — Routeur TCP L4 « générique »

- Impossible sans terminaison protocolaire : en TCP brut il n'y a pas d'info de
  région/workspace. Retombe sur A ou B.

## Recommandation

- Court terme : **Option A** (diff réduit, zéro changement client).
- Cible : **Option B**, cohérente avec la cible « portail central + N
  data-planes régionaux » ; A peut rester comme mode de secours explicite.

## Esquisse d'implémentation

Option B :

1. `internal/server` : route `GET /ssh/{region}` (upgrade WS) ou `CONNECT`,
   auth par ticket/session, résolution `region → Service` (DNS in-cluster).
2. Réutiliser le `Forwarder` existant (`internal/server/vscode.go`) comme base
   de cycle de vie des tunnels : start/stop par workspace et par identité.
3. Chart : `ssh.service.type=ClusterIP` par défaut pour les régions, NetworkPolicy
   « ingress bastion uniquement depuis les pods du portail », values
   `ssh.expose.external=false`.
4. UI/extension : générer
   `ProxyCommand <helper> --ticket <t> wss://portal/ssh/<region>` ; le helper
   ne fait que recopier stdin/stdout.
5. Rétro-compat : garder `ssh.publicHost`/`ssh.service.type=NodePort` comme
   option, avec avertissement dans les values.

Option A (implémentée) :

1. `internal/tunnel/region.go` : cible `tcp:<workspace>:<port>`,
   `RegionAgentConfig.DialWorkspace` (PortForward local), `resolveTarget`.
2. `internal/tunnel/wsconn.go` : `net.Conn` sur WebSocket binaire.
3. `internal/bastion/bastion.go` : si le workspace est local, comportement
   inchangé ; sinon relais vers `PODDY_PORTAL_URL` avec l'en-tête
   `X-Poddy-SSH-Cert`.
4. `internal/bastion/bastion.go` : `scopedCertAuthorized` vérifie le certificat,
   applique owner/admin + approbation, puis `DialRegionWorkspace` ouvre
   `RegionTunnel.Dial(region, "tcp:<workspace>:22")`.
5. Chart : le portail hub reçoit `-ssh-addr=:2222`, `-agent-addr=:8091` et
   `PODDY_POD_IP`/`PODDY_POD_NAMESPACE`; une région reçoit `-ssh-addr=` vide.
6. HA : `internal/tunnel/presence.go` + `internal/k8s/presence.go` publient des
   Leases par workspace/région; toute réplique forward le flux brut vers la
   réplique propriétaire via `/api/internal/dial` (HMAC).
7. Tests : parsing de cible, signature HMAC, Leases, et aller-retour WS `tcp:`.

## Implémentation (portal-native)

- Portail hub : SSH en processus, broker d'agents WSS, forwarding pair-à-pair.
  `sshHost`/`sshPort` restent ceux du hub, y compris pour les workspaces
  régionaux.
- Aucun `PODDY_SSH_RELAY`, aucun `PODDY_PORTAL_URL`, aucun Deployment bastion.
  Le binaire `poddy-bastion` reste un point d'entrée legacy non déployé.
- Secret CA SSH **partagé** entre le hub et chaque région : le hub signe, le
  portail région vérifie. Un CA par cluster casse le relais.
- Auth : certificat SSH court obligatoire. Le bastion transmet
  `X-Poddy-SSH-Cert` (base64) ; le portail revalide CA, principal, scope
  workspace, puis owner (ou admin avec approbation du owner via `Access`).
  Les connexions par clé simple (sans certificat) ne sont pas relayées en v1.
- Réseau : le bastion appelle le portail en HTTP in-cluster ; le portail écrit
  dans le WSS que la région a déjà ouvert. Aucun flux entrant vers la région.

## Sécurité (dans les deux options)

- Ne pas transformer le relais en proxy ouvert : exiger un ticket/session
  courte par connexion et vérifier l'appartenance du workspace (RBAC/quotas).
- NetworkPolicy : bastions joignables uniquement depuis le portail (ou depuis
  le hub en option A).
- Conserver la révocation : désenregistrer une région doit couper les nouveaux
  tunnels ; les tunnels existants expirent avec le cert (15 min) et/ou le TTL
  du ticket.
- Audit : émettre un événement `workspace.ssh.connect` avec région, workspace,
  identité et résultat (`internal/server/audit.go`).

## Questions ouvertes

- WS ou CONNECT ? WS traverse mieux les ingress/CDN et permet l'extension ;
  CONNECT ne demande aucun helper hors `nc`.
- Faut-il garder une exposition directe par région pour les cas d'usage
  « latence minimale » (A en option, B par défaut) ?
- Limite de connexions simultanées par utilisateur et par région.
- Comportement si le portail est en HA : le tunnel est stateless (TCP), donc
  compatible avec plusieurs replicas derrière un Service — contrairement au
  WSS des region managers.

## Schéma — relais SSH via le WSS sortant (option A cross-cluster)

```
Utilisateur (VS Code / ssh)
   │  ProxyCommand → sshHost = hub
   │  TCP entrant (443/2222) vers le hub
   ▼
┌────────────────────── Cluster PRIMAIRE (hub) ──────────────────────────┐
│  bastion hub                                                           │
│     │  reçoit un canal SSH "direct-tcpip"                              │
│     │  cible = poddy-ssh-eu-<ws>                                       │
│     │  GET /api/internal/region/ssh + X-Poddy-SSH-Cert                │
│     ▼                                                                  │
│  portail : revérifie le cert, owner/admin, approbation                 │
│     │  RegionBroker.Dial("eu", "tcp:<ws>:22")                          │
│     ▼                                                                  │
│  session WSS déjà ouverte (initiée par l'agent EU, sortante 443)       │
└─────┬──────────────────────────────────────────────────────────────────┘
      │  frames open/data/close, multiplexés sur CETTE connexion unique
      ▼
┌────────────────────── Cluster EU / APE ────────────────────────────────┐
│  region agent                                                          │
│     │  frameOpen("tcp:<ws>:22")                                        │
│     ▼                                                                  │
│  Forwarder.EnsurePort → port-forward local vers le pod                │
│     ▼                                                                  │
│  workspace Pod :22   (sshd, dial local intra-cluster)                  │
└────────────────────────────────────────────────────────────────────────┘
```

Points clés :

- Le hub **ne joint jamais** l'IP du cluster région. Il n'ouvre aucune
  connexion vers la région : il écrit dans le WSS que la région a déjà ouvert
  vers lui (même flux que l'API region-http actuelle).
- `tcp:` n'est pas une connexion réseau hub → région : c'est le nom de la cible
  dans le protocole. C'est l'agent région qui résout le ClusterIP **depuis
  l'intérieur** de son cluster.
- Aucun LB, aucun peering, aucun subnet public, aucune règle entrante côté
  région : seul le 443 sortant région → hub est nécessaire (déjà requis pour le
  control plane).
- `resolveTarget` n'accepte que `region-http` (cible fixe) et
  `tcp:<workspace>:<port>` ; le workspace et le port sont validés, puis
  `DialWorkspace` passe par le `Forwarder` local (pas de dial arbitraire).

Sans ça, il faut soit un LB joignable (interne + PrivateLink/TGW, ou public),
soit exposer directement chaque bastion.
