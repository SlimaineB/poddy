# Mesh SSH : retour d'expérience et guide de dépannage

> Document interne de retour d'expérience. Les noms de workspaces, d'utilisateurs,
> d'hôtes et de clés sont des exemples : ne jamais copier un token ou une preauth
> key réelle dans un fichier versionné.

## Objectif

Obtenir une connexion SSH fiable vers un workspace Kubernetes, y compris depuis
Windows et depuis VS Code Remote-SSH, avec :

- une identité mesh stable pour le workspace (pas un nouveau nœud à chaque restart) ;
- des clients desktop éphémères et nettoyés après chaque connexion ;
- un choix déterministe du bon pair quand Headscale conserve des nœuds fantômes ;
- le port forwarding nécessaire au VS Code Server.

Le chemin réseau est le suivant :

```text
SSH client (Windows)
  └─ ProxyCommand poddy-connect --mode mesh
       └─ tsnet (client éphémère)
            └─ Headscale / DERP
                 └─ tsnet (agent du workspace, identité persistante)
                      └─ sshd du workspace (127.0.0.1:22)
                           └─ VS Code Server (127.0.0.1:<port aléatoire>)
```

## Symptômes rencontrés

| Symptôme | Contexte |
|---|---|
| `fetch control key: context deadline exceeded` | Le client n'arrive pas à joindre le control server Headscale. |
| `Authkey is set; but state is NoState. Ignoring authkey.` | L'agent workspace reste bloqué au premier démarrage. |
| `mesh: workspace peer "<nom>" is offline` | Un ancien nœud Headscale hors ligne masque le nouveau nœud en ligne. |
| `Failed to set up dynamic port forwarding connection over SSH to the VS Code Server` | Le serveur SSH refuse les canaux `direct-tcpip` utilisés par `ssh -D`. |
| `refused local port forward: originator ... target ...` | Même problème vu côté `sshd`. |
| `Certificate critical option "permit-X11-forwarding" is not supported` | Les options `permit-*` ont été placées au mauvais endroit dans le certificat. |
| `connect_to 127.0.0.1 port <port>: failed` | Le forwarding est autorisé, mais le VS Code Server n'écoute plus sur ce port. |

## Problème 1 — DNS64/NAT64 et control server

### Symptôme

Sur Windows/WSL, le nom du control server se résout en IPv6 NAT64
(`64:ff9b::...`). Le TCP IPv6 semble fonctionner, mais le TLS reste bloqué et
tsnet finit par :

```text
fetch control key: context deadline exceeded
```

Le même nom en IPv4 fonctionne.

### Cause

Le résolveur système renvoie une adresse AAAA synthétique NAT64. Le chemin IPv6
n'est pas réellement utilisable jusqu'au control server, mais le client SSH
garde l'adresse IPv6 et ne bascule pas assez vite vers IPv4.

### Correctif

Un proxy DNS local, dans le processus, filtre les AAAA uniquement pour l'hôte du
control server et renvoie les A IPv4. Le proxy est branché sur le resolver de
tsnet avant le démarrage du nœud.

Fichiers :

- `internal/mesh/dns_ipv4.go`
  - `preferIPv4Control`
  - `installIPv4DNSForwarder`
  - `serveIPv4DNS`
  - `handleDNSQuery`
  - `pipePacketConn`
  - `nat64Prefixes`
  - `isNAT64`
  - `normalizeDNSName`
- `internal/mesh/dns_ipv4_test.go`

Point important : le proxy doit utiliser un `net.PacketConn` (paquet), pas un
`net.Conn` (flux). Un flux TCP ne transporte pas correctement les datagrammes
DNS et le resolver reste muet.

### Validation

```bash
# Le control server doit répondre en IPv4.
curl -vk https://<control-host>/health

# Les logs tsnet doivent dépasser "fetch control key".
# Le client doit afficher le nœud dans Headscale.
```

## Problème 2 — Agent bloqué sur `NoState`

### Symptôme

Au premier démarrage du workspace, l'agent logue :

```text
Authkey is set; but state is NoState. Ignoring authkey. Re-run with TSNET_FORCE_LOGIN=1 to force use of authkey.
```

Le pod tourne, mais le nœud n'apparaît jamais en ligne dans Headscale.

### Cause

`tsnet` ignore volontairement `AuthKey` quand son état local est `NoState`,
sauf si `TSNET_FORCE_LOGIN=1`. Sans état `tailscaled.state` valide, le nœud ne
fait donc pas le login initial.

### Correctif

`forceLoginForNewState` vérifie si `tailscaled.state` est absent ou vide avant
`mesh.Start`. Si c'est le cas, l'agent force le login :

```text
TSNET_FORCE_LOGIN=1
```

Fichier : `internal/mesh/mesh.go` (`forceLoginForNewState`, appelé dans `Start`).

### Piège

Le message `Ignoring authkey` peut encore apparaître alors que le nœud rejoint
correctement le mesh : c'est le comportement normal quand tsnet recharge un état
existant. Toujours vérifier la ligne suivante :

```text
workspace <nom> joined the mesh as <hostname>
```

et l'état `online` dans Headscale, plutôt que de conclure sur ce seul log.

## Problème 3 — Identité mesh perdue au restart

### Symptôme

À chaque redémarrage du pod :

- un nouveau nœud Headscale apparaît ;
- l'ancien nœud reste hors ligne ;
- le nom d'hôte est dupliqué ou suffixé ;
- les clients peuvent tomber sur un pair mort.

### Cause

L'état tsnet était stocké dans un `emptyDir`, donc perdu avec le pod. Le nœud
workspace était également éphémère.

### Correctif

Persister l'état mesh sur le PVC du workspace, et rendre le nœud workspace non
éphémère.

- Constantes et helpers dans `internal/k8s/podenv.go` :
  - `workspaceVolumeName = "workspace"`
  - `agentMeshMountPath = "/var/lib/poddy/mesh"`
  - `findWorkspaceVolume`
  - `mountAgentMeshState`
  - `workspaceMountPath`
- Le sidecar `poddy-agent` monte le volume `workspace` à
  `/var/lib/poddy/mesh`.
- `PODDY_MESH_STATE_DIR=/var/lib/poddy/mesh/.poddy/tsnet`.
- Pour le mode sans sidecar, `applyMeshEnv` utilise
  `<workspaceMount>/.poddy/tsnet`.
- `cmd/poddy-agent/main.go` passe `Ephemeral: false`.

### Validation

```bash
# Le fichier d'état doit être sur le PVC, pas dans un emptyDir.
kubectl -n <workspace-ns> exec <pod> -c workspace -- \
  ls -l /home/dev/.poddy/tsnet/tailscaled.state

# Après suppression du pod, le même nœud Headscale et la même IP doivent revenir.
kubectl -n <workspace-ns> delete pod <pod>
kubectl -n <workspace-ns> get pod <pod> -w
kubectl -n <headscale-ns> exec deploy/<headscale> -- \
  headscale --config /etc/headscale/config.yaml nodes list -o json
```

Le test doit montrer le même `id` et la même IP avant/après restart.

## Problème 4 — Nœuds fantômes et sélection du pair

### Symptôme

Le workspace est en ligne, mais le client échoue avec :

```text
mesh: workspace peer "<nom>" is offline
```

Headscale contient plusieurs nœuds avec le même `HostName` ; le nouveau nœud a
un `given_name` suffixé (`ws-<nom>-<suffixe>`), tandis que l'ancien nœud garde
le nom exact et reste hors ligne.

### Cause

Un parcours naïf de la map `status.Peer` peut sélectionner le premier nœud ou
préférer systématiquement le nom exact, même s'il est mort.

### Correctif

`selectPeer` et `betterPeer` dans `internal/mesh/mesh.go` :

1. correspondance exacte du `HostName` ou du `DNSName` avant un préfixe ;
2. nœud `Online` avant un nœud hors ligne ;
3. `CurAddr` (chemin direct) avant un nœud relayé ;
4. `LastSeen` le plus récent.

Les tests `TestSelectPeerPrefersHealthyDuplicate` couvrent le cas du doublon
sain.

### Piège

Un nom exact hors ligne peut encore masquer un doublon en ligne si le nouveau
nœud n'a pas le même `HostName`. Dans ce cas :

```bash
# Lister les doublons.
kubectl -n <headscale-ns> exec deploy/<headscale> -- \
  headscale --config /etc/headscale/config.yaml nodes list -o json \
  | jq -r '.[] | select(.name|startswith("<prefix>")) | [.id,.name,.given_name,.online] | @tsv'

# Supprimer les anciens nœuds hors ligne.
kubectl -n <headscale-ns> exec deploy/<headscale> -- \
  headscale --config /etc/headscale/config.yaml nodes delete -i <id> --force
```

Ne jamais supprimer un nœud en ligne sans vérifier son IP et son workspace.

## Problème 5 — ProxyCommand, état partagé et timeouts

### Symptôme

- Blocage long (180 s) avant l'échec.
- Messages de verrou tsnet entre deux tentatives.
- Nœuds client qui flappent ou se réutilisent entre connexions concurrentes.
- VS Code perd le tunnel après le script de démarrage.

### Cause

Un répertoire d'état tsnet partagé entre invocations du ProxyCommand, un timeout
de join trop long, et une fermeture du tunnel dès que `stdin` atteint EOF.

### Correctif

`internal/meshclient/run.go` :

- répertoire d'état par invocation :
  `<state-dir>/<hostname>-<pid>` ;
- suppression du répertoire en fin d'invocation ;
- `TSNET_FORCE_LOGIN=1` pour le client ;
- `Ephemeral: true` et `RandomPort: true` ;
- timeout de join à 60 s ;
- gestion du half-close : `stdin` EOF n'est pas une raison de fermer le tunnel,
  car VS Code garde la session ouverte pour `-D`. On attend la fin de `stdout`
  ou l'annulation du contexte.

### Validation

```bash
# Une connexion simple doit rendre la main proprement.
ssh -F <ssh-config> <host> 'echo ok'

# Le nœud client doit disparaître après la fermeture (éphémère).
# Le répertoire d'état ne doit pas être réutilisé par une autre invocation.
```

## Problème 6 — VS Code Remote-SSH et port forwarding

### Symptôme

VS Code affiche :

```text
Failed to set up dynamic port forwarding connection over SSH to the VS Code Server.
```

Le log `sshd` du workspace montre :

```text
refused local port forward: originator 127.0.0.1 port <local> target 127.0.0.1 port <remote>
```

### Cause

Le certificat SSH utilisateur ne contenait pas l'extension OpenSSH
`permit-port-forwarding`. VS Code lance approximativement :

```text
ssh -T -D <socks-port> <host> sh
```

Le canal `direct-tcpip` est refusé par `sshd`, même avec
`AllowTcpForwarding yes`, car l'authentification par certificat exige les
extensions `permit-*`.

### Correctif

Dans `internal/sshca/ca.go`, `CA.Sign` ajoute les permissions standard aux
**extensions** du certificat, pas aux `CriticalOptions` :

```text
permit-agent-forwarding
permit-port-forwarding
permit-pty
permit-user-rc
permit-X11-forwarding
```

Rappel OpenSSH :

- options critiques : `force-command`, `source-address`, `verify-required` ;
- extensions : `permit-*`.

Mettre `permit-*` dans `CriticalOptions` produit :

```text
Certificate critical option "permit-X11-forwarding" is not supported
Invalid certificate options
```

car `sshd` refuse les options critiques inconnues. `golang.org/x/crypto/ssh`'s
`CertChecker` refuse aussi les options critiques inconnues, mais ignore les
extensions inconnues : les `permit-*` doivent donc être des extensions.

### Validation

```bash
# Les permissions doivent apparaître sous "Extensions".
ssh-keygen -Lf ~/.ssh/<key>-cert.pub

# Le principal doit être "dev".
# Tester le forwarding vers un port toujours ouvert, par exemple sshd.
ssh -F <ssh-config> -W 127.0.0.1:22 <host>
# Attendu : SSH-2.0-OpenSSH_...
```

Si `-W 127.0.0.1:22` fonctionne mais `-W 127.0.0.1:<port-vscode>` échoue avec
`connect_to ... failed`, ce n'est pas un problème de permissions : l'ancien VS
Code Server n'écoute plus. Une reconnexion VS Code démarre un nouveau serveur
sur un nouveau port.

### Piège

Le fichier `known_hosts` et le certificat peuvent être mis en cache côté client.
Après un changement de signature, refaire une connexion neuve pour récupérer un
certificat frais. Le `KeyId` peut être un utilisateur ou un token ; le workspace
vérifie surtout le principal `dev` et la CA.

## Problème 7 — Installation et configuration SSH éclatées

### Symptôme

L’installation demandait plusieurs étapes manuelles :

1. installer Headscale avec `mesh.enabled=false` ;
2. créer l’utilisateur et une clé avec `kubectl exec ... headscale ...` ;
3. relancer un second `helm upgrade` avec `mesh.authKey`.

Côté client, l’extension écrivait un bloc `Host poddy-desktop-<workspace>` par
workspace, avec un fichier `authkey`, un répertoire d’état et parfois une clé
SSH par workspace. Le fichier `~/.ssh/config` et `~/.ssh` accumulaient donc des
dizaines d’entrées.

### Correctif

Le chart intègre désormais le bootstrap Headscale et un seul bloc SSH wildcard.

- Headscale managé est activé automatiquement quand `mesh.enabled=true` et que
  `mesh.controlUrl` est vide. `mesh.headscale.enabled` peut encore être forcé.
- `mesh.headscale.host` est déduit de `ingress.hosts[0].host` sous la forme
  `mesh.<host>` s’il n’est pas fourni.
- Un sidecar `mesh-bootstrap` tourne dans le pod Headscale. Il crée l’utilisateur
  `poddy`, une clé workspace réutilisable et une clé desktop éphémère, puis les
  écrit dans le Secret `poddy-mesh`. Il reste vivant après le bootstrap : un
  conteneur de Deployment qui sort est redémarré en boucle.
- Le portail a un init container `mesh-bootstrap-wait` qui attend que les deux
  fichiers de clés soient non vides. Le portail démarre ensuite avec
  `PODDY_MESH_AUTH_KEY_FILE` et `PODDY_MESH_CLIENT_AUTH_KEY_FILE`.
- `secret-mesh.yaml` utilise `lookup` pour conserver les clés déjà générées lors
  des upgrades. Les valeurs `mesh.authKey`/`mesh.clientAuthKey` restent vides
  pour laisser le bootstrap faire le travail.
- L’extension écrit un seul bloc `Host poddy-desktop-*` avec `%n`, une seule clé
  `mesh/authkey`, un seul répertoire `mesh/state` et un seul `mesh/ca.crt`.
  Les tickets portail vont dans `portal/<workspace>.ticket` et
  `poddy-connect` accepte `--ticket-dir`.
- L’extension supprime les anciens blocs `# >>> poddy:<workspace>`, les anciens
  répertoires par workspace et les anciennes clés `poddy_*_ed25519` après avoir
  adopté la plus récente dans `poddy_ed25519`.

### Installation

```bash
helm upgrade --install poddy deploy/helm/poddy -n poddy-system \
  -f values.yaml \
  --set mesh.enabled=true \
  --set mesh.headscale.enabled=true \
  --set mesh.headscale.host=mesh.example.com \
  --set mesh.controlUrl=https://mesh.example.com \
  --wait --timeout 10m

kubectl -n poddy-system get secret poddy-mesh \
  -o jsonpath='{.data.auth-key}' | base64 -d
kubectl -n poddy-system logs deploy/poddy-headscale -c mesh-bootstrap
```

Sans `mesh.controlUrl` ni `mesh.headscale.enabled`, le chart active Headscale
et déduit l’hôte depuis l’ingress. Les clés sont créées automatiquement.

### Piège

- Si `mesh.authKey` ou `mesh.clientAuthKey` est fourni, le bootstrap est
  volontairement désactivé : c’est le mode « contrôleur externe ».
- Le sidecar doit rester vivant après le bootstrap. Le faire sortir avec le
  code 0 provoque un `CrashLoopBackOff` et bloque `helm --wait`.
- Le portail doit attendre les fichiers dans un init container, pas dans le
  conteneur principal : sinon il démarre avec des clés vides et le nœud mesh du
  portail ne rejoint jamais le tailnet.

## Problème 8 — Environnement de lab

### k3s

Le cluster peut ne pas avoir d'unité systemd. Vérifier puis démarrer
manuellement si nécessaire :

```bash
systemctl status k3s 2>&1 || true
sudo nohup /usr/local/bin/k3s server >/tmp/k3s-server.log 2>&1 &
```

### Build de l'image

Podman 3.4.4 peut échouer sur `RUN --mount=type=cache`. Utiliser Docker :

```bash
make PODMAN=docker IMAGE_TAG=dev image
```

Dans le lab, le Makefile exporte l'image et l'importe dans k3s. Redémarrer
ensuite les portails pour qu'ils utilisent la nouvelle image :

```bash
kubectl -n <hub-ns> rollout restart deploy/<hub>
kubectl -n <region-ns> rollout restart deploy/<region>
kubectl -n <hub-ns> rollout status deploy/<hub> --timeout=120s
kubectl -n <region-ns> rollout status deploy/<region> --timeout=120s
```

### Helm

```bash
helm lint deploy/helm/poddy
helm upgrade <release> deploy/helm/poddy \
  --namespace <ns> \
  --reuse-values \
  --set mesh.clientAuthKey='<preauth-key>' \
  --set mesh.clientAuthKeyKey=client-auth-key
```

## Checklist de diagnostic

1. Le pod workspace est `Running` et tous les conteneurs sont `Ready`.
2. L'agent logue `workspace <nom> joined the mesh as <hostname>`.
3. Le nœud workspace est `online` dans Headscale.
4. Le fichier `tailscaled.state` existe sur le PVC et n'est pas vide.
5. Le nom du workspace dans la config SSH est exact (`<workspace>`, pas une
   variante avec une lettre en plus).
6. Le certificat contient `permit-port-forwarding` sous `Extensions` et le
   principal `dev`.
7. `ssh -F <config> <host> 'echo ok'` fonctionne.
8. `ssh -F <config> -W 127.0.0.1:22 <host>` renvoie le banner SSH.
9. Si le forwarding VS Code échoue, vérifier que le port cible écoute encore ;
   une reconnexion VS Code recrée le serveur.
10. En cas de doublons, supprimer les anciens nœuds hors ligne.

## Commandes utiles

```bash
# Pods et logs.
kubectl -n <workspace-ns> get pods -o wide
kubectl -n <workspace-ns> logs <pod> -c poddy-agent --tail=100
kubectl -n <workspace-ns> logs <pod> -c workspace --tail=100

# État tsnet dans le workspace.
kubectl -n <workspace-ns> exec <pod> -c workspace -- \
  ls -l /home/dev/.poddy/tsnet/tailscaled.state

# Nœuds Headscale.
kubectl -n <headscale-ns> exec deploy/<headscale> -- \
  headscale --config /etc/headscale/config.yaml nodes list -o json \
  | jq -r '.[] | [.id,.name,.given_name,.online,(.ip_addresses|join(","))] | @tsv'

# Suppression d'un nœud fantôme.
kubectl -n <headscale-ns> exec deploy/<headscale> -- \
  headscale --config /etc/headscale/config.yaml nodes delete -i <id> --force

# SSH verbeux : le ProxyCommand écrit ses logs sur stderr.
ssh -F <config> -o LogLevel=DEBUG1 <host> 'echo ok'

# Vérifier les permissions du certificat.
ssh-keygen -Lf ~/.ssh/<key>-cert.pub

# Tester le forwarding vers un port toujours ouvert.
ssh -F <config> -W 127.0.0.1:22 <host>
```

## Pièges à éviter

- Ne pas mettre les `permit-*` dans `CriticalOptions`.
- Ne pas partager un répertoire d'état tsnet entre invocations concurrentes du
  ProxyCommand.
- Ne pas utiliser un nœud workspace éphémère.
- Ne pas oublier de monter le PVC `workspace` dans le sidecar agent.
- Ne pas conclure sur `Authkey is set; but state is NoState` sans vérifier
  `joined the mesh` et l'état `online`.
- Ne pas supprimer un nœud Headscale en ligne sans vérifier son IP et son
  workspace.
- Ne pas tester `ssh -W` contre un port VS Code qui n'écoute plus.
- Ne pas committer de preauth key, de token ni de certificat réel.
- Ne pas laisser `PODDY_MESH_DEBUG=1` en production (logs très verbeux).
- Ne pas oublier de redémarrer les portails après un rebuild d'image, et de
  refaire une connexion pour récupérer un certificat frais.

## Fichiers concernés

| Domaine | Fichier |
|---|---|
| DNS IPv4 / NAT64 | `internal/mesh/dns_ipv4.go` |
| Sélection de pair et login forcé | `internal/mesh/mesh.go` |
| Client ProxyCommand | `internal/meshclient/run.go` |
| Sidecar agent et PVC | `internal/k8s/podenv.go` |
| Agent workspace | `cmd/poddy-agent/main.go` |
| Certificats SSH | `internal/sshca/ca.go` |
| Émission des certificats | `internal/server/sshcerts.go` |
| Vérification côté bastion | `internal/bastion/bastion.go` |
| Bootstrap Headscale | `cmd/poddy-mesh-bootstrap/main.go` |
| Helm mesh | `deploy/helm/poddy/values.yaml`, `templates/secret-mesh.yaml`, `templates/deployment.yaml` |
| RBAC bootstrap | `deploy/helm/poddy/templates/rbac-mesh-bootstrap.yaml` |
| Sidecar Headscale | `deploy/helm/poddy/templates/headscale-deployment.yaml` |
| Extension VS Code | `vscode-extension/extension.js` |
| ProxyCommand | `cmd/poddy-connect/main.go` |

## Validation finale

```bash
go test ./...
go vet ./...
helm lint deploy/helm/poddy
```

Validation fonctionnelle effectuée en lab :

- premier démarrage : login automatique, nœud créé ;
- restart du pod : même nœud Headscale et même IP ;
- `ssh <host> 'echo ok'` : succès ;
- `ssh -W 127.0.0.1:22 <host>` : banner SSH, forwarding autorisé ;
- VS Code Remote-SSH : après récupération d'un certificat frais, le tunnel `-D`
  est autorisé ; si le port cible a changé, une reconnexion recrée le VS Code
  Server ;
- installation en une passe : `mesh.enabled=true`, Headscale managé, aucune clé
  fournie ; le sidecar écrit `poddy-mesh`, l’init container du portail attend
  les fichiers, le portail et son nœud mesh démarrent ;
- upgrade suivant : `lookup` conserve les clés du Secret, le sidecar les voit
  déjà présentes et reste inactif ;
- extension : un seul bloc `Host poddy-desktop-*`, un seul `mesh/authkey`, un
  seul `mesh/state`, tickets portail par workspace.
