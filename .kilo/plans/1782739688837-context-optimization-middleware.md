# Plan — `eino-ext` : middleware d'optimisation de contexte (inspiré kilocode)

> Nouveau package `components/middleware/contextopt` portant les stratégies de
> compaction/optimisation de contexte de kilocode dans l'écosystème eino, en
> respectant les conventions du repo et de l'ADK eino.

## Contexte / recherche

### Stratégies kilocode (source `/projects/kilocode/packages/opencode/src/session/`)
- **Détection d'overflow** (`overflow.ts`) : `usable()` = `model.limit.input - reserved`
  (buffer réservé ~20k, borné par max output tokens) ; `isOverflow()` déclenche quand
  `tokens >= cap`.
- **Compaction par résumé ancré** (`compaction.ts`) : résumé Markdown structuré
  (template `## Goal / Constraints & Preferences / Progress / Key Decisions /
  Next Steps / Critical Context / Relevant Files`), mis à jour incrémentalement
  via `<previous-summary>` (merge des faits encore vrais, suppression des périmés).
  Le message résumé est marqué (`summary: true`).
- **Préservation de la « tail »** (`select`/`splitTurn`/`preserveRecentBudget`) :
  garde les N derniers *tours* (`tail_turns`, défaut 2) dans un budget tokens
  (25 % de l'usable, borné 2k–8k), avec découpage fin d'un tour à cheval sur le budget.
- **Pruning des sorties d'outils** (`prune`) : en remontant l'historique, efface
  (`compacted`) les outputs de tool calls au-delà d'une fenêtre protégée
  (`PRUNE_PROTECT` 40k) seulement si le total éligible dépasse `PRUNE_MINIMUM` (20k) ;
  garde les 2 derniers tours et les outils protégés (`skill`).
- **Troncature des outputs d'outils** à la compaction (`TOOL_OUTPUT_MAX_CHARS` 2000).

### Côté eino (framework `github.com/cloudwego/eino@v0.8.5`)
- Point d'extension idiomatique : `adk.ChatModelAgentMiddleware` (interface ; embarquer
  `*adk.BaseChatModelAgentMiddleware` pour des no-op). La méthode
  `BeforeModelRewriteState(ctx, *adk.ChatModelAgentState, *adk.ModelContext)` reçoit
  `state.Messages` et permet de **réécrire l'historique avant chaque appel modèle**.
- Alternative portable hors-ADK : décorer un `model.BaseChatModel`
  (`Generate`/`Stream`) — cf. `components/model/interface.go`.
- Conventions repo `eino-ext` : `Config` struct + `New...` constructeur +
  `github.com/go-playground/validator/v10` + `emperror.dev/errors`.
- Module `memory` déjà présent et réutilisable : `memory.IsSummary`,
  `memory.NewSummaryMessage`, `memory.SummaryMarkerKey`, `memory.DefaultTokenCounter`,
  `memory.TokenCounter`.

## Décisions (validées avec le demandeur)
1. **Deux surfaces** partageant un core commun : un `adk.ChatModelAgentMiddleware`
   ET un décorateur `model.BaseChatModel` portable.
2. **`Summarizer` injectable** (interface/func) : le middleware reste découplé et
   testable sans LLM ; un adaptateur `NewModelSummarizer(model.BaseChatModel)` fournit
   l'implémentation LLM avec le prompt template kilocode.
3. Réutiliser les marqueurs/compteurs du module `memory` pour la cohérence
   (pas de duplication de `SummaryMarkerKey`/`IsSummary`/`DefaultTokenCounter`).
4. Aucune mutation destructive : le pruning marque les outputs via `Extra` et les
   remplace par un placeholder tronqué (n'efface pas l'historique d'audit en amont).

## Emplacement
- Nouveau package : `components/middleware/contextopt/`
  - `optimizer.go` (core pur, sans I/O)
  - `summarizer.go` (interface + adaptateur modèle + prompt template)
  - `middleware.go` (middleware ADK)
  - `model.go` (décorateur `model.BaseChatModel`)
  - `optimizer_test.go`, `summarizer_test.go`, `middleware_test.go`, `model_test.go`

## Tâches

### 1. Core `optimizer.go`
- `Config` (avec tags `validate`) :
  ```go
  type Config struct {
      // Fenêtre / overflow
      ContextLimit         int // limite totale de contexte du modèle (tokens)
      MaxInputTokens       int // si >0, prioritaire sur ContextLimit-Reserved
      ReservedTokens       int // buffer réservé (défaut 20_000)
      // Tail
      TailTurns            int // défaut 2 ; <=0 => pas de troncature de tail
      PreserveRecentTokens int // défaut: clamp(usable*0.25, 2_000, 8_000)
      // Pruning tool outputs
      PruneToolOutputs     bool
      PruneProtectTokens   int      // défaut 40_000
      PruneMinimum         int      // défaut 20_000
      ToolOutputMaxChars   int      // défaut 2_000
      ProtectedTools       []string // outputs jamais prunés
      // Dépendances injectées
      TokenCounter         memory.TokenCounter // défaut memory.DefaultTokenCounter
      Summarizer           Summarizer          // nil => pas de compaction LLM
  }
  ```
- `NewOptimizer(cfg *Config) (*Optimizer, error)` : validation + valeurs par défaut.
- Helpers portés de kilocode :
  - `usable() int` (port `overflow.ts:usable`).
  - `IsOverflow(msgs []*schema.Message) bool` (compte via `TokenCounter` vs `usable`).
  - `splitTurns(msgs) []turn` (port `turns`) ; un *tour* = message user (non-résumé)
    jusqu'au prochain user.
  - `selectTail(msgs) (head []*schema.Message, tailStartIdx int)` (port `select`/
    `splitTurn` + `preserveRecentBudget`).
  - `pruneToolOutputs(msgs) []*schema.Message` (port `prune` : garde 2 derniers tours,
    fenêtre `PruneProtectTokens`, seuil `PruneMinimum`, `ProtectedTools` ; remplace le
    contenu des `schema.Message{Role: Tool}` anciens par un placeholder tronqué et
    marque `Extra["__eino_ext_contextopt_pruned"]=true` pour idempotence).
  - `truncateToolOutput(content string) string` (port `TOOL_OUTPUT_MAX_CHARS`).
  - `trimBeforeLastSummary(msgs) []*schema.Message` (réutilise `memory.IsSummary`).
- Orchestration :
  ```go
  func (o *Optimizer) Optimize(ctx context.Context, msgs []*schema.Message) ([]*schema.Message, error)
  ```
  Pipeline :
  1. `out := trimBeforeLastSummary(msgs)`.
  2. si `PruneToolOutputs` : `out = pruneToolOutputs(out)`.
  3. si `IsOverflow(out)` et `Summarizer != nil` :
     - `head, tailStart := selectTail(out)` ;
     - `prev := lastSummaryText(out)` ;
     - `text, err := Summarizer.Summarize(ctx, head, prev)` ;
     - reconstruire `[]*schema.Message{ memory.NewSummaryMessage(text) } + out[tailStart:]`.
  4. retourner `out` (jamais d'erreur si `Summarizer` nil : on renvoie le trim/prune).

### 2. `summarizer.go`
- ```go
  type Summarizer interface {
      Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
  }
  ```
- `SummarizerFunc` (adaptateur func → interface).
- `NewModelSummarizer(m model.BaseChatModel, opts ...ModelSummarizerOption) Summarizer` :
  construit le prompt (template Markdown ancré kilocode + bloc `<previous-summary>`
  conditionnel), appelle `m.Generate`, renvoie le texte. Option pour override du
  template/instruction et de `ToolOutputMaxChars` lors de la sérialisation de l'historique.
- Exporter la constante `DefaultSummaryTemplate` (copie fidèle du template kilocode).

### 3. Middleware ADK `middleware.go`
- ```go
  type Middleware struct {
      *adk.BaseChatModelAgentMiddleware
      opt *Optimizer
  }
  func NewMiddleware(cfg *Config) (*Middleware, error)
  func (m *Middleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, mc *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error)
  ```
  Override unique : `state.Messages, err = m.opt.Optimize(ctx, state.Messages)`.
  Les autres méthodes proviennent de l'embed no-op.

### 4. Décorateur modèle `model.go`
- ```go
  type ChatModel struct {
      base model.BaseChatModel
      opt  *Optimizer
  }
  func NewChatModel(base model.BaseChatModel, cfg *Config) (*ChatModel, error)
  func (c *ChatModel) Generate(ctx, input, opts...) (*schema.Message, error)
  func (c *ChatModel) Stream(ctx, input, opts...) (*schema.StreamReader[*schema.Message], error)
  ```
  Chaque méthode applique `opt.Optimize` sur `input` avant de déléguer à `base`.
  Implémente bien `model.BaseChatModel`. Si `base` est `ToolCallingChatModel`,
  exposer `WithTools` qui re-wrappe (optionnel, à confirmer à l'implémentation).

### 5. Tests
- `IsOverflow` : sous/au-dessus du seuil ; `ReservedTokens` respecté.
- `selectTail` : conservation des N derniers tours ; split d'un tour qui dépasse
  le budget ; fallback quand tout dépasse.
- `pruneToolOutputs` : fenêtre protégée, `ProtectedTools`, seuil `PruneMinimum`
  non atteint => no-op ; idempotence (marqueur `Extra`).
- `truncateToolOutput` : longueur et placeholder.
- `trimBeforeLastSummary` : avec/sans résumé, plusieurs résumés (seul le dernier
  fait frontière) — cohérent avec `memory.IsSummary`.
- `Optimize` : pipeline complet avec un `Summarizer` fake → `[résumé + tail]` ;
  `Summarizer` nil + overflow => trim/prune seulement, pas d'erreur.
- `NewModelSummarizer` : prompt contient le template + `<previous-summary>` quand fourni
  (modèle fake renvoyant un texte fixe).
- Middleware ADK : `BeforeModelRewriteState` réécrit bien `state.Messages`.
- Décorateur modèle : parité (no-op quand pas d'overflow et pruning désactivé) ;
  `Generate`/`Stream` reçoivent l'input optimisé (base mock).

### 6. Documentation
- `components/middleware/contextopt/README.md` : exemples (middleware ADK + wrapper
  modèle), table des paramètres, note sur le `Summarizer` injectable et le défaut
  ~4 chars/token de `memory.DefaultTokenCounter`.

## Risques / points d'attention
- **Compteur de tokens** approximatif (~4 chars/token) : documenter qu'un counter
  précis (tiktoken/provider) peut être injecté via `Config.TokenCounter`.
- **Streaming** : le décorateur n'optimise que l'input ; la réécriture côté output
  n'est pas concernée.
- **Mapping des « tours »** : en eino un tour = message `user` → prochain `user` ;
  vérifier le comportement avec messages `tool`/`assistant` intercalés.
- **Idempotence du pruning** : indispensable car `Optimize` peut être rappelé à
  chaque tour (marqueur dans `Extra`).
- Ne pas introduire de dépendance LLM obligatoire dans le core : `Summarizer` reste
  optionnel (cohérent avec la contrainte du module `memory`).

## Validation
- `go build ./...` et `go test ./components/middleware/...` du module.
- `go vet ./components/middleware/...`.
- Vérifier l'absence d'import LLM dans `optimizer.go` (core pur).

## Hors scope
- Génération du résumé par défaut sans modèle (assurée par `Summarizer` injecté).
- Persistance de l'historique (gérée par le module `memory`).
- Backends de stockage et release/tagging du module.

## Implémentation
Ce plan nécessite la création/édition de fichiers source Go : basculer vers un agent
capable d'implémenter (mode non-plan) pour exécuter les tâches ci-dessus.
