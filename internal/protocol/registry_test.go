package protocol

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
)

// modelProtocolConstants reads every `X Protocol = "..."` constant from the model
// source, so a protocol added there without a Spec fails here instead of in production.
func modelProtocolConstants(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "../database/model/model.go", nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "Protocol" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok {
					t.Fatalf("Protocol constant with non-literal value %T", v)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, s)
			}
		}
	}
	if len(values) < 10 {
		t.Fatalf("found only %d Protocol constants in model.go: parser out of date?", len(values))
	}
	return values
}

func registeredIDs() []string {
	var ids []string
	for _, s := range All() {
		ids = append(ids, string(s.ID))
	}
	return ids
}

func TestEveryModelProtocolIsRegistered(t *testing.T) {
	want := modelProtocolConstants(t)
	got := registeredIDs()
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("registered protocols %v, model.Protocol constants %v", got, want)
	}
}

// The inbound validator's oneof list is a second copy of the protocol set; an id missing
// there is refused on save, an extra one saves an inbound nothing can serve.
func TestInboundValidatorAcceptsExactlyTheRegisteredProtocols(t *testing.T) {
	field, ok := reflect.TypeFor[model.Inbound]().FieldByName("Protocol")
	if !ok {
		t.Fatal("model.Inbound has no Protocol field")
	}
	var accepted []string
	for rule := range strings.SplitSeq(field.Tag.Get("validate"), ",") {
		if list, ok := strings.CutPrefix(rule, "oneof="); ok {
			accepted = strings.Fields(list)
		}
	}
	got := registeredIDs()
	slices.Sort(accepted)
	slices.Sort(got)
	if !slices.Equal(accepted, got) {
		t.Fatalf("validator oneof %v, registered %v", accepted, got)
	}
}
