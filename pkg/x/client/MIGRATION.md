# Replacing the go-swagger REST client

## Objective

Replace the go-swagger-generated Go client (`pkg/client/generated`) -- which
invents phantom empty objects for unset optional fields (#6590) and cannot
faithfully round-trip several wire formats -- with an openapi-generator-based
client, generated from the **same published `swagger.json` every other
consumer uses**. No per-consumer spec derivatives.

## Done

- **Canonical spec fixes** (benefit every client, not just Go): `.swaggo`
  overrides for `Quantity`/`MicroTime`/`IntOrString`, and `fix-swagger-spec.sh`
  Pass 4 making arbitrary-JSON fields typeless. The TS client was regenerated
  from the fixed spec and the UI adapted (9 files).
- **New Go client** at `pkg/x/client/generated` (`x/` = no stability promise;
  generate your own client from the published spec). Produced by
  `hack/codegen/generate-go-client.sh`; deterministic; optional struct fields
  are pointers, so #6590 is fixed by construction.
- **Wire fidelity proven**: the suite in this directory round-trips richly
  populated resources through the new client and requires byte-equality with
  canonical Kargo/k8s serialization. Runs with `make test-unit`.
- **CLI wiring + exemplar**: `pkg/cli/client/client_new.go` (auth/transport,
  token refresh reuse) and `kargo get stages` ported as the pattern for the
  rest.

## Remaining

1. Port the ~46 remaining CLI files from the old client to the new one
   (mechanical; follow the `get stages` exemplar).
2. Delete `pkg/client/generated` and its go-swagger codegen; wire
   `generate-go-client.sh` into `make codegen-openapi`.
3. Eliminate the one remaining spec deviation in generation: the
   first-tag-per-operation transform (grouping metadata only; openapi-generator
   output does not compile with multi-tag operations).
4. Operationalize generation properly. `generate-go-client.sh` is a
   prototype; subtasks include (at least):
   - Integrate it into the Makefile (`codegen-openapi`) and CI, including a
     codegen-freshness check.
   - Use pinned tooling (`hack/bin/jq`, not PATH `jq`) consistent with the
     rest of the codegen pipeline.
   - Decide whether to suppress the generator's standalone-repo scaffolding
     (docs/, CI configs, git_push.sh) at generation time.
   - Drop the `interface{}{}` sed post-fix if a newer generator version or a
     template override obviates it.
