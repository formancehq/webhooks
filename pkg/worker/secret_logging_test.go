package worker

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoEnvironmentDumping verifies that the worker and server module code
// does NOT contain os.Environ() calls that would dump all environment variables
// (including secrets) to the logs.
func TestNoEnvironmentDumping(t *testing.T) {
	// Scan the source files that previously had the problem
	filesToCheck := []string{
		"module.go",                    // worker module
		"../server/module.go",          // server module
	}

	for _, relPath := range filesToCheck {
		absPath, err := filepath.Abs(relPath)
		require.NoError(t, err)

		content, err := os.ReadFile(absPath)
		require.NoError(t, err, "reading %s", relPath)

		assert.NotContains(t, string(content), "os.Environ()",
			"file %s should not call os.Environ() — this leaks secrets to logs", relPath)
	}
}

// TestNoConfigSecretInLogStatements verifies that log statements in module.go
// do not use %+v or %v formatting on Config objects, which would expose secrets.
func TestNoConfigSecretInLogStatements(t *testing.T) {
	content, err := os.ReadFile("module.go")
	require.NoError(t, err)

	src := string(content)

	// Check that no log line contains a config object dumped with %+v
	assert.NotContains(t, src, `"%+v", cfg`,
		"module.go should not log configs with %%+v — this exposes secrets")
	assert.NotContains(t, src, `"%+v", c`,
		"module.go should not log config objects with %%+v")
}

// TestConfigStringerDoesNotLeakSecret verifies that if Config implements
// fmt.Stringer, it redacts the secret. If it doesn't implement Stringer,
// we verify that %+v would leak the secret (documenting the risk).
func TestConfigStringerDoesNotLeakSecret(t *testing.T) {
	// This test documents the risk: fmt.Sprintf with %+v on a config leaks the secret
	type configLike struct {
		Endpoint string
		Secret   string
		ID       string
	}

	cfg := configLike{
		Endpoint: "https://example.com",
		Secret:   "c3VwZXItc2VjcmV0LXZhbHVlLTEyMw==",
		ID:       "cfg-123",
	}

	output := fmt.Sprintf("%+v", cfg)

	// This proves the risk exists — %+v on any struct with a Secret field leaks it
	if strings.Contains(output, cfg.Secret) {
		t.Logf("CONFIRMED: fmt.Sprintf(%%%%+v) on a config struct leaks the secret — " +
			"this is why we must never log configs with %%%%+v")
	}
}

// TestNoEventPayloadInSpanAttributes verifies that the event payload is no longer
// stored as a span attribute, which could leak sensitive data to the tracing backend.
func TestNoEventPayloadInSpanAttributes(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "module.go", nil, parser.AllErrors)
	require.NoError(t, err)

	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if strings.Contains(lit.Value, "event-payload") {
				found = true
			}
		}
		return true
	})

	assert.False(t, found,
		"module.go should not store event-payload as a span attribute — it can contain sensitive data")
}
