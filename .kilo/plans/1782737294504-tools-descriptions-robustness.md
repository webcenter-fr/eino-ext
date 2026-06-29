# Plan : Amélioration des tools (descriptions + robustesse)

## Objectif
Réduire l'explosion du context window et les échecs d'agents lors de l'usage des tools
`components/tool/kubernetes/*` et `components/tool/opensearch/*`, en améliorant (1) la
documentation exposée au modèle (descriptions de tool + jsonschema) et (2) la robustesse,
**sans modifier les signatures publiques des tools**.

## Cause racine identifiée
- Les descriptions niveau-tool ne documentent que l'output, jamais les paramètres de
  filtrage (`namespace`, `labelsSelector`, `filter`, `paginate`) → le modèle appelle large.
- Défaut de pagination trop élevé (500) et `paginateToken` non documenté.
- Panics sur regex invalide → échecs non récupérables au lieu d'erreurs.

## Périmètre
Descriptions + robustesse. **Hors périmètre** : objet de sortie paginé explicite
`{items, paginateToken}`, sélection positive/jsonpath sur describe (refonte ultérieure).

---

## Tâches

### A. Robustesse

1. **Supprimer les panics regex** (6 emplacements). Créer un helper partagé dans
   `components/tool/kubernetes/helper.go` :
   ```go
   func CompileFilter(pattern string) (*regexp.Regexp, error) {
       if pattern == "" { return nil, nil }
       re, err := regexp.Compile(pattern)
       if err != nil { return nil, errors.Wrapf(err, "invalid regex filter %q (Go RE2 syntax)", pattern) }
       return re, nil
   }
   ```
   - `generic_list.go:55` (`IsMatch`) : compiler en amont dans `Invoke`, propager l'erreur.
   - `resource_list.go:68` (`IsMatch`) : idem.
   - `pod_log.go:75` (`Invoke`) et `pod_log.go:121` (`InvokeAsStream`) : utiliser `CompileFilter`,
     retourner l'erreur ; gérer le cas `nil` (= match tout).
   - `pod_exec.go:85` et `pod_exec.go:144` : idem.
   - Note opensearch : pas de regex, rien à faire côté panic.

2. **Baisser le PageSize par défaut** de 500 → 50.
   - `generic_list.go:68` : `params.Paginate.PageSize = 50`.
   - Aligner la description du champ `ListParamsPaginate.PageSize` (`generic_list.go:36`)
     sur "Default is 50".

3. **Opensearch : cohérence Invoke / InvokeAsStream** (`opensearch_log_kubernetes.go`).
   - Dans `InvokeAsStream` (~ligne 123), appliquer les mêmes défauts que `Invoke` :
     `from="now-24h"`, `to="now"`, `maxLines=100` (factoriser dans un `applyDefaults`).
   - Dans le constructeur `NewOpensearchLogKubernetesTool` (~ligne 207), câbler le mode
     streaming via `utils.InferStreamTool(...)` et assigner `tool.StreamableTool`
     (sur le modèle de `pod_log.go:167`).

### B. Descriptions exposées au modèle

4. **Bloc de guidage commun "limiter l'output"**, centralisé dans les constructeurs
   génériques pour couvrir les 24 list + 24 describe sans éditer chaque fichier.
   - Définir une constante partagée, ex. dans `helper.go` :
     ```
     ** How to limit output (IMPORTANT) **
     Always narrow the query to avoid large responses:
     - Set `namespace` whenever you know it.
     - Use `labelsSelector` (e.g. 'app=nginx,env=prod') to target resources.
     - Use `filter` (Go RE2 regex, applied on each resource JSON) to keep only matches.
     - Use `paginate.pageSize` (default 50) and the returned `paginateToken` to page
       through large result sets instead of requesting everything at once.
     ```
   - Concaténer ce bloc à `toolsDescription` dans `NewListTool` (`generic_list.go:144`),
     `NewResourceListTool` (`resource_list.go`), et un bloc équivalent (sans pagination,
     mais avec `excludeFieldsOutput`) dans `NewDescribeTool` (`generic_describe.go:118`).
   - Documenter que le `paginateToken` est renvoyé comme dernier élément de la liste.

5. **Préciser la syntaxe RE2 + exemple** dans les jsonschema des champs filtre :
   - `generic_list.go:31` (`Filter`), `resource_list.go` (`Filter`),
     `pod_log.go:36` (`FilterPattern`), `pod_exec.go` (`FilterPattern`).
   - Ex : "A Go RE2 regex applied on each line/JSON. Example: 'error|panic'. Invalid
     regex returns an error."

6. **Corriger la faute "sepaeted" → "separated"** : `generic_list.go:30` et `resource_list.go:45`.

7. **Cohérence jsonschema opensearch** : ajouter `omitempty` au json tag des champs
   optionnels `podName`, `containerName`, `from`, `to`, `luceneQuery`
   (`opensearch_log_kubernetes.go:37-41`).

---

## Validation
- `go build ./...`
- `go test ./components/tool/...`
- Test manuel/unitaire : une regex invalide passée à `filter`/`filterPattern` renvoie une
  erreur wrappée (plus aucun panic).
- Vérifier que le mode stream opensearch est bien exposé et applique les défauts `from`/`to`.

## Risques / points d'attention
- Concaténation de description : s'assurer qu'aucun tool ne dépend d'une description exacte
  (tests). Le changement est additif.
- Baisse du PageSize à 50 : comportement par défaut différent pour les appelants ne
  fournissant pas `paginate`. Acceptable (documenté), non breaking au niveau API.
- Garder le helper regex dans le package `kubernetes` ; opensearch n'en a pas besoin.

## Hors périmètre (suivi éventuel)
- Sortie paginée structurée `{items: [...], paginateToken: "..."}`.
- Sélection positive de champs / jsonpath sur le describe pour les gros CRD.
