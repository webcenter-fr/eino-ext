# Plan — Backport headroom / lean-ctx vers eino-ext (middlewares d'optimisation de contexte)

> Module : `github.com/webcenter-fr/eino-ext`. Objectif : porter, comme middlewares
> **out-of-the-box** et déterministes (sans LLM, sans état, sans dépendance lourde),
> les optimisations de contexte de **headroom** et **lean-ctx** qui ne sont pas déjà
> couvertes par eino natif ou par `components/middleware/contextopt`.
> Contrainte : déterminisme + sûreté du prompt-cache (byte-stable hors zones compressées),
> aucune dépendance ML/torch, aucune mutation provider-specific.

## Contexte / état des lieux (ce qui existe déjà — ne pas réinventer)

**Eino natif `v0.8.5`** (`adk/middlewares`) :
- `summarization` — résumé ancré intra-run (TokenCounter, trigger MaxTokens, preserve user msgs, Finalize/Callback).
- `reduction` + `filesystem/large_tool_result` — offload des gros tool-outputs vers un `Backend` puis "clear" → **réversibilité (CCR) déjà native** via store + retrieval.
- `skill` / `plantask` / `dynamictool/toolsearch` — JIT disclosure d'outils/skills.

**Eino-ext `components/middleware/contextopt`** :
- trim avant dernier résumé, prune de tool-outputs périmés (idempotent via `PruneMarkerKey`),
  compaction ancrée (port kilocode), tail préservé, `Summarizer` injectable.
- `components/memory` + `memory/session` (planifié) : persistance, marqueurs de résumé partagés, `TokenCounter`.

**Défaut identifié dans l'existant** : `contextopt.pruneToolOutputs` est **destructif**
(`truncateToolOutput` coupe à `ToolOutputMaxChars` sans stocker l'original) → perte non
réversible. À corriger (Brique C).

## Décisions d'architecture (validées)

- **Pas un middleware par micro-optimisation, ni tout dans `contextopt`.** Regroupement par
  **surface + cycle de vie** :
  1. Opérations qui ont besoin de toute la liste de messages + budget tokens → **étendre `contextopt`**.
  2. Compression **par message** (pure, réutilisable standalone) → **nouveau package `contentcomp`**.
  3. Normalisation des **définitions d'outils** (autre surface que `state.Messages`) → **nouveau middleware `cachestab`**.
  4. Diagnostics non-mutants + steering léger → **observer/option**.
- **Déterminisme obligatoire** : sortie = fonction pure de l'entrée (pas d'échantillonnage
  statistique), pour préserver les remises prompt-cache Anthropic (90 %) / OpenAI (50 %).
- **Réversibilité** : toute troncature/suppression passe par un `Backend` (réutiliser
  `adk/filesystem.Backend` ou définir une interface minimale `contentcomp.Store`) laissant un
  handle content-addressé.

## Périmètre retenu

### Brique A — `components/contentcomp/jsoncrush` (package pur)
Port **déterministe** du `json_crush` de lean-ctx (≠ SmartCrusher statistique de headroom).
- API : `func Crush(content string, opts ...Option) (out string, refs []Ref, err error)` ;
  détecte un array d'objets JSON ; hoist des clés communes à toutes les lignes vers un bloc
  `_defaults`, ne garde que les déviations par ligne (**lossless, reconstructible**).
- Stage **lossy opt-in** : colonnes quasi-uniques haute entropie (timestamps, UUID) déplacées
  derrière un handle `Ref` content-addressé (jamais perdues).
- Sortie déterministe (clés triées, pas de mean/stddev). Reconstruction : `func Expand(out, refs) string`.
- **Gate CI A/B model-free** (port de `Condition::JsonCrush`) : sur un payload redondant, prouver
  que le crush conserve toutes les réponses gold tout en réduisant les tokens.
- Tests : round-trip lossless byte-égal ; lossy → handle résout l'original ; non-array passe inchangé.

### Brique B — `components/contentcomp/shellout` (package pur)
Sous-ensemble des patterns CLI/log/diff (headroom `log_compressor`/`diff_compressor`/`search_compressor`,
lean-ctx 95+ patterns).
- API : `func Compress(content string, opts ...Option) (out string, ref *Ref, err error)`.
- Table de patterns **opt-in** (git status/log, npm/cargo/pip install, docker, kubectl, diffs unifiés,
  logs répétitifs). Déterministe, sans regex coûteux au cold-start si possible (parsing explicite).
- Démarrer petit : 8–12 patterns à fort ROI, extensible via table déclarative.
- Tests : chaque pattern a une fixture entrée→sortie ; contenu non reconnu passe inchangé.

### Brique C — `contextopt` étendu (réversibilité + hook de contenu)
- Ajouter `Config.Backend contentcomp.Store` (optionnel). Quand présent, `pruneToolOutputs`
  **stocke l'original** et remplace par un marqueur + handle au lieu de couper sèchement
  (corrige le défaut destructif). Conserver `PruneMarkerKey` pour l'idempotence.
- Ajouter `Config.ContentCompressors []contentcomp.Compressor` appliqués **avant** la troncature :
  un tool-output JSON passe par `jsoncrush`, un output shell par `shellout`, avant tout prune.
  Ordre : compresser (réversible) → puis prune/troncature seulement si encore trop gros.
- Préserver les **invariants anti-double-coût LLM** du plan `history-lib-refactor` (même `TokenCounter`,
  `Window ≤ usable`, marqueurs partagés).
- Tests : prune réversible (original récupérable via Backend) ; compressor appliqué avant troncature ;
  idempotence sur appels répétés.

### Brique D — `components/middleware/cachestab` (nouveau, petit)
Port de headroom `tool_def_normalize` — **surface = définitions d'outils**, pas messages.
- Décorateur `model.ToolCallingChatModel` : à `WithTools`, trier les outils par nom (alpha) et
  trier récursivement les clés des JSON Schema (`parameters`/`input_schema`).
- **Déterministe, non destructif, cache-safe** ; ne modifie aucun message.
- Exclus de ce module : injection `cache_control` Anthropic / `prompt_cache_key` OpenAI
  (provider-specific, relève d'un proxy — voir Exclusions).
- Tests : sortie triée stable ; sémantique des outils inchangée (mêmes noms/params).

### Brique E — diagnostics & steering légers
- `contextopt` option `VolatileCheck` (port `volatile_detector`, **warn-only, non-mutant**) :
  détecte timestamps ISO-8601 / UUID v4 / champs `*_id` dans le préfixe caché (system + tools +
  premiers messages) et émet un log structuré via callback. Aucun octet modifié.
- `contextopt` option `VerbositySteer string` : **append-only** d'une consigne de concision en
  **fin** de system prompt (cache-safe, le préfixe ne bouge pas). Désactivé par défaut.
- Tests : non-mutation (byte-égal) pour VolatileCheck ; append en fin de system uniquement.

## Exclusions explicites (dangereux / faible gain — documenter dans les README)

- **SmartCrusher statistique / sampling (mean/stddev)** : non-déterministe → casse le prompt-cache ;
  risque d'éliminer les outliers (= erreurs). Complexité élevée (préservation mots-clés d'erreur + CCR)
  pour gain incertain. → on garde **uniquement** la variante déterministe `jsoncrush`.
- **ML / Kompress (torch)** : dépendance lourde, non-déterministe, pas de portage Go raisonnable.
- **Filtrage entropie/densité de la prose** sur l'historique conversationnel : détruit du texte
  sémantiquement important (pertinent pour file-read, pas pour l'historique).
- **Effort routing** (baisse du reasoning effort par turn) : risque de régression qualité, provider-specific.
- **Mutations byte provider-specific** : injection `cache_control` (Anthropic), `prompt_cache_key`
  (OpenAI), **relocation tail des champs volatils** → relèvent d'un proxy, fragiles aux API providers,
  peuvent changer la sémantique. Hors périmètre middleware.
- **Mémoire cross-agent / knowledge graph / property graph / search sémantique** : sous-systèmes
  entiers, pas des middlewares.

## Ordre d'implémentation suggéré

1. Brique A (`jsoncrush`) + gate A/B — fondation à fort ROI, indépendante.
2. Brique B (`shellout`) — indépendante, extensible.
3. Brique C (`contextopt` Backend réversible + hook ContentCompressor) — consomme A/B.
4. Brique D (`cachestab/tooldefs`).
5. Brique E (VolatileCheck + VerbositySteer).

## Validation

- `go test ./...` + `go vet ./...` sur le module.
- **Gate déterminisme** : pour chaque brique de contenu, `Compress(x)` byte-stable et
  re-`Compress` idempotent ; zones non compressées byte-égales (cache-safe).
- **Gate A/B model-free** (jsoncrush) : toutes les réponses gold conservées, tokens réduits.
- **Mesure de parité tokens** par brique sur fixtures (avant/après) pour quantifier le gain réel.
- Brique C : test anti-double-coût LLM (réutiliser les scénarios du plan history-lib).

## Décisions ouvertes (à trancher à l'implémentation)

- `contentcomp.Store` : réutiliser `adk/filesystem.Backend` tel quel, ou définir une interface
  minimale propre (découplage vs réutilisation). Reco : interface minimale propre satisfaite
  structurellement par `filesystem.Backend`.
- `shellout` : ampleur initiale de la table de patterns (8–12 vs portage large). Reco : démarrer
  petit, table déclarative extensible par PR communautaires.
- `jsoncrush` : activer le stage lossy par défaut (off) — reco : **off par défaut**, opt-in.

## Hors scope

- Tout proxy réseau / interception de requêtes (eino appelle les providers via SDK).
- Persistance cross-request de l'historique (couverte par `memory` / `memory/session`).
- Génération de résumé LLM (couverte par `contextopt.Summarizer` / `summarization`).
