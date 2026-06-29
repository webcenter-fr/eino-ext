# Plan — `webcenter-fr/eino-ext` : mémoire conversationnelle avec résumé ancré

> Module externe (propriété du demandeur). À implémenter dans une session/IDE séparés.
> Objectif : faire évoluer `components/memory` pour supporter la condensation **non destructive**
> de l'historique façon kilocode (anchored summary), avec fenêtrage par **tokens**.
> Contrainte forte : **aucune dépendance LLM** dans ce module. Le texte du résumé est fourni
> par l'appelant (le projet `rancher-doc-chat-api-k8s`).

## Contexte / état actuel

Fichiers concernés (`github.com/webcenter-fr/eino-ext/components/memory`) :
- `memory.go` — interface `Memory` (`GetConversation`, `ListConversations`, `DeleteConversation`).
- `conversation.go` — interface `Conversation` (`Append`, `GetFullMessages`, `GetMessages`, `Load`, `Save`).
- `file/file.go` — implémentation fichier (`FileMemory`, `FileConversation`), stockage JSONL append-only.

Limites actuelles :
- `GetMessages()` borne par **nombre de messages** (`MaxWindowSize`), pas par tokens.
- Aucun moyen de marquer/stocker un résumé ni de tronquer l'historique envoyé au modèle.
- Pas de notion de "frontière de résumé" (dernier résumé ancré).

## Cible (pattern kilocode "anchored summary")

- Stockage **non destructif** : on conserve tout le log (audit). On **append** un message
  spécial marqué « résumé ». `GetMessages()` ne renvoie que `[dernier résumé + messages suivants]`
  (équivalent de `trimBeforeLastSummary`), borné par un budget de **tokens**.
- Le comptage de tokens est **injectable** (fonction), avec un estimateur par défaut (~4 chars/token).

## Décisions

1. Marqueur de résumé : clé constante dans `schema.Message.Extra`, ex.
   `Extra["__eino_ext_memory_summary"] = true`. Exposer une constante publique
   (`memory.SummaryMarkerKey`) et un helper `IsSummary(*schema.Message) bool`.
2. Fenêtrage par tokens (nouveau) tout en gardant `MaxWindowSize` (rétrocompat, optionnel).
3. Aucun LLM dans le module : l'appelant génère le texte du résumé et appelle une nouvelle
   méthode d'append de résumé.
4. Rétrocompat : conserver `Append`, `GetFullMessages`, `GetMessages`, `Load`, `Save`.

## Tâches

### 1. Type `TokenCounter`
- Dans `memory.go`, ajouter :
  ```go
  // TokenCounter retourne le nombre de tokens estimé pour un ensemble de messages.
  type TokenCounter func(msgs []*schema.Message) int
  ```
- Fournir un estimateur par défaut `DefaultTokenCounter` (~4 caractères par token,
  somme sur `Content` + arguments de tool calls). Aucune dépendance externe.

### 2. Constante + helper marqueur de résumé (`conversation.go`)
- `const SummaryMarkerKey = "__eino_ext_memory_summary"`.
- `func IsSummary(msg *schema.Message) bool` (lecture sûre de `Extra`).
- `func NewSummaryMessage(content string) *schema.Message` : message `Assistant`
  avec `Extra[SummaryMarkerKey] = true`.

### 3. Extension de l'interface `Conversation` (`conversation.go`)
Ajouter (sans casser l'existant) :
```go
type Conversation interface {
    Append(msg *schema.Message)
    GetFullMessages() []*schema.Message
    GetMessages() []*schema.Message          // conservé (fenêtre messages, rétrocompat)
    Load() error
    Save(msg *schema.Message) error

    // --- nouveau ---
    // AppendSummary ajoute un message-résumé marqué (non destructif).
    AppendSummary(summary *schema.Message)
    // GetWindow retourne [dernier résumé + messages suivants], borné par budget tokens.
    // Si budget <= 0, applique uniquement le découpage au dernier résumé.
    GetWindow(budget int) []*schema.Message
    // CountTokens compte les tokens de la fenêtre courante (via TokenCounter injecté).
    CountTokens() int
    // LastSummaryIndex retourne l'index du dernier message-résumé, ou -1.
    LastSummaryIndex() int
}
```
Notes d'implémentation `GetWindow(budget)` :
- Partir de `LastSummaryIndex()` ; si aucun résumé, partir de 0.
- Construire la tranche `[idxRésumé : fin]`.
- Si `budget > 0` et que la tranche dépasse le budget, retirer les messages **les plus anciens
  après le résumé** jusqu'à respecter le budget, mais **toujours conserver** le message-résumé
  et au moins le dernier message utilisateur. (Le résumé n'est jamais évincé.)

### 4. Config & injection du `TokenCounter` (`file/file.go`)
- `FileMemoryConfig` : ajouter
  ```go
  TokenCounter   TokenCounter // optionnel, défaut DefaultTokenCounter
  MaxWindowTokens int         // optionnel ; 0 = pas de borne tokens
  ```
- Propager `TokenCounter` + `MaxWindowTokens` à chaque `FileConversation`.
- Si `TokenCounter` nil → `DefaultTokenCounter`.

### 5. Implémentation `FileConversation` (`file/file.go`)
- `AppendSummary(summary)` : poser le marqueur si absent, puis `Append` (réutilise le `Save` JSONL).
- `GetWindow(budget)` : logique décrite en 3 ; si `budget == 0`, utiliser `MaxWindowTokens`.
- `CountTokens()` : `tokenCounter(GetWindow(MaxWindowTokens))`.
- `LastSummaryIndex()` : parcours arrière sur `Messages` via `IsSummary`.
- `Load()`/`Save()` inchangés (le marqueur survit car sérialisé dans `Extra`).

### 6. Tests (`file/file_test.go`)
- Round-trip JSONL préserve `Extra[SummaryMarkerKey]`.
- `GetWindow` sans résumé = tous les messages (ou borne tokens).
- `GetWindow` avec résumé = à partir du dernier résumé.
- Budget tokens évince les plus anciens après résumé mais garde résumé + dernier user.
- Plusieurs résumés successifs : seul le dernier fait frontière.
- `DefaultTokenCounter` croît avec la taille du contenu.

### 7. Release
- Tag une nouvelle version du module (pseudo-version Go) que le projet courant référencera.

## Risques / points d'attention
- Rétrocompat : ne pas changer la signature des méthodes existantes ; ajout seulement.
- L'estimateur par défaut sous-compte (comme partout) ; documenter qu'un counter précis
  (tiktoken/provider) peut être injecté côté appelant.
- Concurrence : conserver les `mutex` existants pour les nouvelles méthodes.

## Validation
- `go test ./...` du module.
- Vérifier que l'ancien comportement (`GetMessages` fenêtre messages) reste identique
  quand les nouvelles options ne sont pas configurées.

## Hors scope
- Génération du résumé (LLM) — assurée par le projet consommateur.
- Backends autres que `file` (à étendre ultérieurement de la même façon si besoin).
