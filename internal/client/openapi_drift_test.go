package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"testing"
)

// The monorepo's /v1 spec. The public mirror (culipulse/terraform-provider-culipulse) ships
// terraform-provider/ without it, so every test here skips when it's absent.
const openAPISpecPath = "../../../openapi/culipulse.json"

type jsonSchema struct {
	Ref        string                 `json:"$ref"`
	AllOf      []*jsonSchema          `json:"allOf"`
	Properties map[string]*jsonSchema `json:"properties"`
	Items      *jsonSchema            `json:"items"`
}

type openAPIDoc struct {
	Components struct {
		Schemas map[string]*jsonSchema `json:"schemas"`
	} `json:"components"`
}

func loadOpenAPISpec(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(openAPISpecPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s not found (public mirror): the /v1 drift guard only runs in the monorepo", openAPISpecPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestOpenAPIDrift fails when a /v1 schema the provider consumes gains, loses or renames a field
// that openapi_fields_test.go hasn't classified.
func TestOpenAPIDrift(t *testing.T) {
	problems, err := openAPIDriftProblems(loadOpenAPISpec(t), openAPIFields)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// Each case edits the real spec the way a /v1 change would and must be caught.
func TestOpenAPIDriftDetectsChanges(t *testing.T) {
	cases := []struct {
		name string
		edit func(doc map[string]any)
		want string
	}{
		{
			name: "new check_spec field",
			edit: func(doc map[string]any) {
				props(doc, "CheckSpec", "request")["followUpUrl"] = map[string]any{"type": "string"}
			},
			want: "new /v1 field CheckSpec.request.followUpUrl",
		},
		{
			name: "new top-level check_spec key",
			edit: func(doc map[string]any) { props(doc, "CheckSpec")["retries"] = map[string]any{"type": "object"} },
			want: "new /v1 field CheckSpec.retries",
		},
		{
			name: "new ChannelPatch field",
			edit: func(doc map[string]any) { props(doc, "ChannelPatch")["mute_until"] = map[string]any{"type": "integer"} },
			want: "new /v1 field ChannelPatch.mute_until",
		},
		{
			name: "removed field",
			edit: func(doc map[string]any) { delete(props(doc, "MonitorPatch"), "down_min_sources") },
			want: "MonitorPatch.down_min_sources is classified in openapi_fields_test.go but is gone from openapi/culipulse.json",
		},
		{
			// A refactor that composes a schema from a shared base must not hide the base's fields.
			name: "new field on a base schema pulled in through allOf",
			edit: func(doc map[string]any) {
				s := doc["components"].(map[string]any)["schemas"].(map[string]any)
				patch := s["ChannelPatch"].(map[string]any)
				base := map[string]any{"type": "object", "properties": map[string]any{
					"name": map[string]any{"type": "string"}, "enabled": map[string]any{"type": "boolean"},
					"mute_until": map[string]any{"type": "integer"},
				}}
				own := patch["properties"].(map[string]any)
				delete(own, "name")
				delete(own, "enabled")
				s["ChannelPatchBase"] = base
				s["ChannelPatch"] = map[string]any{"allOf": []any{
					map[string]any{"$ref": "#/components/schemas/ChannelPatchBase"},
					map[string]any{"type": "object", "properties": own},
				}}
			},
			want: "new /v1 field ChannelPatch.mute_until",
		},
		{
			name: "renamed schema",
			edit: func(doc map[string]any) {
				s := doc["components"].(map[string]any)["schemas"].(map[string]any)
				s["WebhookChannelPatch"] = s["ChannelPatch"]
				delete(s, "ChannelPatch")
			},
			want: "schema ChannelPatch is gone from openapi/culipulse.json",
		},
	}
	raw := loadOpenAPISpec(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			tc.edit(doc)
			edited, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			problems, err := openAPIDriftProblems(edited, openAPIFields)
			if err != nil {
				t.Fatal(err)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.want) {
				t.Fatalf("want exactly one problem containing %q, got %q", tc.want, problems)
			}
		})
	}
}

// props returns the properties map of a component schema, or of a nested property path in it.
func props(doc map[string]any, schema string, path ...string) map[string]any {
	s := doc["components"].(map[string]any)["schemas"].(map[string]any)[schema].(map[string]any)
	for _, p := range path {
		s = s["properties"].(map[string]any)[p].(map[string]any)
	}
	return s["properties"].(map[string]any)
}

// openAPIDriftProblems walks every schema named in fields (the part before the first dot) and
// returns one message per unclassified or vanished field.
func openAPIDriftProblems(raw []byte, fields map[string]openAPIField) ([]string, error) {
	var doc openAPIDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi/culipulse.json: %w", err)
	}
	roots := map[string]bool{}
	for key := range fields {
		roots[strings.SplitN(key, ".", 2)[0]] = true
	}
	var problems []string
	seen := map[string]bool{}
	var walk func(path string, s *jsonSchema)
	walk = func(path string, s *jsonSchema) {
		for name, child := range properties(s, doc.Components.Schemas) {
			key := path + "." + name
			seen[key] = true
			f, ok := fields[key]
			if !ok {
				problems = append(problems, fmt.Sprintf("new /v1 field %s: model it in the provider (schema + mapping + tests + "+
					"`go tool tfplugindocs generate` + a terraform-provider/CHANGELOG.md entry, shipped in the next "+
					"terraform-provider/vX.Y.Z release) or mark it ignored with a reason in openapi_fields_test.go", key))
				continue
			}
			if f.ignored != "" {
				continue
			}
			if child.Items != nil { // arrays: classify the element's fields under the array's own key
				child = child.Items
			}
			if ref := refName(child); ref != "" {
				if !roots[ref] {
					problems = append(problems, fmt.Sprintf("%s refers to schema %s, which openapi_fields_test.go doesn't cover: add its fields", key, ref))
				}
				continue // checked as its own root
			}
			walk(key, child)
		}
	}
	for root := range roots {
		s, ok := doc.Components.Schemas[root]
		if !ok {
			problems = append(problems, fmt.Sprintf("schema %s is gone from openapi/culipulse.json: renamed or removed on /v1. "+
				"Update openapi_fields_test.go, and check the provider still decodes what the API now returns", root))
			continue
		}
		walk(root, s)
	}
	for key := range fields {
		root := strings.SplitN(key, ".", 2)[0]
		if _, ok := doc.Components.Schemas[root]; ok && !seen[key] {
			problems = append(problems, fmt.Sprintf("%s is classified in openapi_fields_test.go but is gone from openapi/culipulse.json: "+
				"removed or renamed on /v1. That breaks released provider versions that still send or read it: keep it on the "+
				"server, or drop it from the provider (with a terraform-provider/CHANGELOG.md entry) and from the manifest", key))
		}
	}
	sort.Strings(problems)
	return problems, nil
}

// properties merges a schema's own properties with those of its allOf members. A $ref member is
// expanded, so a schema composed from a shared base still has every field classified under its
// own name (a property that is itself a $ref is different: see refName).
func properties(s *jsonSchema, schemas map[string]*jsonSchema) map[string]*jsonSchema {
	out := map[string]*jsonSchema{}
	for name, p := range s.Properties {
		out[name] = p
	}
	for _, part := range s.AllOf {
		if ref := strings.TrimPrefix(part.Ref, "#/components/schemas/"); ref != "" {
			if base, ok := schemas[ref]; ok {
				part = base
			}
		}
		for name, p := range properties(part, schemas) {
			out[name] = p
		}
	}
	return out
}

// refName returns the component a schema points at, directly or as the single $ref of an allOf
// (zod-openapi wraps a $ref in allOf to attach a description).
func refName(s *jsonSchema) string {
	ref := s.Ref
	for _, part := range s.AllOf {
		if part.Ref != "" {
			ref = part.Ref
		}
	}
	return strings.TrimPrefix(ref, "#/components/schemas/")
}
