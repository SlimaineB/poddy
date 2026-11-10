# TODO

Idées notées pour plus tard.

## Multi-cluster / multi-region

- [x] Une seule UI pour plusieurs clusters Kubernetes / régions.
- [x] Plusieurs provisioners (un par cluster) et plusieurs SSH bastions.
- [x] Registre de clusters : URL du region manager, clé partagée, URL du bastion, région.
- [x] Choisir la région à la création d'un workspace.
- [ ] Placement automatique selon la charge ou la capacité.
- [x] Gérer RBAC, état des workspaces, audit et suppressions à travers les clusters.
- [x] Prévoir le routage terminal/VS Code/SSH vers le bon bastion.
- [x] Documenter le comportement en cas de cluster indisponible (dégradation partielle, pas de failover automatique).

## Authentification

- [x] Ajouter un exemple complet Okta pour l'OIDC (issuer, scopes, groupes, redirect URL).

## Templates

- [ ] Simplifier et uniformiser les templates.
- [ ] Cacher les variables d'environnement communes.
- [ ] Mutualiser les fragments communs (base, env, volumes, sécurité).
- [ ] Intégrer les mutation policies et éviter la duplication entre templates.

## Observabilité

- [ ] Afficher les métriques CPU / RAM / disque par workspace.
- [ ] Historique et éventuellement alertes sur les ressources.
- [ ] Étudier metrics-server et/ou Prometheus comme source.

## Cycle de vie des workspaces

- [ ] Détecter le temps d'inactivité d'un workspace (terminal, VS Code, SSH, accès aux ports) et l'afficher dans l'UI.

## Fiabilité SSH

- [ ] Rendre le process sshd plus fiable pendant les pics mémoire.
- [ ] L'isoler dans un cgroup dédié ou un sidecar.
- [ ] Le protéger de l'OOM killer (limites/réservations, priorité).
- [ ] Éviter les déconnexions des sessions actives.

## Port forwarding

- [x] Détecter automatiquement les ports en écoute dans le workspace et les remonter au portail (port, protocole), avec déclaration manuelle en secours.
- [x] Ajouter une section "Ports" par workspace : liste, état, URL et bouton "Ouvrir dans le navigateur".
- [x] Ouvrir les ports dans une fenêtre dédiée depuis la carte du workspace, tout en gardant la section dans le détail.
- [x] Proxyfier les apps HTTP avec authentification devant chaque port et contrôle RBAC du workspace.
- [x] Router les ports via le hub central vers la région propriétaire du workspace.
- [ ] Gérer le partage : propriétaire, utilisateurs authentifiés ou public, avec audit et révocation immédiate.
- [ ] Fournir une URL stable par workspace/port (sous-domaine `port--workspace--user` avec wildcard DNS/TLS, ou path proxy).
- [ ] Supporter le TCP brut (base de données, serveur de dev) via tunnel SSH `-L` ou une CLI dédiée.
- [ ] Gérer les applications qui génèrent des URLs absolues (les WebSockets et le HMR fonctionnent avec des URLs relatives).
- [x] Fermer automatiquement les forwards à l'arrêt/suppression du workspace.
- [ ] Fermer les forwards après inactivité.
- [x] Documenter les limites (binding sur `127.0.0.1`, URLs absolues, sécurité) et les alternatives SSH.

## Réduction des coûts

- [ ] Mise en veille par inactivité : détecter l'absence d'activité SSH (frappe de touche), de trafic réseau ou d'utilisation CPU pendant 30 minutes, puis arrêter automatiquement le Pod en conservant le PVC intact.
- [ ] Étudier une détection d'inactivité via eBPF et/ou les sessions SSH, avec seuils configurables par template ou par workspace.
- [ ] Wake-on-SSH / Wake-on-Web : intercepter une connexion SSH ou un clic VS Code alors que le workspace est arrêté, redémarrer le Pod en arrière-plan via une Gateway/Proxy en Go, puis ouvrir la session sans passer par le portail.
- [ ] Calculateur de coût en temps réel : afficher sur le tableau de bord utilisateur/admin le coût du workspace à partir de sa quote-part CPU/RAM et du prix cloud configuré, pour encourager l'extinction des ressources.

## Collaboration & partage

- [ ] Workspace pair-programming : inviter un collègue via son identité OIDC dans un workspace existant, en lecture seule ou en écriture, en mappant ses clés SSH vers le même Pod via le bastion.
- [ ] Snapshot & partage de workspace : créer un instantané à chaud du PVC, générer un token/lien unique, puis recréer un workspace identique à partir du snapshot pour reproduire un bug.
- [ ] Définir les règles RBAC, l'audit et la révocation immédiate pour les accès collaboratifs et les liens de snapshot.

## Expérience développeur

- [ ] À compléter : la demande initiale était tronquée après « Expérience Développeur (D… ».
