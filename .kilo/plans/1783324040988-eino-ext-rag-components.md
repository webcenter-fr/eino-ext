# PLAN A — Librairie `webcenter-fr/eino-ext` : ajout de la famille RAG « document / retriever / indexer »

> Plan jumeau : `1783324040988-project-rag-components-adoption.md` (adoption côté projet `rancher-doc-chat-api-k8s`). **Implémenter CE plan en premier**, publier la lib, puis exécuter le plan jumeau.

## 0. Contexte et périmètre

La lib `github.com/webcenter-fr/eino-ext` est un **module Go unique** (un seul `go.mod` à la racine). Elle contient déjà `callbacks/`, `components/{memory,middleware,model,prompt,tool}`, `libs/`. **Il manque toute la famille `components/document/*`, `components/retriever/*`, `components/indexer/*` et un `libs/docid`.** Le projet `rancher-doc-chat-api-k8s` réimplémente localement ces composants ; ce plan les extrait dans la lib.

### Composants à AJOUTER (7 nouveaux packages)

| # | Nouveau package lib | Source projet extraite |
|---|---|---|
| A2 | `libs/docid` | `pkg/indexer/id/id.go` |
| A3 | `components/document/transformer/splitter/sizecap` | `pkg/transformer/sizesplitter.go` |
| A4 | `components/document/loader/github` | `pkg/loader/github.go` |
| A5 | `components/document/parser/opensearch` | `pkg/parser/opensearch/opensearch.go` |
| A6 | `components/document/loader/opensearch` | `pkg/loader/opensearch/opensearch.go` |
| A7 | `components/retriever/opensearch` | `pkg/retriever/retriever.go` |
| A8 | `components/indexer/opensearch` | `pkg/indexer/reconcile.go` + `ensureFieldMappings` de `pkg/indexer/opensearch.go` |

### Hors périmètre (restent locaux au projet)
`pkg/lambda/*` (contextual, uuid, cleaner, document), `pkg/transformer/markdown.go`, `pkg/embedding/*`, `pkg/tools/*`, `pkg/indexer/opensearch.go` (constantes de champ + `DocumentToFields` = schéma métier). Ils seront seulement recâblés vers la lib dans le plan jumeau.

### Règles d'implémentation (à respecter pour chaque package)
- Package Go = dernier segment du chemin (ex : `.../splitter/sizecap` → `package sizecap`).
- Constructeur : `New...(ctx context.Context, config *Config) (..., error)`.
- Conserver `GetType()` et `IsCallbacksEnabled()` là où ils existent déjà.
- En-tête de licence Apache 2.0 comme dans les fichiers projet (bloc de commentaire en tête).
- Écrire chaque fichier avec le contenu EXACT fourni ci-dessous. Ne pas improviser d'autres API.

### Pré-requis de setup
1. Cloner le dépôt de la lib `github.com/webcenter-fr/eino-ext` dans un répertoire de travail (ex : `/tmp/kilo/eino-ext`). **NE PAS éditer le cache de modules Go** (`$GOMODCACHE`, lecture seule).
2. Travailler sur une branche dédiée (ex : `feat/rag-document-components`).
3. Toutes les créations de fichiers ci-dessous sont relatives à la racine du dépôt de la lib.

---

## A1. Mise à jour de `go.mod` (racine de la lib)

Ajouter ces dépendances (elles seront résolues par `go mod tidy` à la fin) :

```
github.com/cloudwego/eino-ext/components/retriever/opensearch3
github.com/cloudwego/eino-ext/components/document/loader/url
github.com/opensearch-project/opensearch-go/v4
github.com/disaster37/opensearch/v4
github.com/elastic/go-ucfg
github.com/google/uuid
github.com/zeebo/xxh3
```

Commande (à exécuter à la racine de la lib, après avoir créé tous les fichiers) :
```
go get github.com/cloudwego/eino-ext/components/retriever/opensearch3@v0.0.0-20260702024331-c05d17c7dace
go get github.com/cloudwego/eino-ext/components/document/loader/url@v0.0.0-20260702024331-c05d17c7dace
go get github.com/opensearch-project/opensearch-go/v4@v4.6.0
go get github.com/disaster37/opensearch/v4@v4.0.0-7
go get github.com/elastic/go-ucfg@v0.9.1
go get github.com/google/uuid@v1.6.1-0.20241114170450-2d3c2a9cc518
go get github.com/zeebo/xxh3@v1.1.0
go mod tidy
```
`github.com/disaster37/opensearch/v3` et `go.yaml.in/yaml/v3` sont déjà des dépendances directes de la lib — ne rien ajouter pour elles.

---

## A2. Créer `libs/docid/docid.go`

Copie exacte de `pkg/indexer/id/id.go`, package renommé en `docid`.

```go
// Package docid provides shared helpers to compute deterministic document
// base IDs and content hashes, usable both inside indexing graphs (as a
// lambda) and outside of them (e.g. in pre-checks) so the two never diverge.
package docid

import (
	"encoding/hex"

	"emperror.dev/errors"
	"github.com/google/uuid"
	"github.com/zeebo/xxh3"
)

// ComputeBaseID computes a deterministic UUID (derived from a 128 bit xxh3
// hash) from a stable identifier (e.g. a repo-relative path or a source URL).
// The same input always produces the same output, required to keep upserts
// landing on stable document IDs across runs.
func ComputeBaseID(identifier string) (string, error) {
	hash := xxh3.HashString128(identifier).Bytes()
	base, err := uuid.FromBytes(hash[:])
	if err != nil {
		return "", errors.Wrap(err, "failed to generate UUID from identifier")
	}
	return base.String(), nil
}

// ComputeContentHash computes a deterministic content hash (hex encoded
// 128 bit xxh3) from raw bytes. Used to decide whether a source has changed.
func ComputeContentHash(content []byte) string {
	hash := xxh3.Hash128(content).Bytes()
	return hex.EncodeToString(hash[:])
}
```

Créer aussi `libs/docid/docid_test.go` (adapter `pkg/indexer/id/id_test.go` : mêmes tests, `package docid`, appels `ComputeBaseID`/`ComputeContentHash` inchangés). Copier le contenu de `pkg/indexer/id/id_test.go` en changeant uniquement la ligne `package id` → `package docid`.

---

## A3. Créer `components/document/transformer/splitter/sizecap/sizecap.go`

Copie de `pkg/transformer/sizesplitter.go`, package `sizecap`, API publique renommée : `Config` (au lieu de `SizeSplitterConfig`), `NewSplitter` (au lieu de `NewSizeSplitter`). Le corps interne (`sizeSplitter`, `splitContent`, `splitParagraphs`, `hardSplit`, `copyMeta`, constantes) est IDENTIQUE.

Changements exacts à appliquer au copier :
1. `package transformer` → `package sizecap`
2. `type SizeSplitterConfig struct` → `type Config struct`
3. `func NewSizeSplitter(ctx context.Context, config *SizeSplitterConfig)` → `func NewSplitter(ctx context.Context, config *Config)`
4. À l'intérieur de `NewSplitter`, `config = &SizeSplitterConfig{}` → `config = &Config{}`
5. Tout le reste inchangé (y compris `GetType()` retournant `"SizeCapSplitter"`).

Ajouter en tête le bloc de licence Apache 2.0.

Créer `components/document/transformer/splitter/sizecap/sizecap_test.go` : un test minimal montant un `NewSplitter` et vérifiant : (a) contenu court non découpé, (b) contenu long découpé en ≥2 chunks, (c) contenu UTF-8 accentué non coupé en milieu de rune (utiliser `utf8.ValidString` sur chaque chunk).

---

## A4. Créer `components/document/loader/github/github.go`

Copie de `pkg/loader/github.go`, package `github`, en pointant vers l'import public du url-loader.

```go
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	urlloader "github.com/cloudwego/eino-ext/components/document/loader/url"
	"github.com/cloudwego/eino/components/document"
)

// NewGithubLoader creates a GitHub document loader. When token is empty it
// behaves as a plain URL loader; otherwise it adds a Bearer Authorization
// header on every request.
func NewGithubLoader(ctx context.Context, token string) (document.Loader, error) {
	if token == "" {
		return urlloader.NewLoader(ctx, &urlloader.LoaderConfig{})
	}
	return urlloader.NewLoader(ctx, &urlloader.LoaderConfig{
		RequestBuilder: func(ctx context.Context, source document.Source, opts ...document.LoaderOption) (*http.Request, error) {
			u, err := url.Parse(source.URI)
			if err != nil {
				return nil, err
			}
			req := &http.Request{Method: "GET", URL: u, Header: make(http.Header)}
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", token))
			return req, nil
		},
	})
}
```

---

## A5. Créer `components/document/parser/opensearch/opensearch.go`

Basé sur `pkg/parser/opensearch/opensearch.go`. **Découplage** : remplacer les imports projet `indexerpkg` et `fileid` par des champs de `Config` (avec défauts) et par `libs/docid`.

Changements exacts vs le fichier source :
1. `package opensearch` (inchangé).
2. Supprimer les imports :
   - `indexerpkg "github.com/hm-it/rancher-doc-chat-api-k8s/pkg/indexer"`
   - `fileid "github.com/hm-it/rancher-doc-chat-api-k8s/pkg/indexer/id"`
   Ajouter : `"github.com/webcenter-fr/eino-ext/libs/docid"`.
3. Ajouter deux constantes de défaut et deux champs à `Config` :

```go
const (
	MetaKeyId    = "id"
	MetaKeyIndex = "index"
	MetaScore    = "score"
	MetaVersion  = "version"

	// DefaultSourceIDField / DefaultSourceHashField are the metadata keys
	// under which the source record id and the content hash are written when
	// Config.SourceIDField / Config.SourceHashField are left empty.
	DefaultSourceIDField   = "source_id"
	DefaultSourceHashField = "source_hash"
)

type Config struct {
	// content selector of goquery-like field selection.
	FieldSelectors []string
	// fieldIgnores is the list of fields to ignore when parsing the document.
	FieldIgnores []string
	// SourceIDField is the metadata key receiving the source record `_id`.
	// Defaults to DefaultSourceIDField ("source_id").
	SourceIDField string
	// SourceHashField is the metadata key receiving the content hash.
	// Defaults to DefaultSourceHashField ("source_hash").
	SourceHashField string
}
```

4. Dans `NewParser`, après `conf = &Config{}`, appliquer les défauts :

```go
func NewParser(ctx context.Context, conf *Config) (*Parser, error) {
	if conf == nil {
		conf = &Config{}
	}
	if conf.SourceIDField == "" {
		conf.SourceIDField = DefaultSourceIDField
	}
	if conf.SourceHashField == "" {
		conf.SourceHashField = DefaultSourceHashField
	}
	return &Parser{conf: conf}, nil
}
```

5. Dans `Parse`, remplacer les deux lignes couplées :
   - `meta[indexerpkg.MetaKeySourceID] = hit.Id` → `meta[p.conf.SourceIDField] = hit.Id`
   - `meta[indexerpkg.MetaKeySourceHash] = fileid.ComputeContentHash(b)` → `meta[p.conf.SourceHashField] = docid.ComputeContentHash(b)`

Tout le reste du fichier (logique ucfg, yaml marshal, `getMetaData`, `var _ parser.Parser = (*Parser)(nil)`) est IDENTIQUE.

---

## A6. Créer `components/document/loader/opensearch/opensearch.go`

Copie de `pkg/loader/opensearch/opensearch.go`. Seul changement : l'import du parser par défaut pointe vers le nouveau package lib.

Changements exacts :
1. `package loader` (inchangé).
2. Remplacer l'import `opensearchparser "github.com/hm-it/rancher-doc-chat-api-k8s/pkg/parser/opensearch"` par `opensearchparser "github.com/webcenter-fr/eino-ext/components/document/parser/opensearch"`.
3. Tout le reste identique (fonctions `NewOpensearchLoader`, `Load`, `GetType`→`"OpensearchLoader"`, `IsCallbacksEnabled`, `uriToIndexAndQuery`, usage de `opensearchparser.NewParser` et `opensearchparser.MetaKeyId`).

---

## A7. Créer `components/retriever/opensearch/retriever.go`

Copie de `pkg/retriever/retriever.go`. Aucun couplage projet à casser (le fichier n'importe que des packages publics). Seul changement : `package opensearch`.

Changements exacts :
1. `package retriever` → `package opensearch`.
2. Corps IDENTIQUE : `RetrieverConfig` (enrobe `opensearch3.RetrieverConfig` + champ `SearchPipeline`), `Retriever`, `NewRetriever`, `Retrieve`, `parseSearchResult`, `searchHitToMap`, `GetType()`→`"OpenSearch3"`, `IsCallbacksEnabled`, `defaultResultParser`.
3. Conserver les constantes `defaultTopK = 10` et `typ = "OpenSearch3"`.

Note : `defaultResultParser` lit le champ `content` en dur ; c'est le comportement par défaut documenté (les appelants fournissent un `ResultParser` custom pour un autre champ, ce que fait le projet). Ne pas modifier.

---

## A8. Créer `components/indexer/opensearch/` (2 fichiers)

### A8.1 `components/indexer/opensearch/mappings.go`

Extraction générique de `ensureFieldMappings` (issu de `pkg/indexer/opensearch.go`), rendue paramétrable (les propriétés de mapping sont passées par l'appelant).

```go
package opensearch

import (
	"bytes"
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// EnsureMappings merges the given field properties into an already-existing
// index via `PUT _mapping`. It is a no-op when the index does not exist yet
// (the caller is expected to create it with its full mapping elsewhere), and
// a safe, idempotent merge otherwise: the OpenSearch put-mapping API only adds
// new fields / sub-fields, it never removes or changes the type of existing
// ones.
//
// `properties` is the value of the mapping "properties" object, e.g.:
//
//	map[string]any{
//	    "source_id":   map[string]any{"type": "keyword"},
//	    "source_hash": map[string]any{"type": "keyword"},
//	}
func EnsureMappings(ctx context.Context, client *opensearchapi.Client, index string, properties map[string]any) error {
	res, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{index}})
	if err != nil {
		// The v4 SDK returns an error even on a 404 (index not found); treat
		// that as "nothing to retrofit yet".
		if res != nil && res.StatusCode == 404 {
			return nil
		}
		return errors.Wrap(err, "failed to check index existence")
	}
	if res.Body != nil {
		defer res.Body.Close()
	}
	if res.StatusCode == 404 {
		return nil
	}

	body, err := json.Marshal(map[string]any{"properties": properties})
	if err != nil {
		return errors.Wrap(err, "failed to marshal mapping update")
	}

	if _, err = client.Indices.Mapping.Put(ctx, opensearchapi.MappingPutReq{
		Indices: []string{index},
		Body:    bytes.NewReader(body),
	}); err != nil {
		return errors.Wrap(err, "failed to update index mapping")
	}
	return nil
}
```

### A8.2 `components/indexer/opensearch/reconcile.go`

Basé sur `pkg/indexer/reconcile.go`, **rendu générique** : les noms de champ `source_id` / `source_hash` deviennent configurables via des `Option` fonctionnelles (défauts identiques aux valeurs projet, donc les appelants existants n'ont rien à changer d'autre que l'import). Le décodage des hits se fait via `map[string]any` (au lieu d'un struct à tags json figés) pour supporter des noms de champ custom.

```go
package opensearch

import (
	"context"
	"encoding/json"

	"emperror.dev/errors"
	"github.com/disaster37/opensearch/v4"
	"github.com/disaster37/opensearch/v4/api"
	"github.com/disaster37/opensearch/v4/querydsl"
)

const (
	DefaultSourceIDField   = "source_id"
	DefaultSourceHashField = "source_hash"

	reconcileScrollBatchSize = 1000
	reconcileDeleteBatchSize = 500
	reconcileScrollKeepAlive = "2m"
)

// options holds the resolved field names used by the reconcile helpers.
type options struct {
	sourceIDField   string
	sourceHashField string
}

// Option customizes the reconcile helpers.
type Option func(*options)

// WithSourceIDField overrides the field holding the source id (default
// "source_id").
func WithSourceIDField(field string) Option {
	return func(o *options) { o.sourceIDField = field }
}

// WithSourceHashField overrides the field holding the content hash (default
// "source_hash").
func WithSourceHashField(field string) Option {
	return func(o *options) { o.sourceHashField = field }
}

func newOptions(opts ...Option) options {
	o := options{sourceIDField: DefaultSourceIDField, sourceHashField: DefaultSourceHashField}
	for _, f := range opts {
		f(&o)
	}
	return o
}

// ReconcileFilter optionally scopes both the scan of existing source ids and
// the deletion of missing ones (e.g. to a single value of a partition field).
type ReconcileFilter struct {
	Field string
	Value string
}

// LookupSourceHash returns the current source hash stored for a given source
// id. found is false if no document exists yet for that source id.
func LookupSourceHash(ctx context.Context, client opensearch.Client, index, sourceID string, opts ...Option) (hash string, found bool, err error) {
	o := newOptions(opts...)
	termQuery, err := querydsl.NewTermQuery(o.sourceIDField, sourceID).Source()
	if err != nil {
		return "", false, errors.Wrap(err, "failed to build source_id term query")
	}
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   termQuery,
			"size":    1,
			"_source": []string{o.sourceHashField},
		},
	})
	if err != nil {
		return "", false, errors.Wrap(err, "failed to search for existing source_id")
	}
	if result.Hits == nil || len(result.Hits.Hits) == 0 {
		return "", false, nil
	}
	src := map[string]any{}
	if err = json.Unmarshal(result.Hits.Hits[0].Source, &src); err != nil {
		return "", false, errors.Wrap(err, "failed to decode existing source hit")
	}
	hash, _ = src[o.sourceHashField].(string)
	return hash, true, nil
}

// BulkLookupSourceHashes scrolls every existing source id -> hash pair in the
// index (optionally scoped by filter) and returns them as a map.
func BulkLookupSourceHashes(ctx context.Context, client opensearch.Client, index string, filter *ReconcileFilter, opts ...Option) (map[string]string, error) {
	o := newOptions(opts...)
	query, err := reconcileQuery(filter)
	if err != nil {
		return nil, err
	}
	hashes := make(map[string]string)
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   query,
			"size":    reconcileScrollBatchSize,
			"_source": []string{o.sourceIDField, o.sourceHashField},
		},
		Params: &api.SearchParams{Scroll: reconcileScrollKeepAlive},
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to start bulk source_hash scroll")
	}
	scrollID := result.ScrollId
	defer func() {
		if scrollID != "" {
			_, _ = client.Search().ClearScroll(ctx, []string{scrollID})
		}
	}()
	for {
		if result.Hits == nil || len(result.Hits.Hits) == 0 {
			break
		}
		for _, h := range result.Hits.Hits {
			src := map[string]any{}
			if unmarshalErr := json.Unmarshal(h.Source, &src); unmarshalErr != nil {
				return hashes, errors.Wrap(unmarshalErr, "failed to decode source hit during bulk lookup")
			}
			sid, _ := src[o.sourceIDField].(string)
			if sid == "" {
				continue
			}
			shash, _ := src[o.sourceHashField].(string)
			hashes[sid] = shash
		}
		scrollID = result.ScrollId
		result, err = client.Search().Scroll(ctx, &api.ScrollRequest{ScrollId: scrollID, KeepAlive: reconcileScrollKeepAlive})
		if err != nil {
			return hashes, errors.Wrap(err, "failed to continue bulk source_hash scroll")
		}
		scrollID = result.ScrollId
	}
	return hashes, nil
}

// DeleteBySourceID deletes every document whose source id matches the value.
func DeleteBySourceID(ctx context.Context, client opensearch.Client, index, sourceID string, opts ...Option) error {
	return DeleteBySourceIDs(ctx, client, index, []string{sourceID}, opts...)
}

// DeleteBySourceIDs deletes every document whose source id is in the list.
func DeleteBySourceIDs(ctx context.Context, client opensearch.Client, index string, sourceIDs []string, opts ...Option) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	o := newOptions(opts...)
	values := make([]any, 0, len(sourceIDs))
	for _, id := range sourceIDs {
		values = append(values, id)
	}
	query, err := querydsl.NewTermsQuery(o.sourceIDField, values...).Source()
	if err != nil {
		return errors.Wrap(err, "failed to build source_id terms query")
	}
	resp, err := client.Document().DeleteByQuery(ctx, []string{index}, map[string]any{"query": query})
	if err != nil {
		return errors.Wrap(err, "failed to delete documents by source_id")
	}
	if len(resp.Failures) > 0 {
		return errors.Errorf("delete by source_id completed with failures: %+v", resp.Failures)
	}
	return nil
}

// Reconcile scrolls every existing source id (optionally scoped by filter) and
// deletes any that is not present in `seen`, in batches. Returns the number of
// deleted source ids.
func Reconcile(ctx context.Context, client opensearch.Client, index string, seen map[string]bool, filter *ReconcileFilter, opts ...Option) (deleted int, err error) {
	o := newOptions(opts...)
	query, err := reconcileQuery(filter)
	if err != nil {
		return 0, err
	}
	result, err := client.Search().Search(ctx, &api.SearchRequest{
		Indices: []string{index},
		Body: map[string]any{
			"query":   query,
			"size":    reconcileScrollBatchSize,
			"_source": []string{o.sourceIDField},
		},
		Params: &api.SearchParams{Scroll: reconcileScrollKeepAlive},
	})
	if err != nil {
		return 0, errors.Wrap(err, "failed to start reconciliation scroll")
	}
	var toDelete []string
	scrollID := result.ScrollId
	defer func() {
		if scrollID != "" {
			_, _ = client.Search().ClearScroll(ctx, []string{scrollID})
		}
	}()
	for {
		if result.Hits == nil || len(result.Hits.Hits) == 0 {
			break
		}
		for _, h := range result.Hits.Hits {
			src := map[string]any{}
			if unmarshalErr := json.Unmarshal(h.Source, &src); unmarshalErr != nil {
				return deleted, errors.Wrap(unmarshalErr, "failed to decode source hit during reconciliation")
			}
			sid, _ := src[o.sourceIDField].(string)
			if sid == "" || seen[sid] {
				continue
			}
			toDelete = append(toDelete, sid)
		}
		scrollID = result.ScrollId
		result, err = client.Search().Scroll(ctx, &api.ScrollRequest{ScrollId: scrollID, KeepAlive: reconcileScrollKeepAlive})
		if err != nil {
			return deleted, errors.Wrap(err, "failed to continue reconciliation scroll")
		}
		scrollID = result.ScrollId
	}
	for i := 0; i < len(toDelete); i += reconcileDeleteBatchSize {
		end := i + reconcileDeleteBatchSize
		if end > len(toDelete) {
			end = len(toDelete)
		}
		if delErr := DeleteBySourceIDs(ctx, client, index, toDelete[i:end], opts...); delErr != nil {
			return deleted, delErr
		}
		deleted += end - i
	}
	return deleted, nil
}

func reconcileQuery(filter *ReconcileFilter) (any, error) {
	if filter == nil || filter.Field == "" {
		matchAll, err := querydsl.NewMatchAllQuery().Source()
		if err != nil {
			return nil, errors.Wrap(err, "failed to build match_all query")
		}
		return matchAll, nil
	}
	boolQuery, err := querydsl.NewBoolQuery().Filter(querydsl.NewTermQuery(filter.Field, filter.Value)).Source()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build reconciliation bool query")
	}
	return boolQuery, nil
}
```

---

## A9. README de découvrabilité (facultatif mais recommandé)

Ajouter un `README.md` court dans chaque nouveau package (2-4 lignes : rôle + snippet du constructeur). Suivre le style des README existants de la lib s'il y en a. Non bloquant pour la compilation.

---

## A10. Validation de la lib

Exécuter à la racine de la lib :
```
go build ./...
go test ./components/document/... ./components/retriever/... ./components/indexer/... ./libs/docid/...
go vet ./...
```
Tout doit passer. Puis committer, pousser la branche, et **noter le commit / la pseudo-version** publiée : elle est nécessaire au plan jumeau (`go get github.com/webcenter-fr/eino-ext@<commit>`).

---

## A11. Ordre d'implémentation (lots indépendants)

1. **Lot 1** : A2 (`docid`) + A3 (`sizecap`) — zéro couplage, aucun risque.
2. **Lot 2** : A8 (`indexer/opensearch` : mappings + reconcile).
3. **Lot 3** : A7 (`retriever/opensearch`).
4. **Lot 4** : A5 (`parser/opensearch`) puis A6 (`loader/opensearch`, dépend de A5).
5. **Lot 5** : A4 (`loader/github`).
6. A1 (`go.mod` / `go mod tidy`) + A10 après chaque lot ou à la fin.

Chaque lot compile indépendamment ; A6 doit être fait après A5.

---

## Risques / points d'attention
- **Noms de champ configurables** : les valeurs par défaut (`source_id`/`source_hash`) DOIVENT rester `"source_id"`/`"source_hash"` pour que le projet fonctionne sans passer d'options. Ne pas changer ces défauts.
- **`disaster37/opensearch/v4`** : version `v4.0.0-7` (pseudo-tag) — utiliser exactement celle du projet pour éviter un skew d'API (`api.SearchRequest`, `querydsl`).
- **Décodage `map[string]any`** dans reconcile : ne pas revenir à un struct à tags json figés, sinon les noms de champ custom cassent silencieusement.
- La lib étant mono-module, **aucun `go.mod` par package** à créer.
