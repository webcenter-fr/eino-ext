package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"emperror.dev/errors"
	opensearchv4 "github.com/disaster37/opensearch/v4"
)

// DefaultRRFPipelineBody is the phase-results-processor pipeline definition
// used by EnsureRRFPipeline: reciprocal rank fusion combining BM25 and kNN
// sub-query rankings.
const DefaultRRFPipelineBody = `{
  "description": "Post processor for hybrid RRF search",
  "phase_results_processors": [
    {
      "score-ranker-processor": {
        "combination": {
          "technique": "rrf"
        }
      }
    }
  ]
}`

// EnsureRRFPipeline creates the named search pipeline with
// DefaultRRFPipelineBody only if it does not already exist. An existing
// pipeline is never overwritten. Returns (created bool, err error).
//
// A failure to create the pipeline (e.g. cluster/plugin does not support
// score-ranker-processor) is returned as an error so the caller can decide
// whether to treat it as fatal.
func EnsureRRFPipeline(ctx context.Context, client opensearchv4.Client, name string) (created bool, err error) {
	exists, err := searchPipelineExists(ctx, client, name)
	if err != nil {
		return false, errors.Wrap(err, "failed to check existing search pipeline")
	}
	if exists {
		return false, nil
	}

	var body map[string]any
	if err = json.Unmarshal([]byte(DefaultRRFPipelineBody), &body); err != nil {
		return false, errors.Wrap(err, "failed to parse default RRF pipeline body")
	}
	if err = putSearchPipeline(ctx, client, name, body); err != nil {
		return false, errors.Wrap(err, "failed to create RRF search pipeline")
	}
	return true, nil
}

// searchPipelineExists returns true when the named search pipeline exists.
func searchPipelineExists(ctx context.Context, client opensearchv4.Client, name string) (bool, error) {
	rc := client.RestyClient()
	resp, err := rc.R().SetContext(ctx).Get(fmt.Sprintf("/_search/pipeline/%s", name))
	if err != nil {
		return false, errors.Wrap(err, "failed to get search pipeline")
	}
	if resp.StatusCode() == http.StatusNotFound {
		return false, nil
	}
	if resp.IsError() {
		return false, errors.Errorf("unexpected status checking search pipeline %q: %s", name, resp.Status())
	}
	return true, nil
}

// putSearchPipeline creates or updates the named search pipeline.
func putSearchPipeline(ctx context.Context, client opensearchv4.Client, name string, body map[string]any) error {
	rc := client.RestyClient()
	resp, err := rc.R().SetContext(ctx).SetBody(body).Put(fmt.Sprintf("/_search/pipeline/%s", name))
	if err != nil {
		return errors.Wrap(err, "failed to put search pipeline")
	}
	if resp.IsError() {
		return errors.Errorf("failed to create search pipeline %q: %s", name, resp.Status())
	}
	return nil
}
