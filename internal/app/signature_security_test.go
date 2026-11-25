package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRazorpaySignatureVerificationUsesConstantTimeComparison(t *testing.T) {
	if !secureHMACEqual([]byte("same"), []byte("same")) || secureHMACEqual([]byte("same"), []byte("different")) {
		t.Fatal("constant-time comparison returned an invalid equality result")
	}

	expectedCalls := map[string]string{
		"signature_security.go":    "Equal",
		"razorpay_webhooks.go":     "secureHMACEqual",
		"razorpay_confirmation.go": "secureHMACEqual",
	}
	for filename, function := range expectedCalls {
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch target := call.Fun.(type) {
			case *ast.Ident:
				found = found || target.Name == function
			case *ast.SelectorExpr:
				found = found || target.Sel.Name == function
			}
			return true
		})
		if !found {
			t.Fatalf("%s does not call %s", filename, function)
		}
	}
}
