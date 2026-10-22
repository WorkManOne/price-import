// Package api - тут лежит openapi спека, вшивается в бинарник
package api

import _ "embed"

//go:embed openapi.yaml
var Spec []byte
