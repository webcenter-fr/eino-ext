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
3. `Summarizer` : interface `{ Model; Prompt }` (prompt par défaut surchargeable, repris de
   `summarySystemPrompt`) + `Condense(ctx, threshold)` qui compte les tokens, déclenche au seuil et
   `AppendSummary`. Reprend la logique de `generateConversationSummary`.
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
  - Disconnect client pendant le streaming → vérifier l'état persisté (complet vs marqué incomplet).

## Hors scope

- Déplacer le filtrage des events ou le SSE dans la lib (politique applicative).
- Faire passer la persistance par un middleware eino (contresens architectural).
- Troncature du fichier JSONL sur disque.
