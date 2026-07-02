// Package modelsdev — snapshot refresh.
//
// Run `go generate ./libs/modelsdev` (or `make models-dev-refresh`) to
// re-download the models.dev catalog and overwrite the committed api.json
// snapshot embedded by catalog.go. Review the diff before committing: this
// keeps builds reproducible while allowing a deliberate, reviewed refresh.
package modelsdev

//go:generate go run ./gen -out api.json
