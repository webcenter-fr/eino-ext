# Plan — Révision complète du codebase eino-ext

> Objectifs : maintenabilité maximale, compréhensibilité humaine, ergonomie
> library (interfaces + implémentation par défaut), détection de bugs et failles.
> Chaque phase est livrable indépendamment. La Phase 0 est bloquante.

---

## Phase 0 — Bugs et failles de sécurité (P0, bloquant)

### 0.1 — `context.Background()` au lieu du `ctx` appelant

**Fichier** : `components/tool/kubernetes/generic_describe.go:60`
```go
// AVANT
if err = c.Get(context.Background(), client.ObjectKey{...}, o); err != nil {
// APRÈS
if err = c.Get(ctx, client.ObjectKey{...}, o); err != nil {
```
**Impact** : annule timeouts/annulations de l'appelant. Bug silencieux en production.

### 0.2 — Messages d'erreur hardcodés dans les generics

**Fichiers** :
- `generic_list.go:72` : `"invalid parameters for PodListTool"` → doit être `"invalid parameters for ListTool"`
- `generic_describe.go:51` : `"invalid parameters for PodDescribeTool"` → doit être `"invalid parameters for DescribeTool"`
- `resource_list.go:116` : `"invalid parameters for PodListTool"` → doit être `"invalid parameters for ResourceListTool"`

**Correction** : utiliser le nom réel du tool via `fmt.Sprintf("invalid parameters for %s", toolName)` ou un nom générique correct.

### 0.3 — Injection JSON dans le paginateToken

**Fichiers** : `generic_list.go:123`, `resource_list.go:171`
```go
// AVANT (vulnérable)
outputs = append(outputs, json.RawMessage(fmt.Sprintf(`{"paginateToken": "%s"}`, continueToken)))

// APRÈS (sûr)
type paginateToken struct {
    PaginateToken string `json:"paginateToken"`
}
tokenData, err := json.Marshal(paginateToken{PaginateToken: continueToken})
if err != nil {
    return "", errors.Wrap(err, "failed to marshal paginate token")
}
outputs = append(outputs, json.RawMessage(tokenData))
```
**Impact** : un `continueToken` contenant `"` ou `\` produit du JSON invalide ou injectable.

### 0.4 — `pod_exec` : parsing de commande non sûr

**Fichier** : `components/tool/kubernetes/pod_exec.go:97`
```go
// AVANT
Command: strings.Fields(params.Command),
```
**Problème** : `strings.Fields` ne respecte pas les guillemets. `cat "/etc/my file"` devient `["cat", '"/etc/my', 'file"']`.

**Correction** : accepter `Command []string` dans `PodExecParams` (breaking change mais correct).
```go
type PodExecParams struct {
    // ...
    Command []string `json:"command" validate:"required,min=1" jsonschema:"(required) The command to execute as an array of strings. Example: [\"ls\", \"-la\", \"/app\"]."`
    // ...
}
```
Mettre à jour la description pour refléter le nouveau format. Le LLM comprend parfaitement les tableaux.

### 0.5 — `pod_exec` : pas de garde-fou code pour les commandes destructrices

**Fichier** : `components/tool/kubernetes/pod_exec.go`

**Correction** : ajouter une blocklist configurable.
```go
type PodExecTool struct {
    // ...
    blocklist []*regexp.Regexp // patterns de commandes bloquées
}

// Dans le constructeur :
var defaultBlocklist = []string{
    `^\s*rm\s`, `^\s*kill\s`, `^\s*shutdown`, `^\s*reboot`,
    `^\s*dd\s`, `^\s*mkfs`, `^\s*chmod\s+-R\s+/`,
}
```
Si `params.Command` match un pattern → erreur explicite. Configurable via option.

### 0.6 — Panics dans les helpers de réflexion

**Fichiers** : `generic_list.go` (`GetItems`, `CloneObject`), `generic_describe.go` (`GetObjectStatus`, `GetObjectSpec`, `GetDataSpec`, `SetObjectTypeMeta`)

**Correction** : transformer les panics en erreurs retournées.

```go
// AVANT
func GetItems[k8sObjectList client.ObjectList, k8sObject client.Object](o k8sObjectList) (items []k8sObject) {
    if reflect.ValueOf(o).IsNil() {
        panic("ressource can't be nil")
    }
    // ...
}

// APRÈS
func GetItems[k8sObjectList client.ObjectList, k8sObject client.Object](o k8sObjectList) ([]k8sObject, error) {
    if reflect.ValueOf(o).IsNil() {
        return nil, errors.New("resource list cannot be nil")
    }
    val := reflect.ValueOf(o).Elem()
    valueField := val.FieldByName("Items")
    if !valueField.IsValid() {
        return nil, errors.Errorf("resource list of type %T has no Items field", o)
    }
    items := make([]k8sObject, valueField.Len())
    for i := range items {
        items[i] = valueField.Index(i).Addr().Interface().(k8sObject)
    }
    return items, nil
}
```

Même traitement pour `CloneObject`, `GetObjectStatus`, `GetObjectSpec`, `GetDataSpec`, `SetObjectTypeMeta`. Propager les erreurs dans `Invoke`.

### 0.7 — `pod_exec` streaming : bufferise tout avant de streamer

**Fichier** : `pod_exec.go:140-208`

**Problème** : `InvokeAsStream` fait `exec.StreamWithContext` dans un `bytes.Buffer`, puis stream les résultats. Latence = temps d'exécution total.

**Correction** : utiliser `io.Pipe` pour un vrai streaming.
```go
func (t *PodExecTool) InvokeAsStream(ctx context.Context, params *PodExecParams) (*schema.StreamReader[string], error) {
    c, config, err := t.validate(params)
    if err != nil {
        return nil, err
    }
    re, err := CompileFilter(params.FilterPattern)
    if err != nil {
        return nil, err
    }

    // ... construire req + exec comme avant ...

    stdoutR, stdoutW := io.Pipe()
    stderrR, stderrW := io.Pipe()

    go func() {
        defer stdoutW.Close()
        defer stderrW.Close()
        execErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
            Stdout: stdoutW,
            Stderr: stderrW,
        })
        if execErr != nil {
            stdoutW.CloseWithError(execErr)
            stderrW.CloseWithError(execErr)
        }
    }()

    sr, sw := schema.Pipe[string](100)
    go func() {
        defer sw.Close()
        // Lire stdout ligne par ligne, filtrer, envoyer
        scanner := bufio.NewScanner(stdoutR)
        for scanner.Scan() {
            line := scanner.Text()
            if re == nil || re.MatchString(line) {
                if closed := sw.Send(line, nil); closed {
                    return
                }
            }
        }
        // Lire stderr si présent
        // ...
    }()
    return sr, nil
}
```

### 0.8 — Supprimer le fichier vide

**Fichier** : `components/tool/kubernetes/pod_debug.go` (contient uniquement `package kubernetes`)

**Action** : `rm components/tool/kubernetes/pod_debug.go`

### 0.9 — Double import dans `generic_describe.go`

**Fichier** : `generic_describe.go:16-17`
```go
metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
```
**Correction** : supprimer `v1`, utiliser `metav1` partout.

### 0.10 — `opensearch_log_kubernetes.go` : duplication complète Invoke/InvokeAsStream

**Fichier** : `opensearch_log_kubernetes.go`

**Problème** : la construction de la requête (lignes 66-103 et 128-166) est dupliquée mot pour mot.

**Correction** : extraire une méthode privée `buildQuery(params) (*opensearch.SearchRequest, error)` partagée.

### 0.11 — `convertor/object.go` : doc comment incorrect

**Fichier** : `convertor/object.go:36`
```go
// Invoke executes the DescribeTool with the given parameters...
```
**Correction** : remplacer par la doc correcte pour le ConvertorTool.

### 0.12 — `opensearch_log_kubernetes.go` : doc comments incorrects

**Fichier** : `opensearch_log_kubernetes.go:65,127`
```go
// Invoke executes the DescribeTool with the given parameters...
```
**Correction** : remplacer par les docs correctes pour Invoke et InvokeAsStream.

---

## Phase 1 — Extraire le code partagé dans `libs/toolkit/`

### 1.1 — `libs/toolkit/filter/filter.go`

Extraire `CompileFilter` et `IsMatch` (actuellement dupliqués dans `argocd/helper.go` et `kubernetes/helper.go`).

```go
package filter

import (
    "regexp"
    "emperror.dev/errors"
    "github.com/goccy/go-json"
)

// Compile compiles a RE2 regex pattern. Empty pattern returns nil (match-all).
func Compile(pattern string) (*regexp.Regexp, error) {
    if pattern == "" {
        return nil, nil
    }
    re, err := regexp.Compile(pattern)
    if err != nil {
        return nil, errors.Wrapf(err, "invalid regex filter %q (Go RE2 syntax)", pattern)
    }
    return re, nil
}

// Match returns true if the JSON data matches the compiled regex.
// A nil filter matches everything.
func Match(data json.RawMessage, filter *regexp.Regexp) bool {
    if filter == nil {
        return true
    }
    return filter.Match(data)
}

// MatchString returns true if the string matches the compiled regex.
func MatchString(s string, filter *regexp.Regexp) bool {
    if filter == nil {
        return true
    }
    return filter.MatchString(s)
}
```

Tests : `filter_test.go` avec patterns valides, invalides, vides, et match/no-match.

### 1.2 — `libs/toolkit/guidance/guidance.go`

Extraire les constantes de guidance avec des variantes paramétrables.

```go
package guidance

import "fmt"

// List produces a guidance block for list tools.
// Fields are the parameter names available for narrowing (e.g. "namespace", "labelsSelector").
func List(fields ...ListField) string { ... }

// Describe produces a guidance block for describe tools.
func Describe(excludeFields ...string) string { ... }

type ListField struct {
    Name        string // e.g. "namespace"
    Description string // e.g. "Set `namespace` whenever you know it."
}
```

### 1.3 — `libs/toolkit/validate/validate.go`

Validateur singleton thread-safe.

```go
package validate

import (
    "sync"
    "emperror.dev/errors"
    "github.com/go-playground/validator/v10"
)

var (
    once sync.Once
    v    *validator.Validate
)

func get() *validator.Validate {
    once.Do(func() { v = validator.New() })
    return v
}

// Struct validates a struct using the shared validator instance.
func Struct(s any) error {
    if err := get().Struct(s); err != nil {
        return errors.Wrapf(err, "invalid parameters for %T", s)
    }
    return nil
}
```

**Bénéfice** : `validator.New()` est coûteux (~2ms). Actuellement recréé à chaque `Invoke()`.

### 1.4 — `libs/toolkit/marshal/marshal.go`

```go
package marshal

import "github.com/goccy/go-json"

// MustMarshal marshals v to JSON, panics on error.
func MustMarshal(v any) []byte {
    b, err := json.Marshal(v)
    if err != nil {
        panic(err)
    }
    return b
}

// MustUnmarshal unmarshals JSON into v, panics on error.
func MustUnmarshal(data []byte, v any) {
    if err := json.Unmarshal(data, v); err != nil {
        panic(err)
    }
}
```

### 1.5 — Migration des helpers existants

Après création de `libs/toolkit/`, migrer :
- `argocd/helper.go` → importer depuis `libs/toolkit/filter`, `libs/toolkit/validate`, `libs/toolkit/marshal`
- `kubernetes/helper.go` → idem
- Garder les constantes `listOutputGuidance`/`describeOutputGuidance` locales ou migrer vers `guidance`

---

## Phase 2 — Refactorer les tools ArgoCD

### 2.1 — Créer `baseTool` dans `components/tool/argocd/base.go`

```go
package argocd

import (
    "github.com/disaster37/goargocdclient/api"
)

// baseTool holds shared state for all ArgoCD tools.
type baseTool struct {
    clients        map[string]api.API
    knownInstances []string
}

// client returns the API client for the given instance name.
func (b *baseTool) client(instance string) (api.API, error) {
    c, ok := b.clients[instance]
    if !ok {
        return nil, instanceNotFoundError(instance, b.knownInstances)
    }
    return c, nil
}

// newBaseTool builds clients and returns a baseTool.
func newBaseTool(configs Configs) (*baseTool, error) {
    clients, err := BuildClients(configs)
    if err != nil {
        return nil, err
    }
    return &baseTool{
        clients:        clients,
        knownInstances: configs.GetInstanceNames(),
    }, nil
}
```

### 2.2 — Refactorer chaque tool ArgoCD

**Pattern cible** (exemple `ApplicationListTool`) :
```go
type ApplicationListTool struct {
    *baseTool
    tool.InvokableTool
}

func (t *ApplicationListTool) Invoke(ctx context.Context, params *ApplicationListParams) (string, error) {
    if err := validateParams(params); err != nil {
        return "", err
    }
    c, err := t.client(params.Instance)
    if err != nil {
        return "", err
    }
    // ... logique métier uniquement ...
}

func NewApplicationListTool(ctx context.Context, configs Configs) (*ApplicationListTool, error) {
    base, err := newBaseTool(configs)
    if err != nil {
        return nil, err
    }
    listTool := &ApplicationListTool{baseTool: base}
    t, err := utils.InferTool("argocd_application_list",
        fmt.Sprintf("%s\n%s", applicationListDescription, listOutputGuidance),
        listTool.Invoke)
    if err != nil {
        return nil, err
    }
    listTool.InvokableTool = t
    return listTool, nil
}
```

**Fichiers impactés** (13 tools) :
- `application_create.go`
- `application_delete.go`
- `application_describe.go`
- `application_list.go`
- `application_sync.go`
- `certificate_list.go`
- `cluster_describe.go`
- `cluster_list.go`
- `instance_list.go` (cas spécial : pas de clients, juste `knownInstances`)
- `project_describe.go`
- `project_list.go`
- `repository_describe.go`
- `repository_list.go`

### 2.3 — Registry : `components/tool/argocd/registry.go`

```go
package argocd

import (
    "context"
    "github.com/cloudwego/eino/components/tool"
)

// NewAllTools creates all ArgoCD tools for the given configurations.
// Returns a flat slice ready to be registered with an eino ToolsNode.
func NewAllTools(ctx context.Context, configs Configs) ([]tool.BaseTool, error) {
    constructors := []func(context.Context, Configs) (tool.BaseTool, error){
        wrapTool(NewInstanceListTool),
        wrapTool(NewApplicationListTool),
        wrapTool(NewApplicationDescribeTool),
        wrapTool(NewApplicationCreateTool),
        wrapTool(NewApplicationDeleteTool),
        wrapTool(NewApplicationSyncTool),
        wrapTool(NewCertificateListTool),
        wrapTool(NewClusterListTool),
        wrapTool(NewClusterDescribeTool),
        wrapTool(NewProjectListTool),
        wrapTool(NewProjectDescribeTool),
        wrapTool(NewRepositoryListTool),
        wrapTool(NewRepositoryDescribeTool),
    }

    tools := make([]tool.BaseTool, 0, len(constructors))
    for _, fn := range constructors {
        t, err := fn(ctx, configs)
        if err != nil {
            return nil, err
        }
        tools = append(tools, t)
    }
    return tools, nil
}
```

**Bénéfice** : un seul appel pour créer tous les tools ArgoCD. Essentiel pour l'ergonomie.

---

## Phase 3 — Refactorer les tools Kubernetes

### 3.1 — Créer `baseTool` dans `components/tool/kubernetes/base.go`

```go
package kubernetes

import (
    "sigs.k8s.io/controller-runtime/pkg/client"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/dynamic"
)

// baseTool holds shared state for all Kubernetes tools.
type baseTool struct {
    clients       map[string]client.Client
    clientsets    map[string]*kubernetes.Clientset
    dynamics      map[string]dynamic.Interface
    configs       Configs
    knownClusters []string
}

func (b *baseTool) client(cluster string) (client.Client, error) {
    c, ok := b.clients[cluster]
    if !ok {
        return nil, clusterNotFoundError(cluster, b.knownClusters)
    }
    return c, nil
}

func (b *baseTool) clientset(cluster string) (*kubernetes.Clientset, error) {
    c, ok := b.clientsets[cluster]
    if !ok {
        return nil, clusterNotFoundError(cluster, b.knownClusters)
    }
    return c, nil
}

// newBaseTool builds all client types for the given configs.
func newBaseTool(configs Configs, scheme *runtime.Scheme, opts ...BaseToolOption) (*baseTool, error) { ... }

type BaseToolOption func(*baseToolConfig)
func WithClientsets() BaseToolOption { ... }
func WithDynamics() BaseToolOption { ... }

func clusterNotFoundError(cluster string, known []string) error {
    return errors.Errorf("Kubernetes cluster not found: %s. Cluster must be one of: %s",
        cluster, strings.Join(known, ", "))
}
```

### 3.2 — Refactorer `generic_list.go`

- Embed `*baseTool` dans `ListTool`
- Corriger les messages d'erreur (Phase 0.2)
- Corriger l'injection JSON paginateToken (Phase 0.3)
- Remplacer panics par erreurs (Phase 0.6)
- Utiliser `ctx` correctement

### 3.3 — Refactorer `generic_describe.go`

- Embed `*baseTool` dans `DescribeTool`
- Corriger `context.Background()` → `ctx` (Phase 0.1)
- Corriger double import (Phase 0.9)
- Remplacer panics par erreurs (Phase 0.6)

### 3.4 — Refactorer `resource_list.go` et `resource_describe.go`

- Embed `*baseTool` avec clients dynamiques
- Corriger message d'erreur hardcodé (Phase 0.2)
- Corriger injection JSON paginateToken (Phase 0.3)

### 3.5 — Refactorer `pod_exec.go`

- Embed `*baseTool`
- Corriger le parsing de commande (Phase 0.4)
- Ajouter la blocklist (Phase 0.5)
- Corriger le streaming (Phase 0.7)

### 3.6 — Refactorer `pod_log.go`

- Embed `*baseTool`
- Extraire la construction de la requête dans une méthode partagée

### 3.7 — Refactorer les wrappers de ressources (30 fichiers)

Pas de changement structurel nécessaire — ils sont déjà des thin wrappers.
Vérifier que les `ToJson` ne panic pas (certains utilisent `panic(err)` dans `json.Marshal`).
Remplacer par un retour d'erreur si possible, ou documenter que `MustMarshal` est acceptable ici (le JSON marshal d'une struct Go ne peut pas échouer en pratique).

### 3.8 — Registry : `components/tool/kubernetes/registry.go`

```go
package kubernetes

import (
    "context"
    "github.com/cloudwego/eino/components/tool"
    "k8s.io/apimachinery/pkg/runtime"
)

// NewAllTools creates all Kubernetes tools for the given configurations.
func NewAllTools(ctx context.Context, configs Configs, scheme *runtime.Scheme) ([]tool.BaseTool, error) {
    // Core tools
    tools := []tool.BaseTool{}

    // List tools
    listConstructors := []func(context.Context, Configs) (tool.InvokableTool, error){
        NewPodListTool,
        NewDeploymentListTool,
        NewConfigMapListTool,
        // ... tous les list tools ...
    }
    for _, fn := range listConstructors {
        t, err := fn(ctx, configs)
        if err != nil {
            return nil, err
        }
        tools = append(tools, t)
    }

    // Describe tools
    describeConstructors := []func(context.Context, Configs) (tool.InvokableTool, error){
        NewPodDescribeTool,
        NewDeploymentDescribeTool,
        // ... tous les describe tools ...
    }
    // ...

    // Special tools
    podLog, err := NewPodLogTool(ctx, configs)
    if err != nil { return nil, err }
    tools = append(tools, podLog)

    podExec, err := NewPodExecTool(ctx, configs)
    if err != nil { return nil, err }
    tools = append(tools, podExec)

    // Generic tools
    resourceList, err := NewResourceListTool(ctx, configs)
    if err != nil { return nil, err }
    tools = append(tools, resourceList)

    resourceDescribe, err := NewResourceDescribeTool(ctx, configs)
    if err != nil { return nil, err }
    tools = append(tools, resourceDescribe)

    return tools, nil
}

// NewCoreTools creates only the core Kubernetes tools (no CRDs like Kafka, OLM, Spark, OpenShift).
func NewCoreTools(ctx context.Context, configs Configs, scheme *runtime.Scheme) ([]tool.BaseTool, error) {
    // ... subset sans kafka_*, olm_*, ocp_*, spark_* ...
}
```

---

## Phase 4 — Interfaces partagées

### 4.1 — `libs/summarizer/summarizer.go`

```go
package summarizer

import (
    "context"
    "github.com/cloudwego/eino/schema"
)

// Summarizer generates a summary of conversation history.
type Summarizer interface {
    Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)
}

// SummarizerFunc adapts a function to the Summarizer interface.
type SummarizerFunc func(ctx context.Context, history []*schema.Message, previousSummary string) (string, error)

func (f SummarizerFunc) Summarize(ctx context.Context, history []*schema.Message, previousSummary string) (string, error) {
    return f(ctx, history, previousSummary)
}
```

**Migration** :
- `components/middleware/contextopt/summarizer.go` : supprimer la définition locale, importer `libs/summarizer`
- `components/memory/session/session.go` : idem

### 4.2 — `libs/counter/counter.go`

```go
package counter

import "github.com/cloudwego/eino/schema"

// TokenCounter estimates the number of tokens in a set of messages.
type TokenCounter func(msgs []*schema.Message) int

// DefaultTokenCounter uses a ~4 chars/token heuristic.
func DefaultTokenCounter(msgs []*schema.Message) int { ... }
```

**Migration** :
- `components/memory/memory.go` : supprimer `TokenCounter` et `DefaultTokenCounter` locaux, importer `libs/counter`
- `components/middleware/contextopt/optimizer.go` : importer `libs/counter`
- `components/memory/session/session.go` : importer `libs/counter`

### 4.3 — `libs/pricer/pricer.go`

```go
package pricer

// Tokens mirrors the activity.Tokens structure.
type Tokens struct {
    Input     int
    Output    int
    Reasoning int
    Cache     CacheTokens
}

type CacheTokens struct {
    Read  int
    Write int
}

// Pricer computes the cost of a model invocation.
type Pricer interface {
    Cost(model string, tokens Tokens) float64
}
```

**Migration** :
- `callbacks/activity/handler.go` : le `Pricer` interface existant utilise `activity.Tokens`. Soit on garde la compatibilité (adapter), soit on migre vers `pricer.Tokens`.
- `libs/modelsdev/pricer.go` : implémente `pricer.Pricer`.

### 4.4 — Interface commune pour les configs multi-instances

Pas de nouveau package nécessaire — le pattern `Configs` est déjà bien établi. Documenter la convention dans le README.

---

## Phase 5 — Ergonomie de la library (DX)

### 5.1 — READMEs manquants

Créer un README.md pour chaque package sans documentation :

| Package | Contenu du README |
|---------|------------------|
| `components/tool/kubernetes/` | Overview, quick start, config, tool list, examples |
| `components/tool/opensearch/` | Overview, config, example |
| `components/tool/convertor/` | Overview, example |
| `components/memory/file/` | Overview, config, JSONL format, example |
| `libs/contentcomp/jsoncrush/` | Overview, algorithm, example |
| `libs/contentcomp/shellout/` | Overview, patterns, example |

**Format standardisé** :
```markdown
# package-name

One-line description.

## Quick Start

\`\`\`go
// minimal usage example
\`\`\`

## Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|

## Tools (if applicable)

| Tool | Description |
|------|-------------|

## Examples

See `examples/` directory.
```

### 5.2 — Tests manquants

#### `components/tool/convertor/convertor_test.go`
- Test YAML→JSON, JSON→YAML
- Test input invalide
- Test types non supportés
- Test round-trip (YAML→JSON→YAML)

#### `components/tool/opensearch/opensearch_test.go`
- Mock client OpenSearch
- Test construction de requête
- Test `fieldAsString` avec string et []interface{}
- Test paramètres par défaut

### 5.3 — `example_test.go` pour chaque package public

Ajouter des `Example*` functions GoDoc pour :
- `components/tool/argocd/` : `ExampleNewAllTools`
- `components/tool/kubernetes/` : `ExampleNewAllTools`, `ExampleNewCoreTools`
- `components/tool/opensearch/` : `ExampleNewOpensearchLogKubernetesTool`
- `components/tool/convertor/` : `ExampleNewConvertorTool`
- `components/memory/file/` : `ExampleNewFileMemory`
- `components/memory/session/` : déjà documenté
- `components/middleware/contextopt/` : `ExampleNewMiddleware`
- `components/middleware/agentattr/` : `ExampleNew`
- `callbacks/activity/` : `ExampleNewHandler`

### 5.4 — Nommage des tools Kubernetes

**Problème** : incohérence dans les noms de tools.
```
kubernetes_list_pods     (verbe avant resource)
kubernetes_describe_pod  (verbe avant resource, singulier)
kubernetes_pod_exec      (resource avant verbe)
kubernetes_pod_logs      (resource avant verbe)
```

**Correction** : harmoniser en `kubernetes_<resource>_<action>` :
```
kubernetes_pods_list
kubernetes_pods_describe
kubernetes_pods_exec
kubernetes_pods_logs
kubernetes_deployments_list
kubernetes_deployments_describe
// etc.
```

⚠️ **Breaking change** : les LLMs qui ont appris les anciens noms devront s'adapter. Documenter dans un CHANGELOG.

---

## Phase 6 — Qualité du code et nettoyage

### 6.1 — Remplacer `go-funk` par du code natif

**Fichiers** : `argocd/config.go:24`, `kubernetes/config.go:18`
```go
// AVANT
func (c Configs) GetInstanceNames() []string {
    return funk.Keys(c).([]string)
}

// APRÈS
func (c Configs) GetInstanceNames() []string {
    names := make([]string, 0, len(c))
    for name := range c {
        names = append(names, name)
    }
    sort.Strings(names) // déterministe
    return names
}
```

**Bénéfice** : supprime la dépendance `github.com/thoas/go-funk`, résultat déterministe (trié).

### 6.2 — Commentaires GoDoc

Auditer et corriger tous les commentaires exportés :
- Style `// TypeName does X.` (3ème personne, point final)
- Supprimer les commentaires qui décrivent l'évidence
- Corriger les copier-coller incorrects (ex: `Invoke executes the DescribeTool...` dans `convertor`)

### 6.3 — Nommage des types de sortie

Harmoniser les noms de types de sortie :
- `ApplicationListOutput` → OK
- `PodListOutput` → OK
- `DescribeOutput` → OK (générique)
- `ClusterDescribeOutput` → OK (spécifique ArgoCD)

Pas de changement nécessaire ici, le nommage est déjà cohérent.

### 6.4 — Supprimer les `logrus.Debugf` dans les helpers de filtrage

**Fichiers** : `generic_list.go:57`, `resource_list.go:69`
```go
logrus.Debugf("Output %s filtered by regex: %s", string(o), filter.String())
```
**Problème** : log le contenu complet de chaque resource filtrée. Verbeux et potentiellement sensible.
**Correction** : supprimer ou réduire à `logrus.Debugf("Resource filtered out by regex")`.

### 6.5 — Corriger les fautes dans les descriptions de tools

- `pod_exec.go:28` : `RULLES` → `RULES`
- `opensearch_log_kubernetes.go:21` : `permits to retrive` → `retrieves`
- `opensearch_log_kubernetes.go:22` : `It useful` → `It is useful`
- `opensearch_log_kubernetes.go:25` : `Parameters *` → `Parameters **`
- `generic_list.go:157` : `ressource` → `resource` (dans panic message)

---

## Phase 7 — Audit sécurité approfondi

### 7.1 — Matrice des risques

| Composant | Entrée | Risque | Sévérité | Mitigation |
|-----------|--------|--------|----------|------------|
| `pod_exec` | `Command` | Exécution arbitraire | 🔴 Critique | Blocklist + `[]string` (Phase 0.4, 0.5) |
| `pod_log` | `FilterPattern` | ReDoS | 🟢 Faible | RE2 (Go) immunisé |
| `argocd` | `Url` | SSRF | 🟡 Moyen | Valider schéma https:// dans `NewClient` |
| `opensearch` | `LuceneQuery` | Injection query | 🟡 Moyen | `AnalyzeWildcard(true)` déjà actif, documenter |
| `kubernetes` | `LabelsSelector` | Parsing error | 🟢 Faible | `labels.Parse` gère les erreurs |
| `convertor` | `Input` | YAML/JSON bomb | 🟡 Moyen | Limiter taille input |
| `generic_list` | `PaginateToken` | Injection JSON | 🔴 Critique | `json.Marshal` (Phase 0.3) |
| `secret_describe` | Secret data | Fuite credentials | 🟢 Faible | REDACTED ✅ |

### 7.2 — Actions supplémentaires

#### 7.2.1 — Valider l'URL ArgoCD
```go
func NewClient(config Config) (api.API, error) {
    if !strings.HasPrefix(config.Url, "https://") && !strings.HasPrefix(config.Url, "http://") {
        return nil, errors.Errorf("ArgoCD URL must include scheme (https:// or http://): %s", config.Url)
    }
    // ...
}
```

#### 7.2.2 — Limiter la taille d'input du convertor
```go
const maxInputSize = 1024 * 1024 // 1MB

func (t *ConvertorTool) Invoke(ctx context.Context, params *ConvertorParams) (string, error) {
    if len(params.Input) > maxInputSize {
        return "", errors.Errorf("input too large: %d bytes (max %d)", len(params.Input), maxInputSize)
    }
    // ...
}
```

#### 7.2.3 — Timeout sur les opérations OpenSearch
Ajouter un `context.WithTimeout` si aucun timeout n'est présent dans le ctx appelant.

---

## Ordre d'exécution recommandé

```
Phase 0 (P0, 2-3 jours)
  ├── 0.1  context.Background → ctx
  ├── 0.2  messages hardcodés
  ├── 0.3  injection paginateToken
  ├── 0.4  pod_exec command parsing
  ├── 0.5  pod_exec blocklist
  ├── 0.6  panics → errors
  ├── 0.7  pod_exec streaming
  ├── 0.8  supprimer pod_debug.go
  ├── 0.9  double import
  ├── 0.10 opensearch duplication
  ├── 0.11 convertor doc
  └── 0.12 opensearch doc

Phase 1 (P1, 1 jour)
  ├── 1.1 libs/toolkit/filter
  ├── 1.2 libs/toolkit/guidance
  ├── 1.3 libs/toolkit/validate
  ├── 1.4 libs/toolkit/marshal
  └── 1.5 migration helpers existants

Phase 2 (P1, 1 jour)
  ├── 2.1 baseTool ArgoCD
  ├── 2.2 refactor 13 tools
  └── 2.3 registry ArgoCD

Phase 3 (P1, 2 jours)
  ├── 3.1 baseTool Kubernetes
  ├── 3.2-3.4 refactor generics + resource
  ├── 3.5-3.6 refactor pod_exec + pod_log
  ├── 3.7 wrappers (vérification)
  └── 3.8 registry Kubernetes

Phase 4 (P2, 1 jour)
  ├── 4.1 libs/summarizer
  ├── 4.2 libs/counter
  └── 4.3 libs/pricer

Phase 5 (P2, 2 jours)
  ├── 5.1 READMEs manquants
  ├── 5.2 tests manquants
  ├── 5.3 example_test.go
  └── 5.4 nommage tools K8s

Phase 6 (P3, 2 jours)
  ├── 6.1 supprimer go-funk
  ├── 6.2 GoDoc comments
  ├── 6.3-6.4 nettoyage logs
  └── 6.5 fautes descriptions

Phase 7 (P3, 1 jour)
  ├── 7.2.1 valider URL ArgoCD
  ├── 7.2.2 limiter input convertor
  └── 7.2.3 timeout OpenSearch
```

**Total estimé** : ~12-14 jours de développement.

---

## Validation

Après chaque phase :
```bash
go build ./...
go vet ./...
go test ./...
```

Après Phase 0 : vérifier que les tests existants passent toujours (pas de breaking change involontaire).
Après Phase 2-3 : les tests ArgoCD et Kubernetes doivent passer sans modification (refactoring pur).
Après Phase 4 : vérifier que `contextopt` et `session` compilent avec les nouvelles interfaces.
Après Phase 5 : `go test ./...` avec les nouveaux tests.

## Risques

- **Breaking changes** : le renommage des tools K8s (Phase 5.4) et le changement de `Command` en `[]string` (Phase 0.4) sont des breaking changes. Documenter dans un CHANGELOG et communiquer aux consommateurs.
- **Interfaces partagées** (Phase 4) : les packages `contextopt` et `session` devront importer `libs/summarizer` et `libs/counter`. Vérifier l'absence de cycles d'import.
- **Tests envtest** : les tests Kubernetes utilisent `envtest` (vrai API server). S'assurer que l'environnement CI dispose des binaires nécessaires.
