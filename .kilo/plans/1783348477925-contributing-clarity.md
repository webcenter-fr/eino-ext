# Plan — Clarifier `CONTRIBUTING.md` pour éviter les violations (constatées sur les features RAG)

> Contexte : les derniers composants (famille RAG : `document/*`, `retriever/opensearch`,
> `indexer/opensearch`) ont été implémentés par une LLM **mais ne respectent pas**
> `CONTRIBUTING.md` (validation jamais appelée, tests manquants, nommage
> `Opensearch`/`Github`). Diagnostic : ce n'est pas une négligence isolée, c'est
> que le CONTRIBUTING est **ambigu / mal scopé** sur ces points. Ce plan corrige le
> CONTRIBUTING (et AGENTS.md) pour le rendre non-ambigu et exécutable par une LLM,
> puis ajoute un minimum d'enforcement automatique.
>
> **Périmètre** : édition de `CONTRIBUTING.md` et `AGENTS.md` + ajout d'une checklist
> et de cibles `Makefile`/CI. **Aucune modification de code source** dans ce plan
> (les correctifs du code RAG sont dans `1783348477925-code-review-fixes.md`).
>
> ⚠️ Ce plan édite des fichiers `.md` non-plan (`CONTRIBUTING.md`, `AGENTS.md`) et
> potentiellement `Makefile`/workflow CI : **à exécuter par un agent
> d'implémentation**, pas par l'agent de planification.

---

## 1. Causes racines identifiées (preuves)

Chaque lacune ci-dessous explique une violation réelle observée.

| # | Lacune du CONTRIBUTING | Preuve | Conséquence observée |
|---|---|---|---|
| C1 | **Les règles fortes sont scopées « tools ».** La section « Tool Design Principles » (l.124-231 : validation, nommage, tests, mocks, sécurité, usabilité) débute par *« When creating new tools (components under `components/tool/`) »*. | `CONTRIBUTING.md:126` | Une LLM en déduit que ces règles **ne s'appliquent pas** aux composants `document`/`retriever`/`indexer`. |
| C2 | **La validation est décrite mais jamais « activée ».** *« un `Config` avec tags `validate`/`jsonschema` … et `validator/v10` pour la validation »* décrit les **tags**, pas l'**appel** dans le constructeur. | `CONTRIBUTING.md:90-92` | Tags posés, `validate.Struct(cfg)` **jamais appelé** dans les 4 constructeurs RAG. |
| C3 | **Le helper partagé n'est jamais cité.** `libs/toolkit/validate.Struct` existe et est utilisé partout (`websearch`, `prometheus`, `kubernetes`…), mais aucun doc n'y renvoie. | `grep`: seul `validator/v10` est cité | La LLM ré-vérifie à la main (ou pas du tout) au lieu d'utiliser le helper. |
| C4 | **Nommage des marques/acronymes composés non couvert.** Exemples donnés : `ToJSON`, `ID`, `URL` seulement. | `CONTRIBUTING.md:174`, `AGENTS.md:11` | `NewOpensearchLoader`, `NewGithubLoader` au lieu de `OpenSearch`/`GitHub`. |
| C5 | **Le CONTRIBUTING se contredit sur le nommage.** L.174 dit `URL` not `Url`, mais l.210 écrit `argocd.Config{Url: ...}`. | `CONTRIBUTING.md:174` vs `:210` | Signal contradictoire → la règle de nommage perd toute autorité. |
| C6 | **« Tests + README par composant » sans enforcement.** Règle présente mais non vérifiée mécaniquement. | `CONTRIBUTING.md:93` | 6/7 packages RAG livrés **sans test**. |
| C7 | **Aucune application automatique.** Pas de `.golangci.yml`, pas de CI ; seulement un `Makefile`. | `ls .github/workflows` vide, `Makefile` présent | Rien n'attrape « tags sans validation », « package sans test », « README manquant ». |
| C8 | **La règle des deux clients OpenSearch n'est pas écrite.** (eino `opensearch3` pour les composants ; `disaster37/opensearch/v4` pour le client complet.) | absente du CONTRIBUTING | Ambiguïté sur quel client utiliser ; personne ne peut vérifier le choix. |

---

## 2. Correctifs du CONTRIBUTING (tâches d'édition)

### Tâche 2.1 — Déscoper les règles transverses hors de « tools » (corrige C1)
- Renommer la section `## Tool Design Principles` en **`## Component Design Principles`**
  et remplacer l'ouverture *« When creating new tools (components under `components/tool/`) »*
  par : *« These principles apply to **every** component (`agent`, `document`,
  `embedding`, `indexer`, `model`, `prompt`, `retriever`, `tool`), aux middlewares
  ADK, aux callbacks et aux libs. Les sous-sections marquées **(tools only)**
  ne concernent que `components/tool/`. »*
- Marquer explicitement **(tools only)** les sous-sections réellement spécifiques
  aux tools : « Command blocklists » et l'exemple `NewAllTools` de factory. Tout le
  reste (validation, nommage, tests, error handling, usability) devient **transverse**.

### Tâche 2.2 — Rendre la validation impérative et pointer le helper (corrige C2, C3)
Dans la section `## Components` (l.88-93), remplacer la puce vague par une règle
**avec exemple de code obligatoire** :
```md
- Chaque `New...` constructeur DOIT valider sa config en appelant le helper
  partagé `github.com/webcenter-fr/eino-ext/libs/toolkit/validate` :

      import "github.com/webcenter-fr/eino-ext/libs/toolkit/validate"

      func NewXxx(ctx context.Context, cfg *Config) (*Xxx, error) {
          if cfg == nil {
              cfg = &Config{}
          }
          // ... application des valeurs par défaut ...
          if err := validate.Struct(cfg); err != nil {
              return nil, err   // déjà enveloppé par le helper
          }
          ...
      }

- N'utilisez PAS `validator.New()` directement ni des checks manuels ad hoc
  (`if len(cfg.URLs) == 0`) : posez la contrainte dans le tag `validate:"..."`
  et laissez `validate.Struct` la vérifier.
- Appelez `validate.Struct` APRÈS avoir appliqué les valeurs par défaut, pour
  que la validation porte sur les valeurs finales. N'utilisez pas `required`
  sur un champ qui reçoit un défaut (utilisez `omitempty,gte=1`).
```

### Tâche 2.3 — Règle de nommage des marques/acronymes + corriger la contradiction (corrige C4, C5)
- Compléter la liste `CONTRIBUTING.md:174` et `AGENTS.md:11` :
  *« Follow Go naming conventions: `ToJSON` not `ToJson`, `ID` not `Id`, `URL`
  not `Url`, **`OpenSearch` not `Opensearch`, `GitHub` not `Github`, `GitLab`,
  `PostgreSQL`, `gRPC`, `API`, `HTTP`**. En cas de doute, respecter la graphie
  officielle du produit dans les identifiants exportés et les valeurs `GetType()`. »*
- **Corriger la contradiction** l.208-211 : `argocd.Config{Url: ...}` →
  `argocd.Config{URL: ...}` (aligner l'exemple sur sa propre règle).

### Tâche 2.4 — Rendre « tests + README » vérifiables (corrige C6)
Dans `## Components`, remplacer la puce par une **Definition of Done** explicite :
```md
- Un composant n'est considéré complet que si TOUS ces artefacts existent :
  1. `xxx.go` avec `Config` (tags `validate`+`jsonschema`), `New...`, et un
     `var _ <abstraction>.<Interface> = (*Xxx)(nil)` de contrôle à la compilation.
  2. `xxx_test.go` : tests table-driven couvrant le cas nominal, les erreurs de
     paramètres, et (avec mocks) les dépendances externes. Pas de dépendance à
     un service réel.
  3. `README.md` : rôle du composant, snippet du constructeur, et note de
     placement (quelle abstraction eino il implémente).
  4. Commentaire de package `// Package xxx ...` en tête.
```

### Tâche 2.5 — Documenter la règle des deux clients OpenSearch (corrige C8)
Ajouter, dans `## Project structure` ou une nouvelle sous-section
`### OpenSearch clients`, une règle claire :
```md
- Deux bibliothèques OpenSearch coexistent volontairement :
  - `github.com/cloudwego/eino-ext/components/{retriever,indexer}/opensearch3`
    pour les COMPOSANTS eino (`Retriever`/`Indexer`).
  - `github.com/disaster37/opensearch/v4` quand vous avez besoin du CLIENT
    OpenSearch complet (scroll, delete-by-query, put-mapping, querydsl) que le
    client eino ne couvre pas.
  Ne les unifiez pas. Chaque README de package OpenSearch doit rappeler lequel
  il utilise et pourquoi.
```

### Tâche 2.6 — Ajouter une checklist de PR « anti-oubli » (corrige C1-C8, résumé actionnable)
En fin de `CONTRIBUTING.md`, ajouter une section `## Checklist avant PR` que
même une LLM peut cocher point par point :
```md
## Checklist avant PR
- [ ] `go build ./...`, `go vet ./...`, `go test ./...` passent.
- [ ] Chaque nouveau `Config` a des tags `validate`+`jsonschema` ET son
      `New...` appelle `validate.Struct(cfg)` après les défauts.
- [ ] Chaque nouveau composant a : test table-driven, README, commentaire de
      package, et un `var _ Interface = (*T)(nil)`.
- [ ] Nommage : acronymes/marques respectent la graphie officielle
      (`OpenSearch`, `GitHub`, `URL`, `ID`, `JSON`).
- [ ] Erreurs enveloppées avec `emperror.dev/errors` (contexte de l'opération).
- [ ] Aucun bandeau de licence ajouté.
- [ ] Le composant est placé selon son abstraction eino (voir Project structure).
- [ ] Pas de duplication d'un helper déjà présent dans `libs/toolkit/`.
```

### Tâche 2.7 — Aligner `AGENTS.md` (corrige C4, cohérence)
`AGENTS.md` est le fichier lu en priorité par l'agent. Y ajouter/renforcer :
- la même liste d'acronymes/marques que 2.3 ;
- une puce « Common Pitfalls » : *« Tags `validate` posés mais `validate.Struct`
  jamais appelé »* et *« composant livré sans test/README »* — ce sont les deux
  pièges réellement tombés.

---

## 3. Enforcement automatique (corrige C7) — minimiser les futures dérives

L'objectif : ce qui peut être vérifié par une machine ne doit pas reposer sur la
vigilance d'un relecteur (humain ou LLM).

### Tâche 3.1 — `golangci-lint`
Ajouter un `.golangci.yml` activant au minimum : `govet`, `errcheck`
(attrape les `_ = json.Unmarshal` du loader), `staticcheck`, `revive`
(commentaires de package / nommage), `misspell`. Ajouter une cible `make lint`.

### Tâche 3.2 — Script de conformité composant
Ajouter un petit script (`scripts/check_components.sh` ou cible `make check-components`)
qui, pour chaque dossier sous `components/*/*` contenant un `.go` non-test,
vérifie la présence d'un `_test.go` ET d'un `README.md`, et échoue sinon
(liste les manquants). C'est exactement le contrôle qui aurait attrapé les 6
packages RAG sans test. **Question ouverte (§5)** : bloquant ou warning au début ?

### Tâche 3.3 — CI GitHub Actions
Ajouter `.github/workflows/ci.yml` exécutant `make lint`, `go vet ./...`,
`go test ./...`, et `make check-components` sur PR. Sans CI, aucune règle n'est
réellement garantie.

---

## 4. Ordre d'exécution

1. **Tâches 2.1 → 2.7** (édition docs) — sans risque, immédiat, corrige la cause
   racine « ambiguïté ».
2. **Tâche 3.1** (`.golangci.yml` + `make lint`) — révèle la dette existante ;
   corriger le bruit ou configurer les exclusions au besoin.
3. **Tâche 3.2** (`check-components`) puis **3.3** (CI).

Validation : après édition, relire le CONTRIBUTING en se demandant pour chaque
violation RAG connue « la règle est-elle maintenant impérative, exemplifiée, et
vérifiable ? ». Lancer `make lint` / la CI localement.

---

## 5. Questions ouvertes

1. **`check-components` bloquant ou warning ?** Recommandation : **warning**
   pendant une phase de transition (le repo a déjà des packages sans test :
   `libs/pricer`, `libs/summarizer`, `libs/counter`…), puis **bloquant** une fois
   la dette résorbée. À confirmer.
2. **Périmètre `libs/` dans la Definition of Done** : imposer test+README à
   toutes les `libs/` ou seulement aux composants ? Recommandation : test
   obligatoire pour `libs/`, README recommandé (pas bloquant).
3. **Faut-il aussi corriger le code RAG dans la foulée ?** Non : c'est l'objet du
   plan `1783348477925-code-review-fixes.md`. Ce plan-ci ne touche que docs +
   enforcement.

---

## Notes

- Ce plan ne modifie **aucun code source**. Les correctifs du code RAG
  (validation, renommage, tests, bugs) restent dans
  `.kilo/plans/1783348477925-code-review-fixes.md`.
- Enchaînement recommandé : appliquer d'abord ce plan (docs + CI) pour que le plan
  de correctifs code soit vérifié par l'enforcement nouvellement en place.
