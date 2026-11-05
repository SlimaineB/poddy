# TODO

Idées notées pour plus tard.

## Multi-cluster / multi-region

- [ ] Une seule UI pour plusieurs clusters Kubernetes / régions.
- [ ] Plusieurs provisioners (un par cluster) et plusieurs SSH bastions.
- [ ] Registre de clusters : kubeconfig/credentials, namespace, URL du bastion, région.
- [ ] Choisir le cluster à la création d'un workspace, ou placement automatique.
- [ ] Gérer RBAC, état des workspaces, audit et suppressions à travers les clusters.
- [ ] Prévoir le routage terminal/VS Code/SSH vers le bon bastion.
- [ ] Documenter le comportement en cas de cluster indisponible (failover, dégradation).

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

## Fiabilité SSH

- [ ] Rendre le process sshd plus fiable pendant les pics mémoire.
- [ ] L'isoler dans un cgroup dédié ou un sidecar.
- [ ] Le protéger de l'OOM killer (limites/réservations, priorité).
- [ ] Éviter les déconnexions des sessions actives.
