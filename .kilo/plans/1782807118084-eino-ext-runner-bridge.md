# Plan A — Déporter le pont ADK→mémoire dans `webcenter-fr/eino-ext`

> À exécuter dans le dépôt `github.com/webcenter-fr/eino-ext` (autre IDE).
> Objectif : rendre réutilisable out-of-the-box la glue actuellement custom dans
> `rancher-doc-chat-api-k8s/internal/server/agent.go:RunAgent`, pour que chaque
> projet eino n'ait plus à réécrire le streaming + persistance.

## Contexte

Déjà présent dans la lib (`components/memory`) :
- `memory.Memory` / `Conversation` (+ `file.FileMemory`).
- `session.SessionManager` / `Turn` : `BeginTurn → Condense → Window → CommitAssistant/Discard`,
  locking ref-compté, condensation non-destructive.
- `TokenCounter`, `DefaultTokenCounter`, marqueurs de résumé (`SummaryMarkerKey`,
  `NewSummaryMessage`, `IsSummary`).

Reste custom côté projet (à généraliser) : la boucle qui consomme l'iterator du
`adk.Runner`, duplique le stream (`Copy(2)`), proxie les tokens du superviseur vers
le consommateur, et persiste la réponse complète via le `Turn` (avec garantie
no-dangling-user, marquage incomplete, exclusion des messages éphémères/tool-calls).

## Décisions de conception

- **Nouveau sous-package** `components/memory/runner` pour isoler l'import `adk`
  hors du package `session` (qui doit rester policy-free et sans dépendance adk).
- **Marqueurs génériques** ajoutés dans `components/memory` (à côté des marqueurs
  de résumé), pas dans le nouveau package, pour qu'ils soient partagés.
- **Prédicat injectable** : la politique « quels événements streamer + persister »
  reste du ressort de l'appelant, mais des briques prêtes à l'emploi sont fournies.
- **Hooks optionnels** plutôt qu'une politique figée (génération de la notice
  d'erreur, observation des événements ignorés).
- Le run est piloté sous `context.Background()` à l'intérieur du bridge (comme
  aujourd'hui) pour qu'une déconnexion client n'avorte pas la génération ni la
  persistance ; la condensation reste sous le contexte requête, gérée par
  l'appelant avant l'appel au bridge.

## Tâches

### 1. Marqueurs génériques — `components/memory/markers.go`
- Ajouter :
  - `const IncompleteMarkerKey = "__eino_ext_memory_incomplete"`
  - `const EphemeralMarkerKey  = "__eino_ext_memory_ephemeral"`
- Helpers :
  - `func MarkIncomplete(msg *schema.Message)` (crée `Extra` si nil, pose `true`).
  - `func IsIncomplete(msg *schema.Message) bool`.
  - `func NewEphemeralMessage(role schema.RoleType, content string) *schema.Message`
    (message marqué éphémère : streamé au client mais jamais persisté).
  - `func IsEphemeral(msg *schema.Message) bool`.
- Réutiliser le même style que `IsSummary`/`NewSummaryMessage`.

### 2. Prédicats composables — `components/memory/runner/predicate.go`
- `type MessagePredicate func(agentName string, role schema.RoleType) bool`
- Helpers :
  - `func AgentRole(agentName string, role schema.RoleType) MessagePredicate`
    (égalité nom + role) — couvre le cas « superviseur + assistant ».
  - `func And(preds ...MessagePredicate) MessagePredicate`
  - `func Or(preds ...MessagePredicate) MessagePredicate`
  - `func Not(p MessagePredicate) MessagePredicate`
- Défaut documenté : si `Predicate == nil`, tout message assistant est streamé+persisté.

### 3. Bridge — `components/memory/runner/runner.go`
- Signature principale :
  ```go
  type Config struct {
      Turn      *session.Turn                              // requis
      Iterator  *adk.AsyncIterator[*adk.AgentEvent]        // requis (retour de runner.Run)
      Predicate MessagePredicate                           // nil => assistant-only
      OnError   func(err error) *schema.Message            // optionnel : notice éphémère
      OnSkip    func(event *adk.AgentEvent)                // optionnel : debug/trace
      BufferSize int                                       // défaut 1000
  }
  func Run(cfg Config) (*schema.StreamReader[*schema.Message], error)
  ```
- Comportement (repris de `RunAgent`, généralisé) :
  1. Valider `cfg` (Turn + Iterator requis).
  2. `sr, sw := schema.Pipe[*schema.Message](bufferSize)` ; `srs := sr.Copy(2)`.
  3. **Goroutine proxy** (lit `Iterator`) :
     - sur `event.Err != nil` : poser `incomplete=true` ; si `OnError != nil`,
       envoyer `OnError(err)` (message éphémère) puis `sw.Send(nil, err)` ; continuer.
     - ignorer les events sans `Output.MessageOutput`.
     - si l'event ne passe pas le `Predicate` (nom/role) : appeler `OnSkip` si fourni ; continuer.
     - si `MessageOutput.IsStreaming` : boucler `MessageStream.Recv()` et `sw.Send(chunk, nil)`
       (proxy token-par-token, EOF => fin, autre erreur => `incomplete=true`).
     - sinon `sw.Send(mo.Message, nil)`.
     - `defer sw.Close()`.
  4. **Goroutine persistance** (lit `srs[1]`) :
     - drainer ; ignorer `chunk == nil`, `IsEphemeral(chunk)`, et `len(chunk.ToolCalls) > 0`.
     - accumuler dans `fullMsgs`.
     - si `fullMsgs` vide => `Turn.Discard()` (no-dangling-user).
     - `schema.ConcatMessages(fullMsgs)` ; en cas d'erreur => `Turn.Discard()`.
     - si `incomplete` => `MarkIncomplete(assistantMsg)`.
     - `Turn.CommitAssistant(assistantMsg)`.
     - `defer srs[1].Close()`.
  5. Retourner `srs[0]`.
- `incomplete` : `atomic.Bool` partagé entre les deux goroutines.
- Logging : via une dépendance déjà présente dans la lib (vérifier l'usage de
  `logrus` ou un logger maison dans `components/`), sinon laisser les hooks porter
  l'observabilité et éviter d'introduire une dépendance de log.

### 4. Tests — `components/memory/runner/runner_test.go`
- Faux `adk.AsyncIterator` (ou via un `adk.Runner` minimal) produisant :
  - un flux assistant superviseur streamé → vérifier proxy + persistance concaténée.
  - un event sous-agent / role tool → vérifier exclusion de la persistance.
  - une erreur d'iterator → vérifier `OnError` éphémère non persisté + `MarkIncomplete`.
  - aucun contenu assistant → vérifier `Turn.Discard()` (rien persisté, pas de user dangling).
- Tests unitaires des marqueurs (`markers_test.go`) et prédicats (`predicate_test.go`).

### 5. Documentation
- Doc package en tête de `runner.go` décrivant le découpage proxy/persistance et
  les garanties (no-dangling-user, incomplete, éphémère).
- Mettre à jour le README/exemples du composant `memory` si présent.

## Risques / points de vigilance
- Vérifier la signature exacte du type iterator renvoyé par `adk.Runner.Run` dans
  la version d'eino utilisée (`*adk.AsyncIterator[*adk.AgentEvent]`) et adapter.
- Garder `session` sans import `adk` : tout ce qui touche adk vit dans `runner`.
- Ne pas changer la sémantique des marqueurs de résumé existants.

## Validation
- `go build ./...` et `go test ./components/memory/...` dans le dépôt lib.
- Publier un tag/commit ; noter la pseudo-version pour le Plan B.

## Hand-off Plan B
- Le Plan B (projet) dépend de la version publiée ici. Communiquer la
  pseudo-version `go get` résultante.
