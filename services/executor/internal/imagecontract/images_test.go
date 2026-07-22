package imagecontract

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestImageBase64ValidationNeverUsesDecodeStringAllocation(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("images.go"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "images.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "DecodeString" {
			return true
		}
		t.Fatalf("image contract must stream base64 validation, not DecodeString")
		return false
	})
}
