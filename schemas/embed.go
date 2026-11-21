package schemas

import _ "embed"

// SkipperSchemaJSON holds the embedded JSON Schema Draft 2020-12 for skipper.yaml.
//
//go:embed skipper.schema.json
var SkipperSchemaJSON []byte
