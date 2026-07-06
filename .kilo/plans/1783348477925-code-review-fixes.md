# Plan — Correctifs qualité du code `webcenter-fr/eino-ext`

> Objectif : appliquer les correctifs identifiés lors de la revue de tout le
> module, pour que le code soit simple, compréhensible, maintenable, conforme au
> `CONTRIBUTING.md` / `AGENTS.md`, sans bug, et bien placé selon la philosophie
> eino. Ce plan est **implémentable tel quel** : toutes les décisions de design
> sont tranchées ci-dessous.
>
> Repos de référence clonés : `/tmp/eino`, `/tmp/eino-ext`, `/tmp/eino-examples`.
> État initial vérifié : `go build ./...` OK, `go vet ./...` OK.

## Décisions de design (tranchées — ne pas rouvrir)

1. **Deux clients OpenSearch = normal et conservé.**
   - `github.com/cloudwego/eino-ext/components/retriever/opensearch3` (et
     `indexer/opensearch3`) = **côté framework eino** (composants
     `Retriever`/`Indexer`).
   - `github.com/disaster37/opensearch/v4` = **client OpenSearch complet**, utilisé
     quand on a besoin d'opérations que le client eino ne couvre pas (scroll,
     delete-by-query, put-mapping, querydsl).
   - **Action** : ne PAS unifier les clients. À la place, **documenter** ce choix
     dans chaque `README` concerné pour qu'un humain comprenne pourquoi les deux
     coexistent. (Annule l'ancien §2.2 « homogénéiser ».)
2. **Validation** : activer réellement la validation via le helper partagé
   existant `libs/toolkit/validate.Struct(cfg)` (pattern déjà utilisé partout :
   `websearch`, `prometheus`, `kubernetes`, `convertor`…). Ne PAS supprimer les
   tags.
3. **`components/indexer/opensearch`** : reste sous `components/indexer/` mais son
   `README` doit clarifier qu'il **complète** l'indexer eino `opensearch3`
   (mappings + reconcile via le client disaster37) et ne le remplace pas. Pas de
   déplacement, pas de faux `Store`.
4. **`retriever/opensearch`** : conservé tel quel (il ajoute `SearchPipeline` +
   `ResultParser` que l'upstream ne fournit pas ; il s'appuie sur le client
   disaster37, cohérent avec la décision 1). Le `README` doit dire pourquoi il
   ne wrappe pas `opensearch3`.
5. **Renommage `Opensearch`→`OpenSearch`, `Github`→`GitHub`** : appliqué aux
   identifiants exportés. Le module a un unique consommateur connu (projet jumeau
   `rancher-doc-chat-api-k8s`, non présent ici) et n'est pas encore publié sur
   ces symboles → rename direct, **sans** alias de compat. Le projet jumeau sera
   recâblé via son propre plan.

---

## Phase 1 — Bugs (P0)

### Tâche 1.1 — `sizecap.mergeOverlap` : overlap cumulatif qui casse `chunkSize`

Fichier : `components/document/transformer/splitter/sizecap/sizecap.go:134-161`.

Bug : `text = overlap + text` puis `prevText = text`. Comme `prevText` contient
déjà l'overlap ajouté au chunk précédent, le préfixe recopié grossit à chaque
itération et un chunk peut dépasser `chunkSize`.

Correctif :
- Introduire une variable qui conserve le **contenu d'origine** du chunk
  précédent (avant préfixage), et calculer l'overlap dessus.
- Concrètement, dans la boucle :
  ```go
  prevOriginal := ""
  for _, chunk := range chunks {
      text := chunk.Content
      if len(prevOriginal) > 0 {
          prevRunes := []rune(prevOriginal)
          overlapStart := len(prevRunes) - s.chunkOverlap
          if overlapStart < 0 {
              overlapStart = 0
          }
          text = string(prevRunes[overlapStart:]) + chunk.Content
      }
      prevOriginal = chunk.Content // contenu AVANT préfixage
      chunk.Content = text
      result = append(result, chunk)
  }
  ```
- Mettre à jour le test : dans `sizecap_test.go`, ajouter à
  `TestTransform_Overlap` (l.91) une assertion **sur tous les chunks** :
  `assert.LessOrEqual(t, utf8.RuneCountInString(chunk.Content), chunkSize+chunkOverlap)`
  au minimum, et idéalement documenter la borne réelle garantie (chunk hard-split
  `<= chunkSize`, plus overlap `<= chunkOverlap`). Vérifier que l'assertion
  d'alignement d'overlap existante (l.107-115) reste vraie.

### Tâche 1.2 — `loader/opensearch` : erreur d'unmarshal ignorée + taille figée

Fichier : `components/document/loader/opensearch/opensearch.go`.

- l.130 `_ = json.Unmarshal(hit.Source, &src)` : ne pas ignorer l'erreur ;
  l'envelopper et la remonter :
  ```go
  if hit.Source != nil {
      if err := json.Unmarshal(hit.Source, &src); err != nil {
          return nil, errors.Wrap(err, "failed to unmarshal hit source")
      }
  }
  ```
- l.111 `"size": 10000` codé en dur : ajouter un champ `Config.PageSize int`
  (tag `validate:"omitempty,gte=1"`, défaut `10000` appliqué dans le
  constructeur), et documenter dans le `README` la limite `index.max_result_window`
  + le fait qu'au-delà il faut affiner la requête `q`. (Pas de scroll ici pour
  garder le loader simple ; le scroll reste dans `indexer/opensearch/reconcile.go`.)

### Tâche 1.3 — Balayage des mêmes classes de bugs sur tout le repo

- `grep -rn "context.Background()" components/ callbacks/ libs/` → remplacer par
  le `ctx` appelant partout où un `ctx` est disponible (hors tests et
  point d'entrée). Corriger au cas par cas.
- `grep -rn "_ = " components/ callbacks/ libs/ | grep -iv "Close()"` → repérer
  les erreurs réseau/JSON silencieusement ignorées hors `defer`. Corriger celles
  qui masquent une vraie erreur (les `_, _ = client...ClearScroll` en `defer` de
  `reconcile.go` sont acceptables : best-effort documenté).
- Vérifier les messages d'erreur hardcodés/faux dans les generics kubernetes
  (`generic_list.go`, `generic_describe.go`, `resource_list.go`) : le nom du tool
  doit être injecté (`toolName`) et non figé sur `Pod...`.

---

## Phase 2 — Validation des `Config` (P1)

Activer `validate.Struct` dans les constructeurs des nouveaux composants RAG,
comme le reste du repo. Import : `github.com/webcenter-fr/eino-ext/libs/toolkit/validate`.

### Tâche 2.1 — `retriever/opensearch`
`retriever.go:59` `NewRetriever` : après le `nil` check, remplacer le
`len(config.URLs)==0` manuel (l.63) par :
```go
if err := validate.Struct(config); err != nil {
    return nil, err
}
```
(`URLs` a déjà `validate:"required,min=1"`.)

### Tâche 2.2 — `loader/opensearch`
`opensearch.go:46` `NewOpensearchLoader` : idem, remplacer le check manuel
`len(config.URLs)==0` par `validate.Struct(config)`. Appliquer aussi le défaut
`PageSize` (Tâche 1.2) après validation.

### Tâche 2.3 — `parser/opensearch`
`opensearch.go:56` `NewParser` : ajouter `validate.Struct(conf)` après les
défauts `SourceIDField`/`SourceHashField`. (Tags actuels tous `omitempty` → sans
effet aujourd'hui, mais rend le contrat cohérent et prêt pour de futures
contraintes.)

### Tâche 2.4 — `sizecap`
`sizecap.go:34` `NewSplitter` : le tag `ChunkSize validate:"required,gte=1"` est
**incohérent** avec le défaut appliqué (`config.ChunkSize <= 0` → 1000). Choix :
retirer `required` du tag (`validate:"omitempty,gte=1"`) car un `0` est
légitimement remplacé par le défaut, PUIS appeler `validate.Struct(config)`
**après** application des défauts (l.38-46), de sorte que la validation porte sur
les valeurs finales. Cohérence : `ChunkOverlap` garde `omitempty,gte=0`.

Après cette phase : `go test ./components/document/... ./components/retriever/...`
doit passer.

---

## Phase 3 — Placement & documentation philosophie eino (P1)

### Tâche 3.1 — README `indexer/opensearch`
`components/indexer/opensearch/README.md` : ajouter une section « Pourquoi ce
package » expliquant :
- qu'il **complète** l'indexer eino `opensearch3` (qui fait le `Store`),
- qu'il fournit les opérations non couvertes par le client eino : `EnsureMappings`
  (put-mapping idempotent), `Reconcile`/`LookupSourceHash`/`DeleteBySourceIDs`
  (via le client complet `disaster37/opensearch/v4`),
- qu'il n'implémente **pas** `indexer.Indexer` (ce sont des helpers de
  maintenance d'index), donc à utiliser en complément de `opensearch3.NewIndexer`.

### Tâche 3.2 — README `retriever/opensearch`
Documenter : s'appuie sur le client complet `disaster37/opensearch/v4` (pas
`opensearch3`) pour supporter `SearchPipeline` et un `ResultParser`
personnalisable ; `defaultResultParser` lit le champ `content` par défaut
(pointer vers `ResultParser` pour un autre champ).

### Tâche 3.3 — README `loader/opensearch` et `loader/github`
- `loader/opensearch` : documenter le format d'URI
  `opensearch://index?q=...`, le défaut `q=*`, `PageSize`, et le client utilisé.
- `loader/github` : documenter le comportement token vide → loader URL nu.

### Tâche 3.4 — Note transverse « deux clients OpenSearch »
Ajouter un court paragraphe dans le README de chaque package OpenSearch
(retriever, loader, indexer, `components/memory/opensearch`, `components/tool/opensearch`)
renvoyant à la règle : **eino `opensearch3` pour les composants, `disaster37/opensearch/v4`
pour les opérations client complètes**. (Une seule formulation copiée, courte.)

### Tâche 3.5 — Confirmer le placement des autres packages
Balayage de conformité (constat, correction seulement si écart) :
- `components/middleware/*` = `adk.ChatModelAgentMiddleware` uniquement :
  `grep -rn "ChatModelAgentMiddleware" components/middleware`.
- `components/model/*` = décorateurs `model.*` : vérifier les `var _ model.X`.
- `callbacks/activity` = `callbacks.Handler` : vérifier `var _ callbacks.Handler`.
- `libs/*` ne dépend d'aucune abstraction composant eino de façon fuyante.
  Documenter tout écart trouvé (ne rien déplacer sans nouvel accord).

---

## Phase 4 — Nommage & conventions Go (P1)

### Tâche 4.1 — `Opensearch` → `OpenSearch`
- `components/document/loader/opensearch/opensearch.go` :
  `loaderType = "OpensearchLoader"` → `"OpenSearchLoader"` ;
  `func NewOpensearchLoader` → `func NewOpenSearchLoader` (l.19, l.46, l.169
  commentaires inclus).
- Rechercher tous les appelants internes : `grep -rn "NewOpensearchLoader" .`
  (hors `/tmp`) et mettre à jour. Le `GetType()` d'un composant peut être une
  valeur observée par des tests/consommateurs : garder la cohérence et adapter
  les tests éventuels.

### Tâche 4.2 — `Github` → `GitHub`
- `components/document/loader/github/github.go:16` :
  `func NewGithubLoader` → `func NewGitHubLoader`. Le package reste `github`
  (dernier segment du chemin, conforme). Mettre à jour les appelants internes.

### Tâche 4.3 — `fmt.Sprintf` vs concaténation
`grep -rn '" + \|+ "' components/ libs/ callbacks/` → convertir les
concaténations de chaînes lisibles en `fmt.Sprintf` (CONTRIBUTING §String
formatting). Corriger au cas par cas ; ne pas toucher aux concaténations triviales
sans gain de lisibilité.

### Tâche 4.4 — Bandeaux de licence
`grep -rln "Copyright .* CloudWeGo Authors" components/ libs/ callbacks/` → doit
être vide. Supprimer tout bandeau réintroduit (règle CONTRIBUTING §License).

---

## Phase 5 — Tests manquants (P1)

Ajouter des tests **table-driven** avec mocks (CONTRIBUTING §Testing) pour les
packages RAG non couverts. Pour les clients réseau, introduire une petite
interface locale sur les méthodes réellement utilisées afin de tester sans
réseau (ne pas dépendre d'un OpenSearch réel).

| Package | Tests |
|---|---|
| `components/document/parser/opensearch` | `Parse` : JSON `_search` → N docs ; `FieldSelectors` ; `FieldIgnores` exclut de la meta ; défauts `source_id`/`source_hash` posés ; hit sans `_source` → ignoré ; contenu vide → skip. `ConvertHit` en direct. |
| `components/document/loader/opensearch` | `uriToIndexAndQuery` (table : `opensearch://idx?q=foo`, sans `q`, URI nue, URI invalide) ; `NewOpenSearchLoader` défaut `PageSize` ; erreur unmarshal remontée (Tâche 1.2). |
| `components/document/loader/github` | token vide → loader nu ; token présent → header `Authorization: Bearer <token>` posé (invoquer le `RequestBuilder`). |
| `components/retriever/opensearch` | `searchHitToMap` (ajout `_id/_index/_score/_version`) ; `defaultResultParser` (content + meta) ; erreur si `Index` absent (retriever.go:113) ; `ResultParser` custom respecté. |
| `components/indexer/opensearch` | `reconcileQuery` (filter nil → match_all ; filter présent → bool/term) ; batching `DeleteBySourceIDs` (bornes `reconcileDeleteBatchSize`) et `Reconcile` avec client mocké ; `EnsureMappings` no-op sur 404. |

`libs/docid` : déjà couvert (`docid_test.go`), rien à faire.

---

## Phase 6 — Compréhensibilité & maintenabilité (P2)

### Tâche 6.1 — Commentaires de package
Ajouter un commentaire `// Package xxx ...` en tête des 6 nouveaux packages RAG
qui n'en ont pas (`libs/docid` sert de modèle). Un par fichier principal.

### Tâche 6.2 — Constantes magiques & timeouts
- Nommer/documenter `size:10000` (traité en 1.2), `30*time.Second`
  (`retriever.go:117`, `loader/opensearch.go:95`), `reconcileScrollBatchSize`, etc.
- Timeouts codés en dur : `context.WithTimeout(ctx, 30s)` **raccourcit** un
  deadline appelant plus long. Décision : rendre le timeout configurable via un
  champ `Config.Timeout time.Duration` (défaut 30s, `0` = pas de timeout imposé,
  on respecte le `ctx` appelant). Documenter dans le README.

### Tâche 6.3 — Doc `sizecap.splitContent`
Documenter que le chemin « contenu court » (`sizecap.go:79`) renvoie le document
source **tel quel** (pas de copie de meta) et qu'aucune mutation aval n'est
attendue sur ce chemin.

---

## Ordre d'exécution

1. **Phase 1** (bugs) — sans changement d'API : 1.1, 1.2, 1.3.
2. **Phase 5** (tests) — verrouille le comportement avant les refactors d'API.
3. **Phase 2** (validation) — dépend du helper `validate` partagé.
4. **Phase 4** (renommage) — changement d'API public, après tests en place.
5. **Phase 3** + **Phase 6** (docs/README/constantes/placement) — polish.

Chaque phase est livrable et testable indépendamment. Après le renommage
(Phase 4), relancer toute la suite pour capter les appelants cassés.

## Validation finale (obligatoire — CONTRIBUTING §Validation)

```bash
go build ./...
go vet ./...
go test ./...
```

Aucune régression tolérée. Ne committer/pusher que sur demande explicite de
l'utilisateur.

## Notes de coordination

- Le renommage (Phase 4) impacte l'API publique consommée par le projet jumeau
  `rancher-doc-chat-api-k8s` (non présent dans ce workspace) : son recâblage
  (`NewOpenSearchLoader`, `NewGitHubLoader`) devra être fait dans son propre plan.
- Aucun déplacement de package n'est prévu (décisions 1 & 3) : uniquement des
  correctifs, de la validation, du renommage local et de la documentation.
