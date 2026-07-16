#!/usr/bin/env bash
set -euo pipefail

# Regenerates the spike's Go client from the repo's swagger.json using
# openapi-generator. See FINDINGS.md for what each step is for and why.
#
# Usage (from anywhere): hack/testing/openapi-generator-spike/regenerate.sh

SPIKE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SPIKE_DIR}/../../.." && pwd)"
GENERATOR_IMAGE="openapitools/openapi-generator-cli:v7.14.0"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "${WORK_DIR}"' EXIT

# --- Step 1: pre-process the spec (generator input ONLY; swagger.json is ---
# --- never modified) --------------------------------------------------------
jq '
  # 1a. Keep only the first tag per operation. openapi-generator generates
  #     one API file per tag and duplicates operations with multiple tags,
  #     producing redeclared types that do not compile.
  .paths |= map_values(map_values(
    if (type == "object" and .tags?) then .tags |= [.[0]] else . end
  )) |

  # 1b. Blank the three definitions swag models structurally but whose real
  #     wire formats are scalars (custom MarshalJSON swag cannot see).
  #     Typeless schemas generate as interface{}, which round-trips anything.
  .definitions.Quantity = {
    "description": "Kubernetes resource quantity; serializes as a string (e.g. \"100m\", \"128Mi\")"
  } |
  .definitions.IntOrString = {
    "description": "Serializes as a bare integer or string"
  } |
  .definitions.V1MicroTime = {
    "description": "Serializes as an RFC3339 string with microseconds"
  } |

  # 1c. Strip "type": "object" from bare object schemas (no properties, no
  #     additionalProperties). These are the apiextensions.JSON fields, whose
  #     values may be ANY JSON -- Health.output carries an array on the wire.
  walk(
    if (type == "object" and .type? == "object"
        and (has("properties") | not)
        and (has("additionalProperties") | not))
    then del(.type)
    else . end
  )
' "${REPO_ROOT}/swagger.json" > "${WORK_DIR}/swagger-go-gen.json"

# --- Step 2: generate -------------------------------------------------------
rm -rf "${SPIKE_DIR}/generated"
docker run --rm \
  -v "${SPIKE_DIR}:/spike" \
  -v "${WORK_DIR}:/work" \
  "${GENERATOR_IMAGE}" generate \
  -i /work/swagger-go-gen.json \
  -g go \
  -o /spike/generated \
  --git-user-id akuity --git-repo-id kargo-spike \
  --type-mappings 'object=interface{}' \
  --additional-properties=packageName=kargogen,withGoMod=true,generateInterfaces=false,enumClassPrefix=true \
  --skip-validate-spec

# --- Step 3: post-fix a generator template bug ------------------------------
# With the object=interface{} type mapping, *Ok() getters emit the invalid
# zero value `interface{}{}`.
LC_ALL=C find "${SPIKE_DIR}/generated" -name 'model_*.go' \
  -exec sed -i '' 's/return interface{}{}, false/return nil, false/g' {} +

# --- Step 4: build and verify -----------------------------------------------
(cd "${SPIKE_DIR}/generated" && go mod tidy && go build ./...)
(cd "${SPIKE_DIR}/fidelity" && go mod tidy && go test ./...)

echo "OK: client regenerated, built, and wire fidelity verified."
