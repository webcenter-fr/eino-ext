# Refactor de la persistance de l'historique : lib `eino-ext` + correctifs projet

## Contexte

La persistance de l'historique de conversation est aujourd'hui répartie ainsi :

- **Lib `github.com/webcenter-fr/eino-ext/components/memory`** : uniquement les primitives de
  stockage — interfaces `Memory` / `Conversation`, implémentation `file` (JSONL), helpers
  `NewSummaryMessage` / `IsSummary` / `TokenCounter`.
- **Projet (`internal/server/agent.go`)** : tout le cycle de vie orchestré — verrouillage par
  session (`sessionMutexes`), `load → append user → condense → window → run → persist assistant`,
  condensation inter-requête (`generateConversationSummary` + `summarySystemPrompt`), lecture du
  flux `AgentEvent` de l'adk et persistance en arrière-plan.

### Constat d'architecture

La gestion de l'historique ne doit **pas** passer par un middleware eino : les middlewares adk
(`summarization`, `reduction`) sont **intra-run** (transforment la liste de messages d'un seul
`runner.Run`) et ne persistent rien entre requêtes ; le `CheckPointStore` adk sert à *reprendre un
run interrompu*, pas à stocker un historique listable/supprimable. La séparation
« middleware = compaction intra-run » / « `memory.Memory` = persistance cross-request » est donc
correcte.

En revanche, le **découpage lib/projet est sous-optimal** : une grande part du cycle de vie est
générique et réimplémentable à l'identique dans tout projet eino, mais vit dans le projet.

### Relation avec le middleware officiel `adk/middlewares/summarization` (eino PR #729)

eino v0.8.5 fournit désormais un middleware officiel de summarization (PR #729) :
`summarization.New(ctx, &summarization.Config{Model, TokenCounter, Trigger.MaxTokens, Instruction,
TranscriptFilePath, PreserveUserMessages, Finalize, Callback, EmitInternalEvents})`. Comme
`contextopt`, il est **intra-run** (`BeforeModelRewriteState`, réécrit `state.Messages`, ne persiste
rien). Il expose `Finalize`/`Callback`/`ActionTypeAfterSummary` qui permettraient de **capter** le
résumé produit intra-run pour le persister (« Design A », un seul appel LLM).

**Décision retenue : Design B — condenser cross-request séparé.** Justification :
- **Cadence indépendante** : la persistance doit pouvoir se déclencher selon un seuil cross-request
  propre, et non être asservie au seuil d'overflow intra-run du middleware (qui ne fire que pendant
  un `runner.Run`). Le hook `Finalize` ne couvre pas une cadence de persistance autonome.
- **Découplage** : la persistance ne doit pas dépendre du fait qu'un run a overflow, ni du format de
  marqueur interne de #729 (`_eino_summarization_content_type`), distinct de `memory.SummaryMarkerKey`.
- **Coût LLM maîtrisé sans #729** : l'invariant anti-double-coût (même `TokenCounter`, budget
  `Window ≤ usable`, marqueurs partagés — voir tâche 3) garantit déjà qu'un tour ne summarize pas
  deux fois la même frontière.

**Conséquence à assumer (duplication consciente)** : le condenser persistant réimplémente la
mécanique « count tokens → appel LLM → message résumé » que #729 (et `contextopt`) réalisent aussi.
On limite cette duplication en **réutilisant le `Summarizer` de `contextopt`** (tâche 3) plutôt que
d'écrire un nouveau prompt, et en gardant le condenser réduit à l'orchestration de persistance.
Le middleware intra-run (`contextopt` et/ou #729) reste le filet de sécurité du run ; le condenser
cross-request reste l'autorité de persistance.

## Décisions retenues

- **Périmètre lib** : remonter le lifecycle générique + le Summarizer dans la lib. La lecture du
  flux adk et le SSE restent côté projet (politique applicative injectée).
- **Compatibilité** : breaking autorisé sur la lib si cela simplifie le design (la lib est gérée
  par l'utilisateur).
- **Frontière** : mécanisme générique dans la lib, politique (filtrage des events, SSE) injectée
  par le projet.

## Partie A — Lib `webcenter-fr/eino-ext` (dépôt source, puis bump de version)

> À implémenter dans le dépôt source de la lib : le module est en lecture seule dans
> `/go/pkg/mod`. Publier/versionner avant la Partie B.

1. Nouveau paquet `components/memory/session` : `SessionManager` encapsulant un `Memory` + une map
   de mutex par clé `<userId>:<id>`, **avec suppression de l'entrée** sur `DeleteConversation` et/ou
   en fin de tour (ref-count) → corrige la fuite mémoire.
2. Lifecycle de tour :
   - `BeginTurn(userId, id, userMsg)` → lock + load + append du message utilisateur, retourne un
     handle.
   - `Window(budget)` → fenêtre `[dernier résumé + messages récents]`.
   - `CommitAssistant(msg)` → append + unlock, avec garde contre double-unlock.
3. **Condensation persistante — réutiliser le Summarizer de `contextopt`** (pas de
   réimplémentation LLM). `contextopt` expose déjà :
   - `type Summarizer interface { Summarize(ctx, history []*schema.Message, previousSummary string) (string, error) }`
   - `NewModelSummarizer(model, opts...)` avec le template ancré (`DefaultSummaryTemplate`), la
     gestion du `<previous-summary>` incrémental et le rendu de l'historique (tool-calls inclus).

   Décisions :
   - Le paquet `session` définit **sa propre interface minimale** de même signature
     (`Summarize(ctx, history, previousSummary) (string, error)`) afin d'éviter toute dépendance
     `memory/session → middleware/contextopt` (typage structurel Go : une instance
     `contextopt.NewModelSummarizer(...)` la satisfait directement). Le projet construit **une
     seule** instance partagée entre le middleware et le condenser → prompt = source unique.
   - `Condense(ctx, threshold)` ne contient plus que l'orchestration spécifique à la persistance :
     compter les tokens de la fenêtre (`CountTokens`/`GetWindow`), déclencher au seuil, appeler
     `Summarize(window, lastSummaryText(window))`, puis `AppendSummary(NewSummaryMessage(text))`
     (persisté). Remplace `generateConversationSummary` ; le prompt `summarySystemPrompt` est
     abandonné au profit de `DefaultSummaryTemplate`.
   - Marqueurs partagés (`memory.SummaryMarkerKey`/`IsSummary`/`NewSummaryMessage`) : les résumés
     persistés sont reconnus par `contextopt` (`trimBeforeLastSummary`, `lastSummaryText`) et
     réciproquement → interopérabilité native.
   - **Invariant anti-double-coût LLM (garantie, pas un réglage).** Le middleware ne ré-appelle le
     LLM que si `IsOverflow(window) && Summarizer != nil` (`optimizer.go:392`). Pour qu'un tour ne
     paie **jamais** deux fois le résumé de l'historique déjà persisté, garantir les 3 conditions
     ensemble :
     1. **Même `TokenCounter`** injecté côté `session` et côté `contextopt.Config` (sinon les
        estimations divergent et la garantie tombe).
     2. **Budget de `Window(budget)` ≤ `usable()` du middleware** (`MaxInputTokens`, sinon
        `ContextLimit − ReservedTokens`) : après `Condense`, la fenêtre `[résumé + tail]` ne peut
        pas déclencher l'overflow au premier appel modèle.
     3. Marqueurs de résumé partagés (déjà acquis) : `trimBeforeLastSummary` du middleware démarre
        exactement au résumé que `Condense` vient de persister.
     ⇒ Le résumé de frontière de tour est calculé **une seule fois** (dans `Condense`). Une
     éventuelle summarization du middleware ne survient qu'en cas d'overflow **intra-run** (history
     qui grossit pendant un run à tool-calls), incrémentale via `previousSummary` — ce n'est pas un
     doublon du résumé de frontière.
   - **Option garantie « un seul appelant LLM »** (si le rescue intra-run par LLM n'est pas requis) :
     configurer `contextopt.Config.Summarizer = nil`. Le middleware ne fait alors que du travail pur
     sans LLM (`trimBeforeLastSummary` + `pruneToolOutputs`) ; l'unique summarizer LLM vit dans le
     condenser persistant. Compromis : l'overflow intra-run est géré par trim/prune, pas par résumé.
4. Ajuster les interfaces `Memory` / `Conversation` si cela simplifie (breaking acceptable) ; sinon
   rester additif.
5. Tests unitaires lib : lifecycle, condensation au seuil, nettoyage des mutex, concurrence.

## Partie B — Projet `rancher-doc-chat-api-k8s`

6. Réécrire `RunAgent` (`internal/server/agent.go`) pour déléguer au `SessionManager` /
   `Summarizer` ; supprimer `sessionMutexes`, `sessionMutex`, `generateConversationSummary`,
   `summarySystemPrompt`.
7. Conserver côté projet le **prédicat de persistance** (events `AgentName=="supervisor"` de rôle
   assistant, exclusion des tool-calls) et le câblage SSE, injectés dans le helper lib.
8. Bump de la dépendance lib dans `go.mod` ; adapter `server.go` (construction du `SessionManager`
   + du `Summarizer`).

## Partie B-bis — Correctifs des anomalies détectées

9. **Messages d'erreur persistés** (`agent.go:159-166` + filtre ligne `238`) : le message
   synthétique « An error occurred... » est consommé par la copie de persistance (`srs[1]`) et écrit
   dans l'historique. → Router l'erreur uniquement vers le flux SSE, l'exclure de la copie persistée.
10. **Fuite mémoire `sessionMutexes`** (`agent.go:36`) : entrées `<user>:<id>` jamais supprimées.
    → Nettoyage assuré par le `SessionManager` de la lib (suppression sur `DeleteConversation`
    et/ou ref-count en fin de tour).
11. **Réponses partielles sur stream tronqué** (`agent.go:230-237`) : une réponse interrompue est
    persistée telle quelle. → Ne pas persister une réponse incomplète, ou la marquer comme
    incomplète via `Extra`.
12. **Tool-calls / sous-agents non persistés** (filtre supervisor `agent.go:184`, exclusion
    tool-calls `238`) : confirmer que l'omission est intentionnelle et la documenter dans le
    prédicat de persistance (sinon persister un résumé minimal des délégations).
13. **Persistance malgré disconnect client** (`agent.go:83`, `ctx=context.Background()`) :
    comportement voulu (sauvegarder la réponse complète) ; à documenter, en s'appuyant sur la
    tâche 11 pour ne pas confondre réponse partielle et réponse complète.

## Décisions ouvertes (à trancher à l'implémentation)

- Tâche 11 : ne pas persister vs marquer `incomplete`.
- Tâche 12 : omission documentée vs résumé minimal des délégations.

## Validation

- Tests unitaires de la lib (lifecycle, condensation, nettoyage mutex, concurrence).
- Projet : `go build ./...` + `go vet ./...`.
- Scénarios manuels :
  - Message provoquant une erreur → recharger l'historique : aucune erreur persistée.
  - Supprimer une conversation → vérifier la suppression de l'entrée mutex.
  - Atteindre le seuil de tokens → vérifier la condensation et la fenêtre `[résumé + récents]`.
  - **Anti-double-coût LLM** : avec persistance + middleware actifs, instrumenter/mock le modèle de
    résumé et vérifier qu'un tour franchissant le seuil ne déclenche **qu'un seul** appel LLM de
    summarization (le middleware ne re-summarize pas la fenêtre `[résumé + tail]` déjà condensée).
  - Disconnect client pendant le streaming → vérifier l'état persisté (complet vs marqué incomplet).

## Hors scope

- Déplacer le filtrage des events ou le SSE dans la lib (politique applicative).
- Faire passer la persistance par un middleware eino (contresens architectural).
- Troncature du fichier JSONL sur disque.
