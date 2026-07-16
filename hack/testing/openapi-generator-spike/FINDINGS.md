# Spike: Go client generation with openapi-generator

**Question:** Can openapi-generator produce a wire-compatible Go client from
the existing (Swagger 2.0) `swagger.json`, fixing the phantom-empty-object
bug (#6590) without regressing anything else?

**Answer: yes — conclusively, and it fixes three additional pre-existing
serialization defects along the way.**

## Verdict details

`fidelity/` contains a round-trip suite: richly-populated resources are
constructed with the canonical types (`api/v1alpha1`, `k8s.io/api`), marshaled
with `encoding/json` (byte-for-byte what the API server sends), unmarshaled
into the generated model, re-marshaled, and required to be JSON-equal to
ground truth.

| Test | Result |
| --- | --- |
| Stage (freight history, verification, conditions, health w/ array output, step config JSON, durations) | lossless |
| Freight (commits/images/charts, per-stage status maps) | lossless |
| Promotion (arbitrary step config + state JSON, timestamps) | lossless |
| Warehouse (custom `WarehouseSpec` marshaler, subscriptions) | lossless |
| ConfigMap / Secret (base64 data) | lossless |
| `IntOrString` wire format (`count: 5` / `"20%"`) | **fixed** (old client: unmarshal error) |
| `Quantity` wire format (`"cpu": "100m"`) | **fixed** (old client: silent corruption to `"cpu":{}`) |
| `V1MicroTime` wire format (Event `eventTime`) | **fixed** (old client: unmarshal error) |

Optional struct fields generate as pointers (`Status *StageStatus`), so the
phantom-empty-object bug of #6590 is fixed by construction — no Pass 5 /
second-spec shim (PR #6612) needed.

## The recipe

1. **Pre-process `swagger.json`** (generator input only; the canonical spec is
   untouched) with jq:
   - Keep only the first tag on each operation (113 ops have multiple tags;
     openapi-generator otherwise generates duplicate request types per tag
     and the output doesn't compile).
   - Replace the `Quantity`, `IntOrString`, and `V1MicroTime` definitions with
     typeless (description-only) schemas. swag reflected these structs,
     ignoring their custom `MarshalJSON`; their real wire formats are scalars.
     Typeless schemas render as `interface{}`, which round-trips anything.
   - Strip `"type": "object"` from bare object schemas (no `properties`, no
     `additionalProperties`). These are exactly the `apiextensions.JSON`
     fields (`Health.output`, `PromotionStep.config`, `PromotionStatus.state`,
     etc.). NOTE: `Health.output` carries a JSON **array** on the wire
     (`pkg/health/aggregating_checker.go`), so without this the generated
     `map[string]interface{}` typing would be a regression vs. the old
     client's `any`.
2. **Generate** with `openapitools/openapi-generator-cli:v7.14.0` (Docker),
   `-g go`, `--type-mappings 'object=interface{}'` (belt-and-suspenders with
   1c: the internal 2.0→3.x conversion re-infers `object` for typeless
   schemas), `--skip-validate-spec`.
3. **Post-fix** a generator template bug: with the `object=interface{}`
   mapping, `*Ok()` getters emit the invalid zero value `interface{}{}`;
   `sed 's/return interface{}{}, false/return nil, false/g'` (6 files).

## Caveats and residual notes

- The `sed` post-fix is a wart. Before productionizing, check whether a newer
  generator version or a small custom template override
  (`--template-dir` overriding just the model template) removes the need.
- Null-vs-absent: fields the server serializes as literal `null` (e.g. a
  zero-valued `metav1.Time` in an always-marshaled position) re-marshal as
  absent. Not exercised by realistic fixtures; display-only cosmetic.
- The generated client (`APIClient`/`Configuration`) accepts an injectable
  `http.Client`, so the CLI's bearer-token/version-header/TLS wiring
  (currently expressed via `go-openapi/runtime`) ports to a custom
  `http.RoundTripper`.
- Not covered by this spike: migrating the ~47 CLI files that consume the old
  client (mechanical, compiler-driven), and pinning/regen-hygiene config
  (suppress version stamps in generated files).

## Part two: CLI slice migration

`kargo get stages` is ported to the new client in-tree (spike state: the root
`go.mod` gains a `require`/`replace` for `github.com/akuity/kargo-spike`):

- `pkg/cli/client/client_v2.go` — the auth/transport design task, answered:
  the generated `Configuration` takes an injectable `http.Client` (reusing the
  existing `versionHeaderTransport` and TLS setup unchanged) and a
  `DefaultHeader` map for the bearer token. Smaller and simpler than the
  `go-openapi/runtime` wiring it replaces; the token-refresh flow is reused
  as-is. `V2APIError` surfaces the response body that the generated
  `GenericOpenAPIError` hides behind bare status text.
- `pkg/cli/cmd/get/stages.go` — `run()` ported. Call-shape change is purely
  mechanical: params-object + `res.Payload` becomes
  `CoreAPI.ListStages(ctx, project).Execute()` returning the model directly.
  The existing model→`kargoapi` JSON round-trip for display is unchanged —
  except it no longer manufactures phantom objects.
- `pkg/cli/client/client_v2_test.go` — hermetic end-to-end proof against an
  `httptest` server: URL construction, bearer + CLI-version headers, and
  byte-exact display fidelity (server JSON == displayed JSON) for a Stage
  with most optional fields unset.

Before/after, same wire bytes (a minimal Stage): the old client's display
payload inflates to a page of phantoms — `"promotionTemplate": {}`,
`"verification": {...}`, an entire invented `"currentPromotion"` tree with
`"origin": {"kind": null, "name": null}` — while the new client's output is
byte-identical to what the server sent (including preserving the server's own
legitimate `"status": {}`).

Lint (`golangci-lint`) and the full `pkg/cli` test suite pass. Migration cost
observed on this slice: ~10 lines in the command, all shape-translation; the
one-time client wiring is ~60 lines total.

## Reproduce

```bash
# requires Docker and jq; regenerates the client from swagger.json,
# builds it, and runs the fidelity suite end to end
hack/testing/openapi-generator-spike/regenerate.sh
```
