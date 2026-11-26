package zitadel

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// helivanta#1859: only Helivanta mails a Helivanta user. Zitadel delivers its
// own, Tesserix-branded mail when a v2 command carries "sendCode" or
// "sendLink"; every command here must ask for "returnCode" and hand the code
// to signupmail instead. This scans every non-test Go file in the module, so a
// new command cannot quietly reintroduce Zitadel's mail.
func TestNoCommandAsksZitadelToSendMail(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		scanned++
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, err := strconv.Unquote(lit.Value); err == nil && (value == "sendCode" || value == "sendLink") {
				t.Errorf("%s asks Zitadel to send its own mail (%q); use returnCode and signupmail", path, value)
			}
			return true
		})
		return nil
	})
	if err != nil || scanned < 20 {
		t.Fatalf("scan incomplete: %v (%d files)", err, scanned)
	}
}
